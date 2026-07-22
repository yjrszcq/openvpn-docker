package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/cli"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
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
	for _, group := range []string{"server", "config", "client", "state", "repair", "migrate", "runtime", "completion", "version"} {
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

func TestHelpFormsShareDetailedLeafOutput(t *testing.T) {
	paths := [][]string{
		{"server", "init"},
		{"config", "apply"},
		{"client", "create"},
		{"client", "address", "edit"},
		{"state", "doctor"},
		{"repair", "apply"},
		{"migrate", "apply"},
		{"runtime", "logs"},
		{"completion"}, {"version"},
	}
	for _, path := range paths {
		byTopic := append([]string{"help"}, path...)
		code, topicOutput, stderr := run(byTopic...)
		if code != 0 || stderr != "" {
			t.Fatalf("help %v code=%d stderr=%q", path, code, stderr)
		}
		byFlag := append(append([]string(nil), path...), "-h")
		code, flagOutput, stderr := run(byFlag...)
		if code != 0 || stderr != "" || flagOutput != topicOutput {
			t.Fatalf("flag help %v code=%d stderr=%q\ntopic=%q\nflag=%q", path, code, stderr, topicOutput, flagOutput)
		}
		if !strings.Contains(topicOutput, "Usage: ovpn "+strings.Join(path, " ")) || !strings.Contains(topicOutput, "Examples:") {
			t.Errorf("help %v lacks usage/examples: %q", path, topicOutput)
		}
	}
}

func TestEveryCommandHasUsefulHelp(t *testing.T) {
	paths := [][]string{
		{"server"}, {"server", "init"}, {"server", "run"}, {"server", "render"},
		{"config"}, {"config", "validate"}, {"config", "show"}, {"config", "export"}, {"config", "plan"}, {"config", "apply"},
		{"client"}, {"client", "create"}, {"client", "list"}, {"client", "export"}, {"client", "rename"}, {"client", "revoke"}, {"client", "reissue"}, {"client", "delete"},
		{"client", "address"}, {"client", "address", "set"}, {"client", "address", "edit"}, {"client", "address", "release"},
		{"state"}, {"state", "show"}, {"state", "doctor"},
		{"repair"}, {"repair", "plan"}, {"repair", "apply"},
		{"migrate"}, {"migrate", "plan"}, {"migrate", "apply"},
		{"runtime"}, {"runtime", "status"}, {"runtime", "disconnect"}, {"runtime", "health"}, {"runtime", "capabilities"}, {"runtime", "logs"}, {"runtime", "events"},
		{"version"},
	}
	for _, path := range paths {
		code, stdout, stderr := run(append([]string{"help"}, path...)...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
			t.Errorf("help %v code=%d stdout=%q stderr=%q", path, code, stdout, stderr)
		}
		if len(path) > 0 && strings.Contains(stdout, "  "+path[len(path)-1]+"  ") && len(stdout) < 40 {
			t.Errorf("help %v is not useful: %q", path, stdout)
		}
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
	if info.Version != "4.0.1" || info.DataSchema != 4 || info.GoVersion == "" || info.Dependencies.SQLite == "" || info.Dependencies.YAML == "" {
		t.Fatalf("unexpected version info: %+v", info)
	}
}

func TestTopLevelVersionAliases(t *testing.T) {
	code, stdout, stderr := run("-v")
	if code != 0 || strings.TrimSpace(stdout) != "4.0.1" || stderr != "" {
		t.Fatalf("short alias code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, alias := range []string{"-V", "--version"} {
		code, aliasOutput, aliasError := run(alias)
		if code != 0 || aliasError != "" || !strings.Contains(aliasOutput, "ovpn 4.0.1") || !strings.Contains(aliasOutput, "data schema: 4") {
			t.Errorf("alias %s code=%d stdout=%q stderr=%q", alias, code, aliasOutput, aliasError)
		}
	}
	for _, alias := range []string{"-v", "-V", "--version"} {
		code, _, stderr = run(alias, "extra")
		if code != 64 || stderr == "" {
			t.Errorf("alias %s accepted extra argument: code=%d stderr=%q", alias, code, stderr)
		}
	}
}

func TestSafeGroupDefaults(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("OVPN_DATA_DIR", dataDir)
	t.Setenv("OVPN_RUNTIME_DIR", filepath.Join(dataDir, "run"))
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))

	code, stdout, stderr := run("client")
	if code != 78 || stdout != "" || !strings.Contains(stderr, "SQLite database is missing") {
		t.Fatalf("client default code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("state")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "state: EMPTY") {
		t.Fatalf("state default code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("runtime")
	if code != 78 || stdout != "" || !strings.Contains(stderr, "runtime state is invalid") {
		t.Fatalf("runtime default code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = run("state", "-j")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"state":"EMPTY"`) {
		t.Fatalf("state option forwarding code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("runtime", "-j")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"runtime_state_refused"`) {
		t.Fatalf("runtime option forwarding code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	clientRoot := createClientPreflightFixture(t)
	t.Setenv("OVPN_DATA_DIR", clientRoot)
	aliasCode, aliasOutput, aliasError := run("client", "-d", "-j")
	listCode, listOutput, listError := run("client", "list", "-d", "-j")
	if aliasCode != listCode || aliasOutput != listOutput || aliasError != listError {
		t.Fatalf("client option forwarding differs: alias=(%d, %q, %q) list=(%d, %q, %q)", aliasCode, aliasOutput, aliasError, listCode, listOutput, listError)
	}
}

func TestVersionJSONUsageError(t *testing.T) {
	code, stdout, stderr := run("version", "--json", "--short")
	if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
		t.Fatalf("version usage code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAmbiguousGroupStillFailsExplicitly(t *testing.T) {
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
	root, _ := createConfigurationFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_MAINTENANCE", "false")
	code, stdout, stderr = run("migrate", "apply", "--yes", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"applied":false`) || !strings.Contains(stdout, `"source_schema":4`) {
		t.Fatalf("current no-op code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRepairCLIPlanAndConfirmation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
	code, stdout, stderr := run("repair", "plan", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"state":"EMPTY"`) || !strings.Contains(stdout, `"actions":[]`) {
		t.Fatalf("repair plan code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("repair", "apply")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "No automatic repairs are needed") {
		t.Fatalf("repair no-op code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
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
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))
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
	if code != 0 || strings.TrimSpace(stdout.String()) != "4.0.1" || stderr.Len() != 0 {
		t.Fatalf("ovpn entrypoint code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.RunEntrypoint([]string{"version", "--short"}, &stdout, &stderr)
	if code != 0 || strings.TrimSpace(stdout.String()) != "4.0.1" || stderr.Len() != 0 {
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
	stderr.Reset()
	if code := cli.RunHook([]string{"pool-persist", "/tmp/openvpn-client-connect"}, &stderr); code != 65 || !strings.Contains(stderr.String(), "hook input is invalid") {
		t.Fatalf("OpenVPN positional hook code=%d stderr=%q", code, stderr.String())
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

func TestClientMutationUsageErrorsAreStructuredJSON(t *testing.T) {
	commands := [][]string{
		{"client", "create", "-j"},
		{"client", "create", "laptop", "-j", "-o", "-"},
		{"client", "rename", "laptop", "-j"},
		{"client", "revoke", "-j"},
		{"client", "reissue", "-j"},
		{"client", "reissue", "laptop", "-j", "-o", "-"},
		{"client", "delete", "-j"},
		{"client", "address", "set", "laptop", "-j"},
		{"client", "address", "edit", "-j"},
		{"client", "address", "release", "-j"},
	}
	for _, args := range commands {
		code, stdout, stderr := run(args...)
		if code != 64 || stdout != "" || !strings.Contains(stderr, `"kind":"usage"`) {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	t.Setenv("OVPN_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	code, stdout, stderr := run("client", "create", "laptop", "-j")
	if code != 78 || stdout != "" || !strings.Contains(stderr, `"kind":"client_state_refused"`) {
		t.Fatalf("mutation state error code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestClientMutationJSONSuccessUsesVersionedFullIDs(t *testing.T) {
	root := createClientPreflightFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	t.Setenv("OVPN_RUNTIME_DIR", filepath.Join(t.TempDir(), "run"))
	t.Setenv("OVPN_COMPATIBILITY_FILE", filepath.Join("..", "..", "compatibility", "contract.json"))
	t.Setenv("OVPN_TEMPLATE_ROOT", filepath.Join("..", "..", "templates"))

	code, stdout, stderr := run("client", "rename", "laptop", "laptop", "-j")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"version":1`) || !strings.Contains(stdout, `"id":"20000000-0000-4000-8000-000000000002"`) {
		t.Fatalf("rename JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run("client", "address", "set", "laptop", "-4", "dynamic", "-j")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"version":1`) || !strings.Contains(stdout, `"kick_required":["20000000-0000-4000-8000-000000000002"]`) || !strings.Contains(stdout, `"status":"pending"`) {
		t.Fatalf("address JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
		{"client", "reissue", "--name", "laptop", "--ipv4", "dynamic", "--ipv4", "auto"},
		{"client", "delete", "--name", "laptop", "--yes", "--yes"},
	} {
		code, _, stderr := run(args...)
		if code != 64 || stderr == "" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
	root := createClientPreflightFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	code, _, stderr := run("client", "delete", "missing")
	if code != 65 || !strings.Contains(stderr, "not found") {
		t.Fatalf("delete preflight code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = run("client", "delete", "--name", "laptop")
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
	root := createClientPreflightFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	code, _, stderr := run("client", "address", "edit", "missing")
	if code != 65 || !strings.Contains(stderr, "not found") {
		t.Fatalf("address edit preflight code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = run("client", "address", "edit", "--all")
	if code != 78 || !strings.Contains(stderr, "confirm") {
		t.Fatalf("unconfirmed address edit code=%d stderr=%q", code, stderr)
	}
}

func createClientPreflightFixture(t *testing.T) string {
	t.Helper()
	root, _ := createConfigurationFixture(t)
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	if err == nil {
		err = store.CreateClient(context.Background(), instance.ID, storesqlite.ClientState{
			Client:    domain.Client{ID: "20000000-0000-4000-8000-000000000002", Name: "laptop", Status: domain.ClientActive},
			CreatedAt: time.Now().UTC().Truncate(time.Second),
		})
	}
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return root
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := run("unknown")
	if code != 64 || !strings.Contains(stderr, "unknown command") || !strings.Contains(stderr, "hint:") {
		t.Fatalf("unknown command code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownNestedCommandIsUsageError(t *testing.T) {
	for _, args := range [][]string{{"server", "unknown"}, {"client", "createe"}} {
		code, _, stderr := run(args...)
		if code != 64 || !strings.Contains(stderr, "unknown command "+strings.Join(args, " ")) {
			t.Fatalf("unknown nested command args=%v code=%d stderr=%q", args, code, stderr)
		}
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
	stdout.Reset()
	stderr.Reset()
	if code := cli.RunBroker([]string{"-h"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "-l PATH") || stderr.Len() != 0 {
		t.Fatalf("broker short help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.RunBroker([]string{"-v"}, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) == "" || stderr.Len() != 0 {
		t.Fatalf("broker short version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.RunBroker([]string{"-l", "/tmp/listen", "--listen", "/tmp/other", "-b", "/tmp/backend", "-r", "/tmp/raw", "-m", "1", "-B", "1", "-t", "1s"}, &stdout, &stderr); code != 64 || !strings.Contains(stderr.String(), "repeated") {
		t.Fatalf("broker mixed duplicate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.RunBroker([]string{"-l", "/tmp/same", "-b", "/tmp/same", "-r", "/tmp/raw", "-m", "1", "-B", "1", "-t", "1s"}, &stdout, &stderr); code != 65 || !strings.Contains(stderr.String(), "broker configuration is invalid") {
		t.Fatalf("broker all-short parse code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
