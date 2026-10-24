package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocationAndOverrides(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle with spaces")
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"ovpn", "runtime/manifest.json"} {
		if err := os.WriteFile(filepath.Join(root, p), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "ovpn")
	if err := os.Symlink(filepath.Join(root, "ovpn"), link); err != nil {
		t.Fatal(err)
	}
	inv, err := Prepare(link, "ovpn", nil, []string{"PATH=/usr/bin", "OVPN_DATA_DIR=/custom/data"})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(inv.Env, "\n")
	for _, want := range []string{"OVPN_DATA_DIR=/custom/data", "OVPN_EXEC_MODE=entrypoint", "OVPN_HOOK_BIN=" + filepath.Join(root, "bin", "ovpn-hook")} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing %s in %s", want, env)
		}
	}
	if inv.Path != filepath.Join(root, "runtime", "bin", "ovpn-core") {
		t.Fatal(inv)
	}
	inv, err = Prepare(link, "openssl", []string{"version"}, nil)
	// Dynamic dispatch is tested against a real placeholder file.
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "runtime", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "runtime", "bin", "openssl"), nil, 0755); err != nil {
		t.Fatal(err)
	}
	inv, err = Prepare(link, "openssl", []string{"version"}, nil)
	if err != nil || inv.Path != filepath.Join(root, "runtime", "lib", "ld-musl.so.1") || inv.Args[len(inv.Args)-1] != "version" {
		t.Fatalf("%+v %v", inv, err)
	}
}
func TestMissingRuntime(t *testing.T) {
	if _, err := Prepare(filepath.Join(t.TempDir(), "missing"), "ovpn", nil, nil); err == nil {
		t.Fatal("missing bundle accepted")
	}
}
