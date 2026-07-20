package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sync"
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

func TestDynamicLeaseAddressIsUniqueWithinNetwork(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	newDynamic := func(id, name string) ClientState {
		return ClientState{
			Client:     domain.Client{ID: id, Name: name, Status: domain.ClientActive},
			CreatedAt:  now,
			Assignment: &AddressAssignment{ID: "dddddddd-dddd-4ddd-8ddd-" + id[len(id)-12:], NetworkID: instance.NetworkID, Kind: "dynamic", Status: AssignmentActive, CreatedAt: now, UpdatedAt: now},
			Lease:      &ClientLease{NetworkID: instance.NetworkID, Address: address(t, "10.42.0.200"), UpdatedAt: now},
		}
	}
	first := newDynamic("23232323-2323-4232-8232-232323232323", "first-lease")
	if err := store.CreateClient(context.Background(), instance.ID, first); err != nil {
		t.Fatal(err)
	}
	second := newDynamic("24242424-2424-4242-8242-242424242424", "second-lease")
	if err := store.CreateClient(context.Background(), instance.ID, second); !errors.Is(err, ErrConstraint) {
		t.Fatalf("duplicate dynamic lease error=%v", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM clients WHERE id = ?", second.Client.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate lease partially created client: count=%d err=%v", count, err)
	}
}

func TestClientRejectsCrossInstanceAssignment(t *testing.T) {
	store, first := storeWithInstance(t)
	secondState := initialInstance(t)
	secondState.ID = "25252525-2525-4252-8252-252525252525"
	if err := store.CreateInstance(context.Background(), secondState); err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadInstance(context.Background(), secondState.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := staticClient(t, second.NetworkID, "26262626-2626-4262-8262-262626262626", "cross-instance", "10.42.0.10")
	if err := store.CreateClient(context.Background(), first.ID, client); err == nil {
		t.Fatal("cross-instance assignment was accepted")
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM clients WHERE id = ?", client.Client.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-instance assignment partially created client: count=%d err=%v", count, err)
	}
}

func TestLoadClientRejectsLeaseWithoutDynamicAssignment(t *testing.T) {
	store, instance := storeWithInstance(t)
	client := staticClient(t, instance.NetworkID, "27272727-2727-4272-8272-272727272727", "static-with-lease", "10.42.0.10")
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
INSERT INTO client_leases(client_id, network_id, family, address, updated_at)
VALUES(?, ?, 4, ?, '2026-07-20T13:01:00Z')`, client.Client.ID, instance.NetworkID, []byte{10, 42, 0, 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadClient(context.Background(), instance.ID, client.Client.ID); !errors.Is(err, ErrSchema) {
		t.Fatalf("logical lease corruption error=%v", err)
	}
}

func TestMutationMapsCompetingWriterToBusy(t *testing.T) {
	store, instance := storeWithInstance(t)
	if _, err := store.db.Exec("PRAGMA busy_timeout = 1"); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"mode": []string{"rw"}, "_busy_timeout": []string{"1"}, "_txlock": []string{"immediate"}}
	dsn := (&url.URL{Scheme: "file", Path: store.Path(), RawQuery: query.Encode()}).String()
	other, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	transaction, err := other.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	client := staticClient(t, instance.NetworkID, "28282828-2828-4282-8282-282828282828", "busy", "10.42.0.11")
	if err := store.CreateClient(context.Background(), instance.ID, client); !errors.Is(err, ErrBusy) {
		t.Fatalf("competing writer error=%v", err)
	}
}

func TestConcurrentLeaseUpdatesAreSerialized(t *testing.T) {
	store, instance := storeWithInstance(t)
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	state := ClientState{
		Client:     domain.Client{ID: "29292929-2929-4292-8292-292929292929", Name: "concurrent-lease", Status: domain.ClientActive},
		CreatedAt:  now,
		Assignment: &AddressAssignment{ID: "30303030-3030-4303-8303-303030303030", NetworkID: instance.NetworkID, Kind: "dynamic", Status: AssignmentActive, CreatedAt: now, UpdatedAt: now},
	}
	if err := store.CreateClient(context.Background(), instance.ID, state); err != nil {
		t.Fatal(err)
	}
	addresses := []string{"10.42.0.200", "10.42.0.201", "10.42.0.202", "10.42.0.203", "10.42.0.204", "10.42.0.205", "10.42.0.206", "10.42.0.207"}
	errorsSeen := make(chan error, len(addresses))
	var wait sync.WaitGroup
	for index, raw := range addresses {
		lease := ClientLease{NetworkID: instance.NetworkID, Address: address(t, raw), UpdatedAt: now.Add(time.Duration(index+1) * time.Second)}
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- store.RecordLease(context.Background(), state.Client.ID, lease)
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent lease update: %v", err)
		}
	}
	loaded, err := store.LoadClient(context.Background(), instance.ID, state.Client.ID)
	if err != nil || loaded.Lease == nil {
		t.Fatalf("load concurrent lease: %+v err=%v", loaded, err)
	}
	found := false
	for _, raw := range addresses {
		found = found || loaded.Lease.Address.String() == raw
	}
	if !found {
		t.Fatalf("final lease %s was not one of the serialized updates", loaded.Lease.Address)
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

func TestInstanceArtifactRegistrationAndOwnerBoundaries(t *testing.T) {
	store, instance := storeWithInstance(t)
	caKey := ArtifactMetadata{
		ID: "35353535-3535-4353-8353-353535353535", OwnerKind: "instance", OwnerID: instance.ID,
		Kind: "ca-key", Key: "pki/private/ca.key", Status: ArtifactActive,
	}
	if err := store.RegisterInstanceArtifacts(context.Background(), instance.ID, []ArtifactMetadata{caKey}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadInstanceArtifacts(context.Background(), instance.ID)
	if err != nil || len(loaded) != 1 || loaded[0].Kind != "ca-key" || loaded[0].Key != caKey.Key {
		t.Fatalf("instance artifacts=%+v err=%v", loaded, err)
	}
	invalid := ArtifactMetadata{
		ID: "36363636-3636-4363-8363-363636363636", OwnerKind: "instance", OwnerID: instance.ID,
		Kind: "client-key", Key: "pki/private/client.key", Status: ArtifactActive,
	}
	if err := store.RegisterInstanceArtifacts(context.Background(), instance.ID, []ArtifactMetadata{invalid}); err == nil {
		t.Fatal("client-only artifact kind was accepted for an instance")
	}
	client := ClientState{
		Client:    domain.Client{ID: "37373737-3737-4373-8373-373737373737", Name: "invalid-owner-kind", Status: domain.ClientActive},
		CreatedAt: time.Now().UTC(),
		Artifacts: []ArtifactMetadata{{
			ID: "38383838-3838-4383-8383-383838383838", OwnerKind: "client", OwnerID: "37373737-3737-4373-8373-373737373737",
			Kind: "ca-key", Key: "pki/private/ca.key", Status: ArtifactActive,
		}},
	}
	if err := store.CreateClient(context.Background(), instance.ID, client); err == nil {
		t.Fatal("instance-only artifact kind was accepted for a client")
	}
}
