package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCreatesConsistentIndependentSnapshot(t *testing.T) {
	store, instance := storeWithInstance(t)
	client := staticClient(t, instance.NetworkID, "12121212-1212-4212-8212-121212121212", "backup", "10.42.0.50")
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot", "state.db")
	if err := store.Backup(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode=%v", info.Mode().Perm())
	}
	backup, err := Open(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	loaded, err := backup.LoadClient(context.Background(), instance.ID, client.Client.ID)
	if err != nil || loaded.Client != client.Client {
		t.Fatalf("backup client=%+v err=%v", loaded, err)
	}
	events, err := backup.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("backup audit events=%d err=%v", len(events), err)
	}
	second := staticClient(t, instance.NetworkID, "13131313-1313-4313-8313-131313131313", "after", "10.42.0.51")
	if err := store.CreateClient(context.Background(), instance.ID, second); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.LoadClient(context.Background(), instance.ID, second.Client.ID); err == nil {
		t.Fatal("post-backup mutation appeared in snapshot")
	}
}

func TestBackupRefusesOverwriteAndCleansCanceledTarget(t *testing.T) {
	store, _ := storeWithInstance(t)
	destination := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Backup(context.Background(), destination); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite error=%v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing backup changed: %q err=%v", data, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceledPath := filepath.Join(t.TempDir(), "canceled.db")
	if err := store.Backup(canceled, canceledPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled backup error=%v", err)
	}
	if _, err := os.Stat(canceledPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled backup left target: %v", err)
	}
}

func TestBackupRejectsSourceAndUnsafeDestination(t *testing.T) {
	store, _ := storeWithInstance(t)
	if err := store.Backup(context.Background(), store.Path()); err == nil {
		t.Fatal("source database accepted as backup destination")
	}
	if err := store.Backup(context.Background(), "relative.db"); err == nil {
		t.Fatal("relative backup destination was accepted")
	}
}
