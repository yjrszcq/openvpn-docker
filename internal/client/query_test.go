package client

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const queryInstanceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type queryFixture struct {
	service   *Service
	store     *storesqlite.Store
	artifacts *artifact.LocalStore
	root      string
	instance  storesqlite.InstanceState
}

func newQueryFixture(t *testing.T) queryFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	store, err := storesqlite.Create(context.Background(), filepath.Join(root, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config, err := configservice.Parse([]byte("version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.42.0.0/24, dynamicPoolSize: 100}\n"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(1, config)
	if err != nil {
		t.Fatal(err)
	}
	instance := storesqlite.InstanceState{ID: queryInstanceID, CreatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Applied: snapshot}
	if err := store.CreateInstance(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	instance, err = store.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, local)
	if err != nil {
		t.Fatal(err)
	}
	return queryFixture{service: service, store: store, artifacts: local, root: root, instance: instance}
}

func TestListAndSelectCurrentClients(t *testing.T) {
	fixture := newQueryFixture(t)
	activeID := "11111111-1111-4111-8111-111111111111"
	revokedID := "11111111-2222-4222-8222-222222222222"
	deletedID := "33333333-3333-4333-8333-333333333333"
	address := mustAddress(t, "10.42.0.10")
	createQueryClient(t, fixture, storesqlite.ClientState{
		Client: domain.Client{ID: activeID, Name: "alpha", Status: domain.ClientActive}, CreatedAt: testTime(),
		Assignment: &storesqlite.AddressAssignment{ID: "41414141-4141-4414-8414-414141414141", NetworkID: fixture.instance.NetworkID, Kind: "static", Address: &address, Status: storesqlite.AssignmentActive, CreatedAt: testTime(), UpdatedAt: testTime()},
	})
	revokedAt := testTime()
	createQueryClient(t, fixture, storesqlite.ClientState{Client: domain.Client{ID: revokedID, Name: "beta", Status: domain.ClientRevoked}, CreatedAt: testTime(), RevokedAt: &revokedAt})
	deletedAt := testTime()
	createQueryClient(t, fixture, storesqlite.ClientState{Client: domain.Client{ID: deletedID, Name: "deleted", Status: domain.ClientDeleted}, CreatedAt: testTime(), DeletedAt: &deletedAt})

	result, err := fixture.service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || len(result.Clients) != 2 || result.Clients[0].Name != "alpha" || result.Clients[1].Name != "beta" {
		t.Fatalf("unexpected list: %+v", result)
	}
	if result.Clients[0].IPv4.Mode != "static" || result.Clients[0].IPv4.Address == nil || *result.Clients[0].IPv4.Address != "10.42.0.10" {
		t.Fatalf("unexpected address view: %+v", result.Clients[0].IPv4)
	}
	_, selected, err := fixture.service.Select(context.Background(), Selector{Name: "alpha"})
	if err != nil || selected.Client.ID != activeID {
		t.Fatalf("name selection=%+v err=%v", selected.Client, err)
	}
	_, selected, err = fixture.service.Select(context.Background(), Selector{IDPrefix: "111111112222"})
	if err != nil || selected.Client.ID != revokedID {
		t.Fatalf("ID selection=%+v err=%v", selected.Client, err)
	}
	if _, _, err := fixture.service.Select(context.Background(), Selector{IDPrefix: "11111111"}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous selection error=%v", err)
	}
	if _, _, err := fixture.service.Select(context.Background(), Selector{IDPrefix: "33333333"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted selection error=%v", err)
	}
	for _, selector := range []Selector{{}, {Name: "alpha", IDPrefix: "11111111"}, {IDPrefix: "1234"}, {IDPrefix: "zzzzzzzz"}, {IDPrefix: "1111-1111"}} {
		if _, _, err := fixture.service.Select(context.Background(), selector); err == nil {
			t.Fatalf("invalid selector was accepted: %+v", selector)
		}
	}
}

func TestExportVerifiesActiveProfile(t *testing.T) {
	fixture := newQueryFixture(t)
	activeID := "51515151-5151-4515-8515-515151515151"
	profile := []byte("client\n# private profile\n")
	key := "clients/active/laptop.ovpn"
	installProfile(t, fixture.artifacts, key, profile)
	createQueryClient(t, fixture, storesqlite.ClientState{
		Client: domain.Client{ID: activeID, Name: "laptop", Status: domain.ClientActive}, CreatedAt: testTime(),
		Artifacts: []storesqlite.ArtifactMetadata{{ID: "52525252-5252-4525-8525-525252525252", OwnerKind: "client", OwnerID: activeID, Kind: "profile", Key: key, Digest: sha256.Sum256(profile), Status: storesqlite.ArtifactActive}},
	})
	content, view, err := fixture.service.Export(context.Background(), Selector{IDPrefix: strings.ToUpper("51515151")})
	if err != nil || string(content) != string(profile) || view.Name != "laptop" {
		t.Fatalf("export view=%+v content=%q err=%v", view, content, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(key)), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.service.Export(context.Background(), Selector{Name: "laptop"}); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("tampered export error=%v", err)
	}
}

func TestExportRejectsRevokedAndMissingProfiles(t *testing.T) {
	fixture := newQueryFixture(t)
	revokedAt := testTime()
	createQueryClient(t, fixture, storesqlite.ClientState{Client: domain.Client{ID: "61616161-6161-4616-8616-616161616161", Name: "revoked", Status: domain.ClientRevoked}, CreatedAt: testTime(), RevokedAt: &revokedAt})
	createQueryClient(t, fixture, storesqlite.ClientState{Client: domain.Client{ID: "62626262-6262-4626-8626-626262626262", Name: "missing", Status: domain.ClientActive}, CreatedAt: testTime()})
	if _, _, err := fixture.service.Export(context.Background(), Selector{Name: "revoked"}); !errors.Is(err, ErrInactive) {
		t.Fatalf("revoked export error=%v", err)
	}
	if _, _, err := fixture.service.Export(context.Background(), Selector{Name: "missing"}); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("missing export error=%v", err)
	}
}

func createQueryClient(t *testing.T, fixture queryFixture, state storesqlite.ClientState) {
	t.Helper()
	if err := fixture.store.CreateClient(context.Background(), fixture.instance.ID, state); err != nil {
		t.Fatal(err)
	}
}

func installProfile(t *testing.T, store *artifact.LocalStore, key string, content []byte) {
	t.Helper()
	operation, err := store.BeginOperation("71717171-7171-4717-8717-717171717171")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), key, 0o600, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := operation.Commit(nil); err != nil {
		t.Fatal(err)
	}
}

func mustAddress(t *testing.T, value string) domain.Address {
	t.Helper()
	address, err := domain.ParseAddress(value)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func testTime() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }

func TestShortID(t *testing.T) {
	if got := ShortID("11111111-2222-4222-8222-222222222222"); got != "111111112222" {
		t.Fatalf("short ID=%q", got)
	}
}
