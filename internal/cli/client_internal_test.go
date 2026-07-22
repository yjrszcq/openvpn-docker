package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func TestDisplayClientID(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	if got := displayClientID(id, false); got != "111111112222" {
		t.Fatalf("short ID=%q", got)
	}
	if got := displayClientID(id, true); got != id {
		t.Fatalf("full ID=%q", got)
	}
}

func TestTakeClientOutputOptionsCanonicalizesAliases(t *testing.T) {
	options, remaining, err := takeClientOutputOptions([]string{"laptop", "-u", "-j"})
	if err != nil || !options.FullID || !options.JSON || len(remaining) != 1 || remaining[0] != "laptop" {
		t.Fatalf("options=%+v remaining=%v err=%v", options, remaining, err)
	}
	if _, _, err := takeClientOutputOptions([]string{"-u", "--full-id"}); err == nil {
		t.Fatal("mixed duplicate full-id option was accepted")
	}
	if _, _, err := takeClientOutputOptions([]string{"-j", "--json"}); err == nil {
		t.Fatal("mixed duplicate JSON option was accepted")
	}
}

func TestWriteClientMutationJSONPreservesFullIDs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := clientservice.MutationResult{
		Version:     1,
		OperationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Client: clientservice.View{
			ID: "11111111-2222-4333-8444-555555555555", Name: "laptop", Status: domain.ClientActive,
		},
	}
	if code := writeClientMutationJSON(&stdout, &stderr, result); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var decoded clientservice.MutationResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || decoded.Client.ID != result.Client.ID || decoded.OperationID != result.OperationID {
		t.Fatalf("decoded result=%+v", decoded)
	}
	stdout.Reset()
	envelope := clientMutationOutput{MutationResult: result, ProfileOutput: &clientProfileOutput{Destination: "client.ovpn", Written: true}}
	if code := writeClientMutationJSON(&stdout, &stderr, envelope); code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"profile_output":{"destination":"client.ovpn","written":true}`)) {
		t.Fatalf("envelope code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestTakeOutputOptionSupportsFilesAndStdout(t *testing.T) {
	output, remaining, err := takeOutputOption([]string{"laptop", "-o", "client.ovpn", "-4", "dynamic"})
	if err != nil || output != "client.ovpn" || len(remaining) != 3 {
		t.Fatalf("output=%q remaining=%v err=%v", output, remaining, err)
	}
	output, remaining, err = takeOutputOption([]string{"laptop", "--output", "-"})
	if err != nil || output != "-" || len(remaining) != 1 {
		t.Fatalf("stdout output=%q remaining=%v err=%v", output, remaining, err)
	}
	for _, args := range [][]string{{"laptop", "-o"}, {"laptop", "-o", "--json"}, {"laptop", "-o", "a", "--output", "b"}} {
		if _, _, err := takeOutputOption(args); err == nil {
			t.Fatalf("invalid output options were accepted: %v", args)
		}
	}
}

func TestProfileDestinationAndCommittedFailureReporting(t *testing.T) {
	var stdout bytes.Buffer
	output, err := writeProfileDestination("-", []byte("profile\n"), &stdout)
	if err != nil || stdout.String() != "profile\n" || output.Destination != "stdout" || !output.Written {
		t.Fatalf("stdout=%q output=%+v err=%v", stdout.String(), output, err)
	}
	if _, err := writeProfileDestination("-", []byte("profile"), failingWriter{}); err == nil {
		t.Fatal("stdout write failure was accepted")
	}
	result := clientservice.MutationResult{Version: 1, Client: clientservice.View{ID: "11111111-2222-4333-8444-555555555555", Name: "laptop"}}
	var stderr bytes.Buffer
	if code := writeCommittedProfileError(&stderr, result, "client.ovpn", errors.New("disk full"), true); code != 1 || !bytes.Contains(stderr.Bytes(), []byte(`"kind":"profile_output_failed"`)) || !bytes.Contains(stderr.Bytes(), []byte("was committed")) || !bytes.Contains(stderr.Bytes(), []byte("client.ovpn")) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestSelectAddressEditorUsesOverridesThenNano(t *testing.T) {
	t.Setenv("OVPN_EDITOR", "")
	t.Setenv("EDITOR", "")
	if editor, err := selectAddressEditor(""); err != nil || editor != "nano" {
		t.Fatalf("default editor=%q err=%v", editor, err)
	}
	t.Setenv("EDITOR", "vim")
	if editor, err := selectAddressEditor(""); err != nil || editor != "vim" {
		t.Fatalf("EDITOR selection=%q err=%v", editor, err)
	}
	t.Setenv("OVPN_EDITOR", "/usr/bin/nano")
	if editor, err := selectAddressEditor(""); err != nil || editor != "/usr/bin/nano" {
		t.Fatalf("OVPN_EDITOR selection=%q err=%v", editor, err)
	}
	t.Setenv("OVPN_EDITOR", "nano --softwrap")
	if _, err := selectAddressEditor(""); err == nil {
		t.Fatal("editor command with arguments was accepted")
	}
	t.Setenv("OVPN_EDITOR", "vim")
	if editor, err := selectAddressEditor("/usr/bin/nano"); err != nil || editor != "/usr/bin/nano" {
		t.Fatalf("command-line editor selection=%q err=%v", editor, err)
	}
	if _, err := selectAddressEditor("vim --clean"); err == nil {
		t.Fatal("command-line editor with arguments was accepted")
	}
}

func TestRuntimeReconcileIsBestEffort(t *testing.T) {
	client := clientservice.View{ID: "11111111-2222-4333-8444-555555555555", Name: "laptop", Status: domain.ClientActive}
	if result := reconcileClientRuntime(context.Background(), client, false); result.Status != "not_required" || result.Warning != "" {
		t.Fatalf("not-required result=%+v", result)
	}
	t.Setenv("OVPN_RUNTIME_DIR", t.TempDir())
	result := reconcileClientRuntime(context.Background(), client, true)
	if result.Status != "pending" || !strings.Contains(result.Warning, "runtime disconnect --id 111111112222") {
		t.Fatalf("pending result=%+v", result)
	}
	var stdout, stderr bytes.Buffer
	writeRuntimeReconcileHuman(&stdout, &stderr, result, false)
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "warning:") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteExportFileIsPrivateAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.ovpn")
	if err := writeExportFile(path, []byte("profile")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode=%v", info.Mode().Perm())
	}
	if err := writeExportFile(path, []byte("replacement")); err == nil {
		t.Fatal("export overwrote existing file")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "profile" {
		t.Fatalf("export content=%q err=%v", content, err)
	}
}
