package artifact_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
)

const operationID = "41414141-4141-4414-8414-414141414141"

func newStore(t *testing.T) *artifact.LocalStore {
	t.Helper()
	store, err := artifact.NewLocal(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestOperationInstallsAndCommitsArtifacts(t *testing.T) {
	store := newStore(t)
	operation, err := store.BeginOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("private material\n")
	reference, err := operation.Stage(context.Background(), "pki/private/server.key", 0o600, bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	if reference.Backend != artifact.BackendLocal || reference.Key != "pki/private/server.key" || reference.Digest != sha256.Sum256(want) || reference.Mode != 0o600 {
		t.Fatalf("unexpected reference: %+v", reference)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if operation.State() != artifact.OperationFilesInstalled {
		t.Fatalf("operation state=%s", operation.State())
	}
	data, loaded, err := store.Read(context.Background(), reference.Key)
	if err != nil || !bytes.Equal(data, want) || loaded != reference {
		t.Fatalf("installed artifact=%q reference=%+v err=%v", data, loaded, err)
	}
	if err := operation.Commit(nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingOperations(); err != nil || len(pending) != 0 {
		t.Fatalf("pending operations=%v err=%v", pending, err)
	}
}

func TestRollbackRestoresReplacedArtifact(t *testing.T) {
	store := newStore(t)
	installArtifact(t, store, "server/server.conf", 0o600, "original\n", operationID)
	operation, err := store.BeginOperation("42424242-4242-4424-8424-424242424242")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "server/server.conf", 0o600, strings.NewReader("replacement\n")); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := operation.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, _, err := store.Read(context.Background(), "server/server.conf")
	if err != nil || string(data) != "original\n" {
		t.Fatalf("rollback content=%q err=%v", data, err)
	}
}

func TestInterruptedInstallCanBeReopenedAndRolledBack(t *testing.T) {
	store := newStore(t)
	installArtifact(t, store, "pki/ca.crt", 0o644, "old CA\n", operationID)
	id := "43434343-4343-4434-8434-434343434343"
	operation, err := store.BeginOperation(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "pki/ca.crt", 0o644, strings.NewReader("new CA\n")); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("simulated crash")
	err = operation.Install(context.Background(), func(point artifact.CrashPoint) error {
		if point == artifact.CrashAfterInstall {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("crash error=%v", err)
	}
	reopened, err := store.OpenOperation(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, _, err := store.Read(context.Background(), "pki/ca.crt")
	if err != nil || string(data) != "old CA\n" {
		t.Fatalf("recovered content=%q err=%v", data, err)
	}
}

func TestOperationRejectsUnsafePathsModesAndSymlinks(t *testing.T) {
	store := newStore(t)
	for _, key := range []string{"", ".", "../escape", "/absolute", "pki//key", "pki\\key", ".operations/hidden"} {
		if err := artifact.ValidateKey(key); !errors.Is(err, artifact.ErrUnsafePath) {
			t.Errorf("key %q error=%v", key, err)
		}
	}
	operation, err := store.BeginOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "pki/key", 0o666, strings.NewReader("bad")); err == nil {
		t.Fatal("unsafe mode was accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(store.Root(), "pki")); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "pki/key", 0o600, strings.NewReader("secret")); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); !errors.Is(err, artifact.ErrUnsafePath) {
		t.Fatalf("symlink containment error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact escaped root: %v", err)
	}
}

func TestSnapshotPreservesContentModeAndDigest(t *testing.T) {
	store := newStore(t)
	installArtifact(t, store, "pki/ca.crt", 0o644, "CA\n", operationID)
	installArtifact(t, store, "secrets/tls-crypt.key", 0o600, "SECRET\n", "44444444-4444-4444-8444-444444444444")
	destination := filepath.Join(t.TempDir(), "snapshot")
	if err := store.Snapshot(context.Background(), []string{"secrets/tls-crypt.key", "pki/ca.crt"}, destination); err != nil {
		t.Fatal(err)
	}
	for key, mode := range map[string]os.FileMode{"pki/ca.crt": 0o644, "secrets/tls-crypt.key": 0o600, ".ovpn-artifact-snapshot.json": 0o600} {
		info, err := os.Stat(filepath.Join(destination, filepath.FromSlash(key)))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("snapshot %s mode=%v", key, info.Mode().Perm())
		}
	}
	manifest, err := os.ReadFile(filepath.Join(destination, ".ovpn-artifact-snapshot.json"))
	if err != nil || !bytes.Contains(manifest, []byte(`"key":"pki/ca.crt"`)) || bytes.Contains(manifest, []byte("SECRET")) {
		t.Fatalf("snapshot manifest=%s err=%v", manifest, err)
	}
	if err := store.Snapshot(context.Background(), []string{"pki/ca.crt"}, destination); err == nil {
		t.Fatal("snapshot overwrote an existing destination")
	}
}

func TestStagedDigestTamperPreventsAnyInstall(t *testing.T) {
	store := newStore(t)
	operation, err := store.BeginOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "server/server.conf", 0o600, strings.NewReader("server")); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), "pki/ca.crt", 0o644, strings.NewReader("ca")); err != nil {
		t.Fatal(err)
	}
	stagedCA := filepath.Join(store.Root(), ".operations", operationID, "staged", "pki", "ca.crt")
	if err := os.WriteFile(stagedCA, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); !errors.Is(err, artifact.ErrOperationState) {
		t.Fatalf("tamper error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "server", "server.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("install mutated an earlier target before validating all staged files")
	}
}

func TestOperationDeletionCommitsAndRollsBack(t *testing.T) {
	store := newStore(t)
	installArtifact(t, store, "ccd/client-id", 0o600, "ifconfig-push\n", operationID)
	rollback, err := store.BeginOperation("45454545-4545-4454-8454-454545454545")
	if err != nil {
		t.Fatal(err)
	}
	if err := rollback.Remove("ccd/client-id"); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "ccd", "client-id")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact remains: %v", err)
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, _, err := store.Read(context.Background(), "ccd/client-id")
	if err != nil || string(data) != "ifconfig-push\n" {
		t.Fatalf("restored deletion=%q err=%v", data, err)
	}
	commit, err := store.BeginOperation("46464646-4646-4464-8464-464646464646")
	if err != nil {
		t.Fatal(err)
	}
	if err := commit.Remove("ccd/client-id"); err != nil {
		t.Fatal(err)
	}
	if err := commit.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := commit.Commit(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "ccd", "client-id")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed deletion remains: %v", err)
	}
}

func installArtifact(t *testing.T, store *artifact.LocalStore, key string, mode os.FileMode, content, id string) {
	t.Helper()
	operation, err := store.BeginOperation(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), key, mode, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := operation.Commit(nil); err != nil {
		t.Fatal(err)
	}
}
