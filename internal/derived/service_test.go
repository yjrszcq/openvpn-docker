package derived

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	testInstanceID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	staticClientID  = "11111111-1111-4111-8111-111111111111"
	dynamicClientID = "22222222-2222-4222-8222-222222222222"
	revokedClientID = "33333333-3333-4333-8333-333333333333"
)

type serviceFixture struct {
	service   *Service
	state     *storesqlite.Store
	artifacts *artifact.LocalStore
	dataDir   string
	instance  storesqlite.InstanceState
	now       time.Time
	ca        *x509.Certificate
	caKey     ed25519.PrivateKey
	renderer  render.Renderer
	paths     render.Paths
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	dataDir := filepath.Join(t.TempDir(), "openvpn")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	ca, caKey := makeCA(t, now)
	writeCertificate(t, filepath.Join(dataDir, "pki", "ca.crt"), ca)
	writeTLSCrypt(t, filepath.Join(dataDir, "secrets", "tls-crypt.key"))
	state, err := storesqlite.Create(ctx, filepath.Join(dataDir, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state store: %v", err)
		}
	})
	config, err := configservice.Parse([]byte("version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.42.0.0/24, dynamicPoolSize: 100}\n"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(1, config)
	if err != nil {
		t.Fatal(err)
	}
	instance := storesqlite.InstanceState{ID: testInstanceID, CreatedAt: now, CAFingerprint: sha256.Sum256(ca.Raw), Applied: snapshot}
	if err := state.CreateInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	instance, err = state.LoadInstance(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"), contract)
	if err != nil {
		t.Fatal(err)
	}
	paths := render.Paths{DataDir: dataDir, RuntimeDir: filepath.Join(t.TempDir(), "run")}
	service, err := NewService(state, local, renderer, paths)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return serviceFixture{service: service, state: state, artifacts: local, dataDir: dataDir, instance: instance, now: now, ca: ca, caKey: caKey, renderer: renderer, paths: paths}
}

func TestRefreshServerIsTransactionalAndRepeatable(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	first, err := fixture.service.RefreshServer(ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.RefreshServer(ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == second.OperationID || len(second.Written) != 1 || second.Written[0] != "server/server.conf" {
		t.Fatalf("unexpected refresh results: first=%+v second=%+v", first, second)
	}
	content, reference, err := fixture.artifacts.Read(ctx, "server/server.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "server 10.42.0.0 255.255.255.0") || reference.Mode != 0o600 {
		t.Fatalf("unexpected server artifact mode=%04o\n%s", reference.Mode, content)
	}
	metadata, err := fixture.state.LoadInstanceArtifacts(ctx, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].Key != "server/server.conf" || metadata[0].Digest != reference.Digest || metadata[0].Status != storesqlite.ArtifactActive {
		t.Fatalf("unexpected server metadata: %+v", metadata)
	}
	operation, err := fixture.state.LoadOperation(ctx, second.OperationID)
	if err != nil || operation.State != storesqlite.OperationCommitted {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
	pending, err := fixture.artifacts.PendingOperations()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending artifact operations=%v err=%v", pending, err)
	}
	assertAuditTypes(t, fixture.state, fixture.instance.ID, "artifacts.refreshed", "operation.committed")
}

func TestRefreshClientGeneratesProfilesAndReconcilesCCD(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	staticAddress := parseAddress(t, "10.42.0.10")
	static := clientState(fixture, staticClientID, "laptop", domain.ClientActive, "static", &staticAddress)
	createClient(t, fixture, static)
	installText(t, fixture.artifacts, "clients/revoked/laptop.ovpn", "obsolete")
	result, err := fixture.service.RefreshClient(ctx, fixture.instance.ID, staticClientID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Written) != 2 || len(result.Deleted) != 1 {
		t.Fatalf("static refresh result=%+v", result)
	}
	profile := readText(t, fixture.artifacts, "clients/active/laptop.ovpn")
	if !strings.Contains(profile, "# ovpn-client-id: "+staticClientID) || !strings.Contains(profile, "<key>") {
		t.Fatalf("unexpected static profile:\n%s", profile)
	}
	if got := readText(t, fixture.artifacts, "ccd/"+staticClientID); got != "ifconfig-push 10.42.0.10 255.255.255.0\n" {
		t.Fatalf("unexpected CCD: %q", got)
	}
	assertMissing(t, filepath.Join(fixture.dataDir, "clients", "revoked", "laptop.ovpn"))
	staticLoaded, err := fixture.state.LoadClient(ctx, fixture.instance.ID, staticClientID)
	if err != nil {
		t.Fatal(err)
	}
	assertActiveArtifactKinds(t, staticLoaded.Artifacts, "ccd", "client-cert", "client-key", "profile")

	dynamic := clientState(fixture, dynamicClientID, "phone", domain.ClientActive, "dynamic", nil)
	createClient(t, fixture, dynamic)
	installText(t, fixture.artifacts, "ccd/"+dynamicClientID, "obsolete")
	if _, err := fixture.service.RefreshClient(ctx, fixture.instance.ID, dynamicClientID); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(fixture.dataDir, "ccd", dynamicClientID))
	dynamicLoaded, err := fixture.state.LoadClient(ctx, fixture.instance.ID, dynamicClientID)
	if err != nil {
		t.Fatal(err)
	}
	assertActiveArtifactKinds(t, dynamicLoaded.Artifacts, "client-cert", "client-key", "profile")

	revoked := clientState(fixture, revokedClientID, "tablet", domain.ClientRevoked, "dynamic", nil)
	createClient(t, fixture, revoked)
	installText(t, fixture.artifacts, "clients/active/tablet.ovpn", "obsolete")
	if _, err := fixture.service.RefreshClient(ctx, fixture.instance.ID, revokedClientID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readText(t, fixture.artifacts, "clients/revoked/tablet.ovpn"), "# ovpn-client-id: "+revokedClientID) {
		t.Fatal("revoked profile does not contain its stable client ID")
	}
	assertMissing(t, filepath.Join(fixture.dataDir, "clients", "active", "tablet.ovpn"))
	assertMissing(t, filepath.Join(fixture.dataDir, "ccd", revokedClientID))
}

func TestRefreshClientRollsBackFilesWhenMetadataCommitFails(t *testing.T) {
	fixture := newServiceFixture(t)
	client := clientState(fixture, staticClientID, "laptop", domain.ClientActive, "dynamic", nil)
	createClient(t, fixture, client)
	installText(t, fixture.artifacts, "clients/active/laptop.ovpn", "previous-profile")
	failure := errors.New("injected metadata failure")
	service, err := NewService(commitFailStore{StateStore: fixture.state, failure: failure}, fixture.artifacts, fixture.renderer, fixture.paths)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return fixture.now }
	if _, err := service.RefreshClient(context.Background(), fixture.instance.ID, staticClientID); !errors.Is(err, failure) {
		t.Fatalf("refresh error=%v", err)
	}
	if got := readText(t, fixture.artifacts, "clients/active/laptop.ovpn"); got != "previous-profile" {
		t.Fatalf("rollback profile=%q", got)
	}
	loaded, err := fixture.state.LoadClient(context.Background(), fixture.instance.ID, staticClientID)
	if err != nil || len(loaded.Artifacts) != 0 {
		t.Fatalf("metadata survived failed commit: %+v err=%v", loaded.Artifacts, err)
	}
	pending, err := fixture.state.PendingOperations(context.Background(), fixture.instance.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending DB operations=%+v err=%v", pending, err)
	}
}

func TestRefreshClientRejectsCAStateMismatchBeforeWriting(t *testing.T) {
	fixture := newServiceFixture(t)
	client := clientState(fixture, staticClientID, "laptop", domain.ClientActive, "dynamic", nil)
	createClient(t, fixture, client)
	otherCA, _ := makeCA(t, fixture.now)
	writeCertificate(t, filepath.Join(fixture.dataDir, "pki", "ca.crt"), otherCA)
	if _, err := fixture.service.RefreshClient(context.Background(), fixture.instance.ID, staticClientID); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("CA mismatch error=%v", err)
	}
	assertMissing(t, filepath.Join(fixture.dataDir, "clients", "active", "laptop.ovpn"))
}

type commitFailStore struct {
	StateStore
	failure error
}

func (store commitFailStore) CommitArtifactOperation(context.Context, string, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.failure
}

func clientState(fixture serviceFixture, id, name string, status domain.ClientStatus, kind string, address *domain.Address) storesqlite.ClientState {
	state := storesqlite.ClientState{
		Client: domain.Client{ID: id, Name: name, Status: status}, CreatedAt: fixture.now,
		Assignment: &storesqlite.AddressAssignment{ID: "dddddddd-dddd-4ddd-8ddd-" + id[len(id)-12:], NetworkID: fixture.instance.NetworkID, Kind: kind, Address: address, Status: storesqlite.AssignmentActive, CreatedAt: fixture.now, UpdatedAt: fixture.now},
	}
	if status == domain.ClientRevoked {
		state.RevokedAt = &fixture.now
		state.Assignment.Status = storesqlite.AssignmentRetained
	}
	return state
}

func createClient(t *testing.T, fixture serviceFixture, state storesqlite.ClientState) {
	t.Helper()
	writeClientIdentity(t, fixture, state.Client.ID)
	if err := fixture.state.CreateClient(context.Background(), fixture.instance.ID, state); err != nil {
		t.Fatal(err)
	}
}

func makeCA(t *testing.T, now time.Time) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenVPN Container CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, private
}

func writeClientIdentity(t *testing.T, fixture serviceFixture, id string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: new(big.Int).SetBytes([]byte(id[:8])), Subject: pkix.Name{CommonName: id}, NotBefore: fixture.now.Add(-time.Hour), NotAfter: fixture.now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, fixture.ca, public, fixture.caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	writeCertificate(t, filepath.Join(fixture.dataDir, "pki", "issued", id+".crt"), certificate)
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fixture.dataDir, "pki", "private", id+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600)
}

func writeCertificate(t *testing.T, path string, certificate *x509.Certificate) {
	t.Helper()
	writeFile(t, path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o644)
}

func writeTLSCrypt(t *testing.T, path string) {
	t.Helper()
	content := "-----BEGIN OpenVPN Static key V1-----\n" + hex.EncodeToString(bytesOf(0x5a, 256)) + "\n-----END OpenVPN Static key V1-----\n"
	writeFile(t, path, []byte(content), 0o600)
}

func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func installText(t *testing.T, store *artifact.LocalStore, key, content string) {
	t.Helper()
	id, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.BeginOperation(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.Stage(context.Background(), key, 0o600, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := operation.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := operation.Commit(nil); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, store *artifact.LocalStore, key string) string {
	t.Helper()
	content, _, err := store.Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func parseAddress(t *testing.T, value string) domain.Address {
	t.Helper()
	address, err := domain.ParseAddress(value)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, error=%v", path, err)
	}
}

func assertActiveArtifactKinds(t *testing.T, artifacts []storesqlite.ArtifactMetadata, want ...string) {
	t.Helper()
	got := make([]string, 0, len(artifacts))
	for _, value := range artifacts {
		if value.Status == storesqlite.ArtifactActive {
			got = append(got, value.Kind)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("active artifact kinds=%v, want %v; all=%+v", got, want, artifacts)
	}
}

func assertAuditTypes(t *testing.T, store *storesqlite.Store, instanceID string, want ...string) {
	t.Helper()
	events, err := store.AuditEvents(context.Background(), instanceID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, event := range events {
		seen[event.Type] = true
	}
	for _, eventType := range want {
		if !seen[eventType] {
			t.Errorf("audit event %q is missing from %+v", eventType, events)
		}
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
