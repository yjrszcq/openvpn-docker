package cli

import (
	"os"
	"path/filepath"
	"testing"
)

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
