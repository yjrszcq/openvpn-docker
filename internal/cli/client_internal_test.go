package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
}

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
