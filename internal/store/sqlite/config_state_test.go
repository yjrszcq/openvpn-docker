package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const storedConfigYAML = `version: 1
server:
  endpoint: vpn.example.test
  transport: {protocol: tcp, family: ipv4, port: 443}
  clientToClient: false
ipv4:
  network: 10.42.0.0/24
  dynamicPoolSize: 100
  nat: {enabled: true, interface: eth0}
  redirectGateway: true
  dns: [1.1.1.1, 8.8.8.8]
  routes: [192.168.0.0/16, 172.16.0.0/12]
logging: {maxBytes: 4096, backups: 2}
`

func appliedSnapshot(t *testing.T, revision configservice.Revision, yaml string) configservice.AppliedSnapshot {
	t.Helper()
	value, err := configservice.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(revision, value)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func initialInstance(t *testing.T) InstanceState {
	t.Helper()
	state := InstanceState{
		ID:        "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		CreatedAt: time.Date(2026, 7, 20, 12, 34, 56, 0, time.UTC),
		Applied:   appliedSnapshot(t, 1, storedConfigYAML),
	}
	for index := range state.CAFingerprint {
		state.CAFingerprint[index] = byte(index)
	}
	return state
}

func TestConfigurationStateRoundTrip(t *testing.T) {
	store := createStore(t, databasePath(t))
	want := initialInstance(t)
	if err := store.CreateInstance(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadInstance(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := configservice.EqualCanonical(want.Applied.Config, got.Applied.Config)
	if err != nil || !equal || got.Applied.Revision != 1 || got.Applied.Digest != want.Applied.Digest {
		t.Fatalf("applied round trip mismatch: equal=%t err=%v got=%+v", equal, err, got.Applied)
	}
	if got.ID != want.ID || got.CreatedAt != want.CreatedAt || got.CAFingerprint != want.CAFingerprint || !domain.ValidUUID(got.NetworkID) {
		t.Fatalf("instance round trip mismatch: %+v", got)
	}
	for _, table := range []string{"networks", "address_pools", "pushed_routes", "dns_servers"} {
		var count int
		if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Errorf("%s was not persisted", table)
		}
	}
	for _, query := range []string{
		"SELECT length(network) FROM networks",
		"SELECT length(first_address) FROM address_pools LIMIT 1",
		"SELECT length(network) FROM pushed_routes LIMIT 1",
		"SELECT length(address) FROM dns_servers LIMIT 1",
	} {
		var length int
		if err := store.db.QueryRow(query).Scan(&length); err != nil || length != 4 {
			t.Errorf("packed address query %q length=%d err=%v", query, length, err)
		}
	}
}

func TestApplyConfigurationAdvancesAtomically(t *testing.T) {
	store := createStore(t, databasePath(t))
	state := initialInstance(t)
	if err := store.CreateInstance(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	updatedYAML := strings.Replace(storedConfigYAML, "vpn.example.test", "new.example.test", 1)
	updatedYAML = strings.Replace(updatedYAML, "10.42.0.0/24", "10.43.0.0/24", 1)
	updated := appliedSnapshot(t, 2, updatedYAML)
	if err := store.ApplyConfig(context.Background(), state.ID, updated); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadInstance(context.Background(), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Applied.Revision != 2 || loaded.Applied.Config.Endpoint != "new.example.test" || loaded.Applied.Config.IPv4.Network.String() != "10.43.0.0/24" {
		t.Fatalf("unexpected applied state: %+v", loaded.Applied)
	}
	var history int
	if err := store.db.QueryRow("SELECT count(*) FROM applied_config WHERE instance_id = ?", state.ID).Scan(&history); err != nil || history != 2 {
		t.Fatalf("history=%d err=%v", history, err)
	}
	if err := store.ApplyConfig(context.Background(), state.ID, appliedSnapshot(t, 4, updatedYAML)); err == nil {
		t.Fatal("revision gap was accepted")
	}
	afterFailure, err := store.LoadInstance(context.Background(), state.ID)
	if err != nil || afterFailure.Applied.Revision != 2 {
		t.Fatalf("failed apply changed current revision: %+v err=%v", afterFailure, err)
	}
}

func TestConfigurationConstraintsAndIdentity(t *testing.T) {
	store := createStore(t, databasePath(t))
	state := initialInstance(t)
	if err := store.CreateInstance(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(context.Background(), state); !errors.Is(err, ErrConstraint) {
		t.Fatalf("duplicate instance error=%v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO networks(id, instance_id, family, network, prefix, purpose, enabled) VALUES(?, ?, 6, ?, 24, 'tunnel', 1)`, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", state.ID, []byte{10, 0, 0, 0}); err == nil {
		t.Fatal("family 6 network was accepted")
	}
	invalid := state
	invalid.ID = "not-a-uuid"
	if err := store.CreateInstance(context.Background(), invalid); err == nil {
		t.Fatal("invalid instance UUID was accepted")
	}
}
