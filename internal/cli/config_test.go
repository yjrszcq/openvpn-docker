package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestConfigShowExportAndPlanUseSQLiteAppliedState(t *testing.T) {
	root, configPath := createConfigurationFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_CONFIG_FILE", configPath)

	code, stdout, stderr := run("config", "show", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"revision":1`) || !strings.Contains(stdout, `"network":"10.42.0.0/24"`) {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	exportPath := filepath.Join(t.TempDir(), "config.yaml")
	code, stdout, stderr = run("config", "export", "--output", exportPath)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err := os.ReadFile(exportPath)
	if err != nil || !strings.Contains(string(content), "endpoint: vpn.example.test") {
		t.Fatalf("export content=%q err=%v", content, err)
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode=%v", info.Mode().Perm())
	}

	code, stdout, stderr = run("config", "plan", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("plan code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var plan configurationservice.Plan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Configuration.InSync || len(plan.Configuration.Changes) != 1 || plan.Configuration.Changes[0].Field != "server.endpoint" || plan.Configuration.TargetRevision != 2 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestConfigQueryUsageAndJSONErrors(t *testing.T) {
	code, stdout, stderr := run("config", "show", "--json", "--json")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("duplicate JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	root, configPath := createConfigurationFixture(t)
	if err := os.WriteFile(configPath, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_CONFIG_FILE", configPath)
	code, stdout, stderr = run("config", "plan", "--json")
	if code != 65 || stdout != "" || !strings.Contains(stderr, `"kind":"invalid_config"`) || !strings.Contains(stderr, `"hint":`) || !strings.Contains(stderr, "field unknown") {
		t.Fatalf("invalid plan code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestClientListExplainsEmptyState(t *testing.T) {
	root, _ := createConfigurationFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	code, stdout, stderr := run("client", "list")
	if code != 0 || stdout != "No clients.\n" || stderr != "" {
		t.Fatalf("empty list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigApplyRequiresConfirmationAndCommitsOffline(t *testing.T) {
	root, configPath := createConfigurationFixture(t)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	t.Setenv("OVPN_CONFIG_FILE", configPath)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))

	code, stdout, stderr := run("config", "apply", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"configuration_preflight_refused"`) || !strings.Contains(stderr, "issue(s)") {
		t.Fatalf("preflight code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("config", "apply", "-f", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"confirmation_required"`) || strings.Contains(stderr, "Type yes") {
		t.Fatalf("confirmation code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("config", "apply", "--force", "--yes", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"applied":true`) || !strings.Contains(stdout, `"target_revision":2`) || !strings.Contains(stdout, `"restart_required":true`) || !strings.Contains(stdout, `"runtime_restarted":false`) || !strings.Contains(stdout, `"profile_redistribution":[]`) {
		t.Fatalf("apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	_ = store.Close()
	if err != nil || instance.Applied.Revision != 2 || instance.Applied.Config.Endpoint != "new.example.test" {
		t.Fatalf("applied instance=%+v err=%v", instance, err)
	}
	code, stdout, stderr = run("config", "apply", "--force", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"applied":false`) {
		t.Fatalf("in-sync apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, `"operation_id"`) {
		t.Fatalf("in-sync apply unexpectedly reported an operation: %q", stdout)
	}
}

func TestConfigApplyRejectsRepeatedForceAlias(t *testing.T) {
	code, stdout, stderr := run("config", "apply", "--json", "-f", "--force")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) || !strings.Contains(stderr, "--force may only be specified once") {
		t.Fatalf("duplicate force code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigApplyCoordinatesOnlineRuntimeRestart(t *testing.T) {
	root, configPath := createConfigurationFixture(t)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	shared, err := artifact.AcquireLock(context.Background(), artifact.RuntimeLockPath(root), artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "supervisor.sock"))
	if err != nil {
		_ = shared.Release()
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(connection)
		command, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(command) != "APPLY" {
			serverDone <- fmt.Errorf("apply command=%q err=%v", command, err)
			return
		}
		if err := shared.Release(); err != nil {
			serverDone <- err
			return
		}
		if _, err := connection.Write([]byte("READY\n")); err != nil {
			serverDone <- err
			return
		}
		command, err = reader.ReadString('\n')
		if err != nil || strings.TrimSpace(command) != "RESUME" {
			serverDone <- fmt.Errorf("resume command=%q err=%v", command, err)
			return
		}
		_, err = connection.Write([]byte("OK\n"))
		serverDone <- err
	}()
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	t.Setenv("OVPN_CONFIG_FILE", configPath)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
	t.Setenv("OVPN_MAINTENANCE", "false")

	code, stdout, stderr := run("config", "apply", "--force", "--yes", "--json")
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"applied":true`) || !strings.Contains(stdout, `"restart_required":false`) || !strings.Contains(stdout, `"runtime_restarted":true`) {
		t.Fatalf("online apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestServerRenderUsesAppliedSnapshotWithoutChangingState(t *testing.T) {
	root, _ := createConfigurationFixture(t)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))

	code, stdout, stderr := run("server", "render")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "server 10.42.0.0 255.255.255.0") || !strings.Contains(stdout, filepath.Join(root, "pki", "ca.crt")) {
		t.Fatalf("render code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	output := filepath.Join(t.TempDir(), "server.conf")
	code, stdout, stderr = run("server", "render", "--output", output)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("render output code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rendered output mode=%v", info.Mode().Perm())
	}
	code, _, stderr = run("server", "render", "--output", output)
	if code != 1 || !strings.Contains(stderr, "write rendered server configuration") {
		t.Fatalf("render overwrite code=%d stderr=%q", code, stderr)
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	_ = store.Close()
	if err != nil || instance.Applied.Revision != 1 {
		t.Fatalf("render changed state=%+v err=%v", instance, err)
	}
}

func TestConfigApplyMapsRuntimeLockToTemporaryFailure(t *testing.T) {
	root, configPath := createConfigurationFixture(t)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	t.Setenv("OVPN_CONFIG_FILE", configPath)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
	lock, err := artifact.AcquireLock(context.Background(), artifact.RuntimeLockPath(root), artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	code, stdout, stderr := run("config", "apply", "-f", "--yes", "--json")
	if code != 75 || stdout != "" || !strings.Contains(stderr, `"kind":"configuration_busy"`) {
		t.Fatalf("locked apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigApplyRequiresInterruptedOperationRecovery(t *testing.T) {
	root, configPath := createConfigurationFixture(t)
	runtimeDir := filepath.Join(t.TempDir(), "run")
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", runtimeDir)
	t.Setenv("OVPN_CONFIG_FILE", configPath)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	operation := storesqlite.Operation{ID: "93939393-9393-4939-8939-939393939393", InstanceID: instance.ID, Kind: "client.create", State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: json.RawMessage(`{"version":1}`), CreatedAt: now, UpdatedAt: now}
	if err := store.PrepareOperation(context.Background(), operation); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run("config", "apply", "--force", "--yes", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"operation_recovery_required"`) {
		t.Fatalf("pending apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func createConfigurationFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	data := []byte("version: 1\nserver:\n  endpoint: vpn.example.test\nipv4:\n  network: 10.42.0.0/24\n  dynamicPoolSize: 64\n")
	value, err := configservice.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(1, value)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storesqlite.Create(context.Background(), filepath.Join(root, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(context.Background(), storesqlite.InstanceState{ID: "10000000-0000-4000-8000-000000000001", CreatedAt: time.Now().UTC(), Applied: snapshot}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "desired.yaml")
	desired := strings.Replace(string(data), "vpn.example.test", "new.example.test", 1)
	if err := os.WriteFile(configPath, []byte(desired), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, configPath
}
