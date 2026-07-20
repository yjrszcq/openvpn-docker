package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func TestConfigurationOperationPreservesHistoricalNetworkAndAssignments(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	clientID := "41414141-4141-4414-8414-414141414141"
	address, _ := domain.ParseAddress("10.42.0.10")
	client := ClientState{Client: domain.Client{ID: clientID, Name: "alpha", Status: domain.ClientActive}, CreatedAt: now, Assignment: &AddressAssignment{ID: "42424242-4242-4424-8424-424242424242", NetworkID: instance.NetworkID, Kind: "static", Address: &address, Status: AssignmentActive, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	operationID := prepareConfigurationOperation(t, store, instance.ID, now)
	desiredYAML := strings.Replace(storedConfigYAML, "10.42.0.0/24", "10.43.0.0/24", 1)
	snapshot := appliedSnapshot(t, 2, desiredYAML)
	newAddress, _ := domain.ParseAddress("10.43.0.10")
	newNetworkID := "43434343-4343-4434-8434-434343434343"
	err := store.CommitConfigurationOperation(context.Background(), ConfigurationCommit{
		OperationID: operationID, InstanceID: instance.ID,
		ExpectedRevision: instance.Applied.Revision, ExpectedDigest: instance.Applied.Digest,
		Snapshot: snapshot, NewNetworkID: newNetworkID,
		AddressChanges:  []ConfigurationAddressChange{{ClientID: clientID, Name: "alpha", ClientStatus: domain.ClientActive, Assignment: AddressAssignment{ID: "44444444-4444-4444-8444-444444444444", NetworkID: newNetworkID, Kind: "static", Address: &newAddress, CreatedAt: now, UpdatedAt: now}}},
		RecoveryPayload: json.RawMessage(`{"version":1}`), UpdatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NetworkID != newNetworkID || loaded.Applied.Revision != 2 || loaded.Applied.Config.IPv4.Network.String() != "10.43.0.0/24" {
		t.Fatalf("applied instance=%+v", loaded)
	}
	loadedClient, err := store.LoadClient(context.Background(), instance.ID, clientID)
	if err != nil || loadedClient.Assignment == nil || loadedClient.Assignment.Address.String() != "10.43.0.10" || loadedClient.Assignment.NetworkID != newNetworkID {
		t.Fatalf("remapped client=%+v err=%v", loadedClient, err)
	}
	var networks, assignments, enabled int
	if err := store.db.QueryRow("SELECT count(*), sum(enabled) FROM networks WHERE instance_id = ?", instance.ID).Scan(&networks, &enabled); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM address_assignments WHERE client_id = ?", clientID).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if networks != 2 || enabled != 1 || assignments != 2 {
		t.Fatalf("history networks=%d enabled=%d assignments=%d", networks, enabled, assignments)
	}
	operation, err := store.LoadOperation(context.Background(), operationID)
	if err != nil || operation.State != OperationCommitted {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
}

func TestConfigurationOperationRollsBackOnAddressConstraint(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC)
	clients := []struct{ id, name string }{{"51515151-5151-4515-8515-515151515151", "alpha"}, {"52525252-5252-4525-8525-525252525252", "beta"}}
	for index, value := range clients {
		address, _ := domain.ParseAddress(fmt.Sprintf("10.42.0.%d", index+10))
		state := ClientState{Client: domain.Client{ID: value.id, Name: value.name, Status: domain.ClientActive}, CreatedAt: now, Assignment: &AddressAssignment{ID: fmt.Sprintf("%08d-0000-4000-8000-000000000001", index+1), NetworkID: instance.NetworkID, Kind: "static", Address: &address, Status: AssignmentActive, CreatedAt: now, UpdatedAt: now}}
		if err := store.CreateClient(context.Background(), instance.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	operationID := prepareConfigurationOperation(t, store, instance.ID, now)
	snapshot := appliedSnapshot(t, 2, strings.Replace(storedConfigYAML, "10.42.0.0/24", "10.43.0.0/24", 1))
	duplicate, _ := domain.ParseAddress("10.43.0.10")
	changes := make([]ConfigurationAddressChange, 0, 2)
	for index, value := range clients {
		changes = append(changes, ConfigurationAddressChange{ClientID: value.id, Name: value.name, ClientStatus: domain.ClientActive, Assignment: AddressAssignment{ID: fmt.Sprintf("%08d-0000-4000-8000-000000000002", index+3), Kind: "static", Address: &duplicate, CreatedAt: now, UpdatedAt: now}})
	}
	err := store.CommitConfigurationOperation(context.Background(), ConfigurationCommit{OperationID: operationID, InstanceID: instance.ID, ExpectedRevision: 1, ExpectedDigest: instance.Applied.Digest, Snapshot: snapshot, NewNetworkID: "53535353-5353-4535-8535-535353535353", AddressChanges: changes, RecoveryPayload: json.RawMessage(`{"version":1}`), UpdatedAt: now.Add(time.Minute)})
	if err == nil {
		t.Fatal("duplicate remap was committed")
	}
	loaded, loadErr := store.LoadInstance(context.Background(), instance.ID)
	if loadErr != nil || loaded.Applied.Revision != 1 || loaded.NetworkID != instance.NetworkID {
		t.Fatalf("failed transaction changed instance=%+v err=%v", loaded, loadErr)
	}
	var networks int
	if err := store.db.QueryRow("SELECT count(*) FROM networks WHERE instance_id = ?", instance.ID).Scan(&networks); err != nil || networks != 1 {
		t.Fatalf("network rollback count=%d err=%v", networks, err)
	}
}

func prepareConfigurationOperation(t *testing.T, store *Store, instanceID string, now time.Time) string {
	t.Helper()
	id, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"version":1}`)
	if err := store.PrepareOperation(context.Background(), Operation{ID: id, InstanceID: instanceID, Kind: "config.apply", State: OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceOperation(context.Background(), id, OperationFilesInstalled, payload, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return id
}
