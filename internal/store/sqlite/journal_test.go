package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAuthoritativeMutationsAppendAtomicAudit(t *testing.T) {
	store, instance := storeWithInstance(t)
	client := staticClient(t, instance.NetworkID, "abababab-abab-4bab-8bab-abababababab", "audited", "10.42.0.30")
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	updatedYAML := strings.Replace(storedConfigYAML, "vpn.example.test", "audit.example.test", 1)
	if err := store.ApplyConfig(context.Background(), instance.ID, appliedSnapshot(t, 2, updatedYAML)); err != nil {
		t.Fatalf("non-network configuration update failed: %v", err)
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "instance.created" || events[1].Type != "client.created" || events[2].Type != "config.applied" || events[0].Sequence >= events[1].Sequence || events[1].Sequence >= events[2].Sequence {
		t.Fatalf("unexpected audit events: %+v", events)
	}
	for _, event := range events {
		if event.PayloadVersion != 1 || !json.Valid(event.Payload) {
			t.Fatalf("invalid audit payload: %+v", event)
		}
	}
}

func TestOperationJournalTransitionsAndPendingQuery(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	operation := Operation{
		ID: "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd", InstanceID: instance.ID,
		Kind: "client.create", State: OperationPrepared, PayloadVersion: 1,
		RecoveryPayload: json.RawMessage(`{"stage":"prepared"}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.PrepareOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingOperations(context.Background(), instance.ID)
	if err != nil || len(pending) != 1 || pending[0].State != OperationPrepared {
		t.Fatalf("prepared pending=%+v err=%v", pending, err)
	}
	if err := store.AdvanceOperation(context.Background(), operation.ID, OperationFilesInstalled, json.RawMessage(`{"stage":"files"}`), "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingOperations(context.Background(), instance.ID)
	if err != nil || len(pending) != 1 || pending[0].State != OperationFilesInstalled {
		t.Fatalf("files pending=%+v err=%v", pending, err)
	}
	if err := store.AdvanceOperation(context.Background(), operation.ID, OperationCommitted, json.RawMessage(`{"stage":"done"}`), "", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingOperations(context.Background(), instance.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("committed operation remained pending: %+v err=%v", pending, err)
	}
	if err := store.AdvanceOperation(context.Background(), operation.ID, OperationRolledBack, json.RawMessage(`{}`), "", now.Add(3*time.Second)); err == nil {
		t.Fatal("terminal operation transition was accepted")
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[1].Type != "operation.prepared" || events[2].Type != "operation.files-installed" || events[3].Type != "operation.committed" {
		t.Fatalf("unexpected operation audit: %+v", events)
	}
}

func TestOperationFailureAndValidation(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Now().UTC().Truncate(time.Second)
	operation := Operation{ID: "dededede-dede-4ede-8ede-dededededede", InstanceID: instance.ID, Kind: "repair", State: OperationPrepared, PayloadVersion: 1, RecoveryPayload: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := store.PrepareOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceOperation(context.Background(), operation.ID, OperationFailed, json.RawMessage(`{"retry":true}`), "", now.Add(time.Second)); err == nil {
		t.Fatal("failed transition without failure text was accepted")
	}
	if err := store.AdvanceOperation(context.Background(), operation.ID, OperationFailed, json.RawMessage(`{"retry":true}`), "certificate mismatch", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingOperations(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	invalid := operation
	invalid.ID = "efefefef-efef-4fef-8fef-efefefefefef"
	invalid.RecoveryPayload = json.RawMessage(`not-json`)
	if err := store.PrepareOperation(context.Background(), invalid); err == nil {
		t.Fatal("invalid recovery JSON was accepted")
	}
}

func TestAuditFailureRollsBackBusinessMutation(t *testing.T) {
	store, instance := storeWithInstance(t)
	if _, err := store.db.Exec("DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	client := staticClient(t, instance.NetworkID, "fafafafa-fafa-4afa-8afa-fafafafafafa", "rollback", "10.42.0.40")
	if err := store.CreateClient(context.Background(), instance.ID, client); err == nil {
		t.Fatal("client committed without its audit event")
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM clients WHERE id = ?", client.Client.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit failure left client state: count=%d err=%v", count, err)
	}
}
