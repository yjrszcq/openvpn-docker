package cli_test

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestRuntimeDisconnectUsesResolvedUUIDAndJSON(t *testing.T) {
	root := createClientPreflightFixture(t)
	runtimeDir := t.TempDir()
	clientID := "20000000-0000-4000-8000-000000000002"
	commands := startCLIBrokerSequence(t, filepath.Join(runtimeDir, "management.sock"), [][]string{
		{"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address", "CLIENT_LIST\t" + clientID + "\t192.0.2.10:44321\t10.42.0.2", "END"},
		{"SUCCESS: common name found, client(s) killed"},
	})
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	code, stdout, stderr := run("runtime", "disconnect", "laptop", "-j")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"client_id":"`+clientID+`"`) || !strings.Contains(stdout, `"disconnected":true`) {
		t.Fatalf("disconnect code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if first, second := <-commands, <-commands; first != "status 3" || second != "kill "+clientID {
		t.Fatalf("commands=%q, %q", first, second)
	}
}

func TestClientListDetailCombinesRuntimeConnectionState(t *testing.T) {
	root := createClientPreflightFixture(t)
	runtimeDir := t.TempDir()
	clientID := "20000000-0000-4000-8000-000000000002"
	commands := startCLIBrokerSequence(t, filepath.Join(runtimeDir, "management.sock"), [][]string{
		{"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address", "CLIENT_LIST\t" + clientID + "\t192.0.2.10:44321\t10.42.0.2", "END"},
	})
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	code, stdout, stderr := run("client", "list", "--detail")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "CONNECTION") || !strings.Contains(stdout, "online") {
		t.Fatalf("online detail code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if command := <-commands; command != "status 3" {
		t.Fatalf("command=%q", command)
	}

	t.Setenv("OVPN_RUNTIME_DIR", t.TempDir())
	code, stdout, stderr = run("client", "list", "--detail", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"connection":"unknown"`) {
		t.Fatalf("unknown detail code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("client", "list", "--json")
	if code != 0 || stderr != "" || strings.Contains(stdout, `"connection"`) {
		t.Fatalf("plain list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestClientListDetailReportsOfflineWhenRuntimeIsAvailable(t *testing.T) {
	root := createClientPreflightFixture(t)
	runtimeDir := t.TempDir()
	commands := startCLIBrokerSequence(t, filepath.Join(runtimeDir, "management.sock"), [][]string{
		{"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address", "END"},
	})
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	code, stdout, stderr := run("client", "list", "-d")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "offline") {
		t.Fatalf("offline detail code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if command := <-commands; command != "status 3" {
		t.Fatalf("command=%q", command)
	}
}

func TestRuntimeDisconnectSupportsOfflineAndDeletedIdentities(t *testing.T) {
	root := createClientPreflightFixture(t)
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	deletedAt := instance.CreatedAt
	deletedID := "30000000-0000-4000-8000-000000000003"
	if err == nil {
		err = store.CreateClient(context.Background(), instance.ID, storesqlite.ClientState{Client: domain.Client{ID: deletedID, Name: "historical", Status: domain.ClientDeleted}, CreatedAt: deletedAt, DeletedAt: &deletedAt})
	}
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create tombstone err=%v close=%v", err, closeErr)
	}
	runtimeDir := t.TempDir()
	commands := startCLIBrokerSequence(t, filepath.Join(runtimeDir, "management.sock"), [][]string{{"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address", "END"}})
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	code, stdout, stderr := run("runtime", "disconnect", "--id", "30000000")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "historical [300000000000]") || !strings.Contains(stdout, "not connected") {
		t.Fatalf("offline code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if command := <-commands; command != "status 3" {
		t.Fatalf("command=%q", command)
	}
}

func TestRuntimeDisconnectErrorsAreStructured(t *testing.T) {
	code, stdout, stderr := run("runtime", "disconnect", "-j")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	root := createClientPreflightFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", t.TempDir())
	code, stdout, stderr = run("runtime", "disconnect", "laptop", "-j")
	if code != 69 || stdout != "" || !strings.Contains(stderr, `"kind":"runtime_unavailable"`) {
		t.Fatalf("unavailable code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func startCLIBrokerSequence(t *testing.T, path string, responses [][]string) <-chan string {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan string, len(responses))
	go func() {
		defer listener.Close()
		for _, response := range responses {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = connection.Write([]byte(">INFO:OpenVPN Management Broker Version 1\n"))
			command, _ := bufio.NewReader(connection).ReadString('\n')
			commands <- strings.TrimSpace(command)
			for _, line := range response {
				_, _ = connection.Write([]byte(line + "\n"))
			}
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(path) })
	return commands
}
