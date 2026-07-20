package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"
)

func TestCommitArtifactOperationIsAtomicWithJournalAndAudit(t *testing.T) {
	store, instance := storeWithInstance(t)
	client := staticClient(t, instance.NetworkID, "41414141-4141-4414-8414-414141414141", "artifact-owner", "10.42.0.10")
	client.Artifacts = []ArtifactMetadata{
		{ID: "42424242-4242-4424-8424-424242424242", OwnerKind: "client", OwnerID: client.Client.ID, Kind: "profile", Key: "clients/active/artifact-owner.ovpn", Digest: sha256.Sum256([]byte("old")), Status: ArtifactActive},
		{ID: "43434343-4343-4434-8434-434343434343", OwnerKind: "client", OwnerID: client.Client.ID, Kind: "ccd", Key: "ccd/" + client.Client.ID, Digest: sha256.Sum256([]byte("old-ccd")), Status: ArtifactActive},
	}
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	operationID := "44444444-4444-4444-8444-444444444444"
	payload := json.RawMessage(`{"version":1,"kind":"test"}`)
	prepareFilesInstalledOperation(t, store, instance.ID, operationID, payload)
	newDigest := sha256.Sum256([]byte("new"))
	active := []ArtifactMetadata{{ID: "45454545-4545-4454-8454-454545454545", OwnerKind: "client", OwnerID: client.Client.ID, Kind: "profile", Key: "clients/active/artifact-owner.ovpn", Digest: newDigest, Status: ArtifactActive}}
	deleted := []ArtifactDeletion{{OwnerKind: "client", OwnerID: client.Client.ID, Key: "ccd/" + client.Client.ID}}
	if err := store.CommitArtifactOperation(context.Background(), operationID, active, deleted, payload, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadClient(context.Background(), instance.ID, client.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Artifacts) != 2 || loaded.Artifacts[0].Kind != "ccd" || loaded.Artifacts[0].Status != ArtifactDeleted || loaded.Artifacts[1].Digest != newDigest || loaded.Artifacts[1].Status != ArtifactActive {
		t.Fatalf("unexpected committed artifacts: %+v", loaded.Artifacts)
	}
	operation, err := store.LoadOperation(context.Background(), operationID)
	if err != nil || operation.State != OperationCommitted {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var refreshed, committed bool
	for _, event := range events {
		if event.OperationID == operationID && event.Type == "artifacts.refreshed" {
			refreshed = true
		}
		if event.OperationID == operationID && event.Type == "operation.committed" {
			committed = true
		}
	}
	if !refreshed || !committed {
		t.Fatalf("artifact commit audit is incomplete: %+v", events)
	}
}

func TestCommitArtifactOperationRejectsForeignOwnerWithoutPartialState(t *testing.T) {
	store, instance := storeWithInstance(t)
	operationID := "46464646-4646-4464-8464-464646464646"
	payload := json.RawMessage(`{"version":1}`)
	prepareFilesInstalledOperation(t, store, instance.ID, operationID, payload)
	active := []ArtifactMetadata{{ID: "47474747-4747-4474-8474-474747474747", OwnerKind: "client", OwnerID: "48484848-4848-4484-8484-484848484848", Kind: "profile", Key: "clients/active/foreign.ovpn", Digest: sha256.Sum256([]byte("foreign")), Status: ArtifactActive}}
	if err := store.CommitArtifactOperation(context.Background(), operationID, active, nil, payload, time.Now().UTC()); err == nil {
		t.Fatal("foreign artifact owner was accepted")
	}
	operation, err := store.LoadOperation(context.Background(), operationID)
	if err != nil || operation.State != OperationFilesInstalled {
		t.Fatalf("failed commit changed journal: %+v err=%v", operation, err)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM artifacts WHERE artifact_key = 'clients/active/foreign.ovpn'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed commit persisted metadata count=%d err=%v", count, err)
	}
}

func prepareFilesInstalledOperation(t *testing.T, store *Store, instanceID, operationID string, payload json.RawMessage) {
	t.Helper()
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	if err := store.PrepareOperation(context.Background(), Operation{ID: operationID, InstanceID: instanceID, Kind: "artifacts.test", State: OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceOperation(context.Background(), operationID, OperationFilesInstalled, payload, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
