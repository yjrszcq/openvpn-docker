package cli

import (
	"os"
	"path/filepath"
	"testing"
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

func TestTakeBooleanOptionCanonicalizesAliases(t *testing.T) {
	found, remaining, err := takeBooleanOption([]string{"laptop", "-u"}, "--full-id")
	if err != nil || !found || len(remaining) != 1 || remaining[0] != "laptop" {
		t.Fatalf("found=%t remaining=%v err=%v", found, remaining, err)
	}
	if _, _, err := takeBooleanOption([]string{"-u", "--full-id"}, "--full-id"); err == nil {
		t.Fatal("mixed duplicate full-id option was accepted")
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
