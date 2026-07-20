package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const operationInstanceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestRecoverOperationsRollsBackPendingAndOrphanFiles(t *testing.T) {
	for name, withJournal := range map[string]bool{"pending": true, "orphan": false} {
		t.Run(name, func(t *testing.T) {
			dataDir, store, local := operationFixture(t)
			operationID := "11111111-1111-4111-8111-111111111111"
			if withJournal {
				prepareJournal(t, store, operationID)
			}
			installOperation(t, local, operationID, "server/server.conf", []byte("new\n"))
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := RecoverOperations(context.Background(), dataDir)
			if err != nil || len(result.Actions) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if _, err := os.Lstat(filepath.Join(dataDir, "server", "server.conf")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rolled-back artifact still exists: %v", err)
			}
			if pending, err := local.PendingOperations(); err != nil || len(pending) != 0 {
				t.Fatalf("pending artifacts=%v err=%v", pending, err)
			}
			if withJournal {
				reopened, err := storesqlite.Open(context.Background(), filepath.Join(dataDir, "meta", "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				journal, err := reopened.LoadOperation(context.Background(), operationID)
				if err != nil || journal.State != storesqlite.OperationRolledBack {
					t.Fatalf("journal=%+v err=%v", journal, err)
				}
			}
		})
	}
}

func TestRecoverOperationsFinishesCommittedArtifactCleanup(t *testing.T) {
	dataDir, store, local := operationFixture(t)
	operationID := "22222222-2222-4222-8222-222222222222"
	prepareJournal(t, store, operationID)
	content := []byte("committed\n")
	installOperation(t, local, operationID, "server/server.conf", content)
	payload := json.RawMessage(`{"version":1}`)
	now := time.Now().UTC()
	if err := store.AdvanceOperation(context.Background(), operationID, storesqlite.OperationFilesInstalled, payload, "", now); err != nil {
		t.Fatal(err)
	}
	metadata := storesqlite.ArtifactMetadata{ID: "33333333-3333-4333-8333-333333333333", OwnerKind: "instance", OwnerID: operationInstanceID, Kind: "server-config", Key: "server/server.conf", Digest: sha256.Sum256(content), Status: storesqlite.ArtifactActive}
	if err := store.CommitArtifactOperation(context.Background(), operationID, []storesqlite.ArtifactMetadata{metadata}, nil, payload, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverOperations(context.Background(), dataDir)
	if err != nil || len(result.Actions) != 1 || result.Actions[0].Action != "commit-finished" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(dataDir, "server", "server.conf")); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("committed data=%q err=%v", data, err)
	}
	if pending, err := local.PendingOperations(); err != nil || len(pending) != 0 {
		t.Fatalf("pending artifacts=%v err=%v", pending, err)
	}
}

func TestRecoverOperationsRejectsCommittedOrphan(t *testing.T) {
	dataDir, store, local := operationFixture(t)
	operationID := "44444444-4444-4444-8444-444444444444"
	operation := installOperation(t, local, operationID, "server/server.conf", []byte("unknown\n"))
	injected := errors.New("crash")
	if err := operation.Commit(func(point artifact.CrashPoint) error {
		if point == artifact.CrashAfterCommitMarker {
			return injected
		}
		return nil
	}); !errors.Is(err, injected) {
		t.Fatalf("commit crash error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverOperations(context.Background(), dataDir); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func operationFixture(t *testing.T) (string, *storesqlite.Store, *artifact.LocalStore) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "openvpn")
	store, err := storesqlite.Create(context.Background(), filepath.Join(dataDir, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	config, err := configservice.Parse([]byte("version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.42.0.0/24}\n"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(1, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(context.Background(), storesqlite.InstanceState{ID: operationInstanceID, CreatedAt: time.Now().UTC().Truncate(time.Second), Applied: snapshot}); err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return dataDir, store, local
}

func prepareJournal(t *testing.T, store *storesqlite.Store, operationID string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.PrepareOperation(context.Background(), storesqlite.Operation{ID: operationID, InstanceID: operationInstanceID, Kind: "test.operation", State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: json.RawMessage(`{"version":1}`), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func installOperation(t *testing.T, local *artifact.LocalStore, operationID, key string, content []byte) *artifact.Operation {
	t.Helper()
	operation, err := local.BeginOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), key, 0o600, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return operation
}
