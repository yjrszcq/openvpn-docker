package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestAPIKeyCLIWorkflow(t *testing.T) {
	root, _ := createConfigurationFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	code, stdout, stderr := run("api", "key", "create", "frontend", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var created apikey.CreateResult
	if err := json.Unmarshal([]byte(stdout), &created); err != nil || created.Secret == "" || created.Key.Name != "frontend" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	code, stdout, stderr = run("api", "key", "list", "--json")
	if code != 0 || stderr != "" || strings.Contains(stdout, created.Secret) || !strings.Contains(stdout, created.Key.ID) {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = run("api", "key", "delete", "--id", created.Key.ID[:8])
	if code != 78 || !strings.Contains(stderr, "confirm") {
		t.Fatalf("unconfirmed delete code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = run("api", "key", "delete", "--id", created.Key.ID[:8], "--yes", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, created.Key.ID) {
		t.Fatalf("delete code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service, _ := apikey.NewService(store, instance.ID)
	if _, err := service.Authenticate(context.Background(), created.Secret); err == nil {
		t.Fatal("deleted CLI key still authenticates")
	}
}

func TestAPIKeyCLISecretFileAndUsage(t *testing.T) {
	root, _ := createConfigurationFixture(t)
	t.Setenv("OVPN_DATA_DIR", root)
	output := filepath.Join(t.TempDir(), "api.key")
	code, stdout, stderr := run("api", "key", "create", "file-key", "--output", output)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "secret written") {
		t.Fatalf("file create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode=%v", info.Mode())
	}
	for _, args := range [][]string{
		{"api", "key", "create"},
		{"api", "key", "create", "x", "--json", "-j"},
		{"api", "key", "delete", "x", "--name", "x"},
		{"api", "key", "delete", "--id"},
	} {
		code, _, stderr := run(args...)
		if code != 64 || stderr == "" {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
}
