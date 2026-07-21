package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/cli"
)

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHelpShowsPlannedGroups(t *testing.T) {
	code, stdout, stderr := run("--help")
	if code != 0 || stderr != "" {
		t.Fatalf("help code=%d stderr=%q", code, stderr)
	}
	for _, group := range []string{"server", "config", "client", "state", "repair", "migrate", "runtime", "version"} {
		if !strings.Contains(stdout, group) {
			t.Errorf("help is missing %q", group)
		}
	}
}

func TestNestedHelp(t *testing.T) {
	code, stdout, _ := run("client", "address", "--help")
	if code != 0 || !strings.Contains(stdout, "set") || !strings.Contains(stdout, "release") {
		t.Fatalf("nested help code=%d output=%q", code, stdout)
	}
}

func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := run("version", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("version code=%d stderr=%q", code, stderr)
	}
	var info buildinfo.Info
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("decode version JSON: %v", err)
	}
	if info.Version != "4.0.0-dev" || info.DataSchema != 4 || info.GoVersion == "" || info.Dependencies.SQLite == "" || info.Dependencies.YAML == "" {
		t.Fatalf("unexpected version info: %+v", info)
	}
}

func TestVersionJSONUsageError(t *testing.T) {
	code, stdout, stderr := run("version", "--json", "--short")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("version usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUnimplementedCommandFailsExplicitly(t *testing.T) {
	code, _, stderr := run("server")
	if code != 1 || !strings.Contains(stderr, "not implemented") {
		t.Fatalf("foundation command code=%d stderr=%q", code, stderr)
	}
}

func TestMigrationPlanUsageAndStructuredRefusal(t *testing.T) {
	code, stdout, stderr := run("migrate", "plan", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn migrate plan") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("migrate", "plan", "--json", "extra")
	if code != 64 || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("usage code=%d stderr=%q", code, stderr)
	}
	root := t.TempDir()
	t.Setenv("OVPN_DATA_DIR", root)
	code, stdout, stderr = run("migrate", "plan", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"migration_source_invalid"`) {
		t.Fatalf("refusal code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMigrationApplyConfirmationMaintenanceAndUsage(t *testing.T) {
	code, stdout, stderr := run("migrate", "apply", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn migrate apply") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("migrate", "apply", "--yes", "--yes")
	if code != 64 || !strings.Contains(stderr, "specified once") {
		t.Fatalf("duplicate code=%d stderr=%q", code, stderr)
	}
	t.Setenv("OVPN_MAINTENANCE", "false")
	code, stdout, stderr = run("migrate", "apply", "--yes", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"maintenance_required"`) {
		t.Fatalf("maintenance code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRepairCLIPlanAndConfirmation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"))
	code, stdout, stderr := run("repair", "plan", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"state":"EMPTY"`) || !strings.Contains(stdout, `"actions":[]`) {
		t.Fatalf("repair plan code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("repair", "apply")
	if code != 78 || stdout != "" || !strings.Contains(stderr, "not confirmed") {
		t.Fatalf("repair confirmation code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("repair", "apply", "--yes", "--yes")
	if code != 64 || !strings.Contains(stderr, "specified once") {
		t.Fatalf("repair duplicate option code=%d stderr=%q", code, stderr)
	}
}

func TestStateCLIReportsEmptyAndMissingDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"))
	code, stdout, stderr := run("state", "show", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"state":"EMPTY"`) || !strings.Contains(stdout, `"issues":[]`) {
		t.Fatalf("empty state code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy"), []byte("state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = run("state", "doctor")
	if code != 78 || stderr != "" || !strings.Contains(stdout, "SQLITE_MISSING") || !strings.Contains(stdout, "RESTORE_BACKUP") {
		t.Fatalf("missing state code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestServerInitUsageAndInvalidConfiguration(t *testing.T) {
	code, stdout, stderr := run("server", "init", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn server init") {
		t.Fatalf("init help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("server", "init", "extra")
	if code != 64 || !strings.Contains(stderr, "usage: ovpn server init") {
		t.Fatalf("init usage code=%d stderr=%q", code, stderr)
	}
	root := t.TempDir()
	configFile := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configFile, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_CONFIG_FILE", configFile)
	t.Setenv("OVPN_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("OVPN_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"))
	code, stdout, stderr = run("server", "init")
	if code != 65 || stdout != "" || !strings.Contains(stderr, "initialization configuration is invalid") {
		t.Fatalf("invalid init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestServerRunUsageAndMissingState(t *testing.T) {
	code, stdout, stderr := run("server", "run", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn server run") {
		t.Fatalf("run help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("server", "run", "extra")
	if code != 64 || !strings.Contains(stderr, "usage: ovpn server run") {
		t.Fatalf("run usage code=%d stderr=%q", code, stderr)
	}
	t.Setenv("OVPN_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	code, stdout, stderr = run("server", "run")
	if code != 78 || stdout != "" || !strings.Contains(stderr, "operation recovery was refused") {
		t.Fatalf("missing state code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestEntrypointDispatchesOVPNAndDefaultCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunEntrypoint([]string{"ovpn", "version", "--short"}, &stdout, &stderr)
	if code != 0 || strings.TrimSpace(stdout.String()) != "4.0.0-dev" || stderr.Len() != 0 {
		t.Fatalf("ovpn entrypoint code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.RunEntrypoint([]string{"version", "--short"}, &stdout, &stderr)
	if code != 0 || strings.TrimSpace(stdout.String()) != "4.0.0-dev" || stderr.Len() != 0 {
		t.Fatalf("default entrypoint code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHookUsageAndInvalidEnvironment(t *testing.T) {
	var stderr bytes.Buffer
	if code := cli.RunHook(nil, &stderr); code != 64 || !strings.Contains(stderr.String(), "ovpn-hook pool-persist") {
		t.Fatalf("hook usage code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	t.Setenv("script_type", "client-connect")
	t.Setenv("common_name", "not-a-uuid")
	if code := cli.RunHook([]string{"pool-persist"}, &stderr); code != 65 || !strings.Contains(stderr.String(), "hook input is invalid") {
		t.Fatalf("hook input code=%d stderr=%q", code, stderr.String())
	}
}

func TestRuntimeLogAndEventHistory(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "logs", "openvpn.log"), []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	event := `{"timestamp":"2026-07-20T00:00:00Z","event":"client_connection","operation":"connect","outcome":"applied","client_id":null,"client_name":null}` + "\n"
	if err := os.WriteFile(filepath.Join(dataDir, "logs", "events.jsonl"), []byte(event), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_DATA_DIR", dataDir)
	code, stdout, stderr := run("runtime", "logs", "--lines", "1", "--raw")
	if code != 0 || stdout != "second\n" || stderr != "" {
		t.Fatalf("logs code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("runtime", "events", "--lines", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"event":"client_connection"`) {
		t.Fatalf("events code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRuntimeStreamUsageErrors(t *testing.T) {
	code, stdout, stderr := run("runtime", "logs", "--lines", "-1")
	if code != 64 || stdout != "" || !strings.Contains(stderr, "between 0") {
		t.Fatalf("logs usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("runtime", "events", "--json", "--raw")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("events usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestClientQueryUsageAndStructuredStateError(t *testing.T) {
	code, stdout, stderr := run("client", "export", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn client export") {
		t.Fatalf("export help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("client", "export", "--name", "alpha", "--id", "11111111")
	if code != 64 || !strings.Contains(stderr, "exactly one") {
		t.Fatalf("mixed selector code=%d stderr=%q", code, stderr)
	}
	t.Setenv("OVPN_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	code, stdout, stderr = run("client", "list", "--json")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"client_state_refused"`) {
		t.Fatalf("list state error code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestClientCreateAndRenameUsage(t *testing.T) {
	code, stdout, stderr := run("client", "create", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn client create") {
		t.Fatalf("create help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"client", "create"},
		{"client", "create", "laptop", "--ipv4"},
		{"client", "create", "laptop", "--unknown", "value"},
		{"client", "rename", "--name", "laptop"},
		{"client", "rename", "--name", "laptop", "--id", "11111111", "new"},
	} {
		code, _, stderr := run(args...)
		if code != 64 || stderr == "" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestClientLifecycleUsageAndDeleteConfirmation(t *testing.T) {
	for _, command := range []string{"revoke", "reissue", "delete"} {
		code, stdout, stderr := run("client", command, "--help")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn client "+command) {
			t.Fatalf("%s help code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{
		{"client", "revoke", "--name", "laptop", "--release-ip"},
		{"client", "reissue", "--name", "laptop", "--ipv4"},
		{"client", "reissue", "--name", "laptop", "--ipv4", "dynamic", "--ipv4", "auto"},
		{"client", "delete", "--name", "laptop", "--yes", "--yes"},
	} {
		code, _, stderr := run(args...)
		if code != 64 || stderr == "" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
	code, _, stderr := run("client", "delete", "--name", "laptop")
	if code != 78 || !strings.Contains(stderr, "confirm") {
		t.Fatalf("non-TTY delete code=%d stderr=%q", code, stderr)
	}
}

func TestClientAddressUsageAndBatchConfirmation(t *testing.T) {
	for _, command := range []string{"set", "edit", "release"} {
		code, stdout, stderr := run("client", "address", command, "--help")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage: ovpn client address "+command) {
			t.Fatalf("address %s help code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{
		{"client", "address", "set", "--name", "laptop"},
		{"client", "address", "set", "--name", "laptop", "--ipv4", "dynamic", "--ipv4", "auto"},
		{"client", "address", "release", "--name", "laptop", "extra"},
		{"client", "address", "edit", "--all", "--name", "laptop", "--yes"},
		{"client", "address", "edit", "--name", "laptop", "--id", "11111111", "--yes"},
	} {
		code, _, stderr := run(args...)
		if code != 64 || stderr == "" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
	code, _, stderr := run("client", "address", "edit", "--all")
	if code != 78 || !strings.Contains(stderr, "confirm") {
		t.Fatalf("unconfirmed address edit code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := run("unknown")
	if code != 64 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown command code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownNestedCommandIsUsageError(t *testing.T) {
	code, _, stderr := run("server", "unknown")
	if code != 64 || !strings.Contains(stderr, "unknown command server unknown") {
		t.Fatalf("unknown nested command code=%d stderr=%q", code, stderr)
	}
}

func TestBrokerSkeleton(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.RunBroker([]string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "ovpn-broker") || stderr.Len() != 0 {
		t.Fatalf("broker help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := cli.RunBroker([]string{"run"}, &stdout, &stderr); code != 64 {
		t.Fatalf("broker invalid command code=%d, want 64", code)
	}
}

func TestConfigValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.42.0.0/24}\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_CONFIG_FILE", path)
	code, stdout, stderr := run("config", "validate", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"valid":true`) || !strings.Contains(stdout, `"dynamic_pool_size":126`) {
		t.Fatalf("validate code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigValidateJSONError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_CONFIG_FILE", path)
	code, stdout, stderr := run("config", "validate", "--json")
	if code != 65 || stdout != "" || !strings.Contains(stderr, `"kind":"invalid_config"`) {
		t.Fatalf("validate error code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
