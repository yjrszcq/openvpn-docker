package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func storeWithInstance(t *testing.T) (*Store, InstanceState) {
	t.Helper()
	store := createStore(t, databasePath(t))
	instance := initialInstance(t)
	if err := store.CreateInstance(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, loaded
}

func address(t *testing.T, value string) domain.Address {
	t.Helper()
	parsed, err := domain.ParseAddress(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func staticClient(t *testing.T, networkID, id, name, ip string) ClientState {
	t.Helper()
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	assigned := address(t, ip)
	return ClientState{
		Client:    domain.Client{ID: id, Name: name, Status: domain.ClientActive},
		CreatedAt: now,
		Assignment: &AddressAssignment{
			ID: "dddddddd-dddd-4ddd-8ddd-" + id[len(id)-12:], NetworkID: networkID,
			Kind: "static", Address: &assigned, Status: AssignmentActive,
			CreatedAt: now, UpdatedAt: now,
		},
	}
}

func TestClientAggregateRoundTrip(t *testing.T) {
	store, instance := storeWithInstance(t)
	state := staticClient(t, instance.NetworkID, "11111111-1111-4111-8111-111111111111", "laptop", "10.42.0.10")
	state.Artifacts = []ArtifactMetadata{{
		ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", OwnerKind: "client", OwnerID: state.Client.ID,
		Kind: "profile", Key: "profiles/11111111.ovpn", Status: ArtifactActive,
	}}
	if err := store.CreateClient(context.Background(), instance.ID, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadClient(context.Background(), instance.ID, state.Client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Client != state.Client || loaded.Assignment == nil || loaded.Assignment.Address.String() != "10.42.0.10" || len(loaded.Artifacts) != 1 || loaded.Artifacts[0].Key != "profiles/11111111.ovpn" {
		t.Fatalf("unexpected client round trip: %+v", loaded)
	}
}

func TestDynamicLeaseRoundTripAndUpdate(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	state := ClientState{
		Client:     domain.Client{ID: "22222222-2222-4222-8222-222222222222", Name: "phone", Status: domain.ClientActive},
		CreatedAt:  now,
		Assignment: &AddressAssignment{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", NetworkID: instance.NetworkID, Kind: "dynamic", Status: AssignmentActive, CreatedAt: now, UpdatedAt: now},
		Lease:      &ClientLease{NetworkID: instance.NetworkID, Address: address(t, "10.42.0.200"), UpdatedAt: now},
	}
	if err := store.CreateClient(context.Background(), instance.ID, state); err != nil {
		t.Fatal(err)
	}
	updated := ClientLease{NetworkID: instance.NetworkID, Address: address(t, "10.42.0.201"), UpdatedAt: now.Add(time.Minute)}
	if err := store.RecordLease(context.Background(), state.Client.ID, updated); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadClient(context.Background(), instance.ID, state.Client.ID)
	if err != nil || loaded.Lease == nil || loaded.Lease.Address.String() != "10.42.0.201" || loaded.Assignment.Address != nil {
		t.Fatalf("unexpected dynamic client: %+v err=%v", loaded, err)
	}
}

func TestClientConstraintsAndNameReuse(t *testing.T) {
	store, instance := storeWithInstance(t)
	first := staticClient(t, instance.NetworkID, "33333333-3333-4333-8333-333333333333", "shared", "10.42.0.20")
	if err := store.CreateClient(context.Background(), instance.ID, first); err != nil {
		t.Fatal(err)
	}
	duplicateName := staticClient(t, instance.NetworkID, "44444444-4444-4444-8444-444444444444", "shared", "10.42.0.21")
	if err := store.CreateClient(context.Background(), instance.ID, duplicateName); !errors.Is(err, ErrConstraint) {
		t.Fatalf("duplicate current name error=%v", err)
	}
	duplicateAddress := staticClient(t, instance.NetworkID, "55555555-5555-4555-8555-555555555555", "other", "10.42.0.20")
	if err := store.CreateClient(context.Background(), instance.ID, duplicateAddress); !errors.Is(err, ErrConstraint) {
		t.Fatalf("duplicate current address error=%v", err)
	}
	deletedAt := first.CreatedAt.Add(time.Hour)
	deleted := ClientState{Client: domain.Client{ID: "66666666-6666-4666-8666-666666666666", Name: "reusable", Status: domain.ClientDeleted}, CreatedAt: first.CreatedAt, DeletedAt: &deletedAt}
	if err := store.CreateClient(context.Background(), instance.ID, deleted); err != nil {
		t.Fatal(err)
	}
	reused := ClientState{Client: domain.Client{ID: "77777777-7777-4777-8777-777777777777", Name: "reusable", Status: domain.ClientActive}, CreatedAt: first.CreatedAt}
	if err := store.CreateClient(context.Background(), instance.ID, reused); err != nil {
		t.Fatalf("deleted name was not reusable: %v", err)
	}
}

func TestClientRejectsPoolAndArtifactViolations(t *testing.T) {
	store, instance := storeWithInstance(t)
	outsideStatic := staticClient(t, instance.NetworkID, "88888888-8888-4888-8888-888888888888", "outside", "10.42.0.250")
	if err := store.CreateClient(context.Background(), instance.ID, outsideStatic); err == nil {
		t.Fatal("dynamic-pool address was accepted as static")
	}
	invalidArtifact := ClientState{Client: domain.Client{ID: "99999999-9999-4999-8999-999999999999", Name: "artifact", Status: domain.ClientActive}, CreatedAt: time.Now().UTC(), Artifacts: []ArtifactMetadata{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab", OwnerKind: "client", OwnerID: "99999999-9999-4999-8999-999999999999", Kind: "profile", Key: "../escape", Status: ArtifactActive}}}
	if err := store.CreateClient(context.Background(), instance.ID, invalidArtifact); err == nil {
		t.Fatal("escaping artifact key was accepted")
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM clients WHERE id = ?", invalidArtifact.Client.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed aggregate was partially committed: count=%d err=%v", count, err)
	}
}
