package client

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
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type mutationFixture struct {
	manager   *Manager
	store     *storesqlite.Store
	artifacts *artifact.LocalStore
	root      string
	instance  storesqlite.InstanceState
	executor  *issueExecutor
	now       time.Time
}

type issueExecutor struct {
	ca      *x509.Certificate
	caKey   ed25519.PrivateKey
	now     time.Time
	failure error
	calls   int
}

func (executor *issueExecutor) Run(_ context.Context, invocation pki.Invocation) error {
	executor.calls++
	if executor.failure != nil {
		return executor.failure
	}
	if len(invocation.Args) < 2 || invocation.Args[0] != "build-client-full" {
		return fmt.Errorf("unexpected invocation: %+v", invocation)
	}
	clientID := invocation.Args[1]
	pkiDir := envValue(invocation.Env, "EASYRSA_PKI")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: new(big.Int).SetBytes([]byte(clientID[:8])), Subject: pkix.Name{CommonName: clientID}, NotBefore: executor.now.Add(-time.Hour), NotAfter: executor.now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, executor.ca, public, executor.caKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(pkiDir, "issued"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(pkiDir, "private"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pkiDir, "issued", clientID+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pkiDir, "private", clientID+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600)
}

func newMutationFixture(t *testing.T) mutationFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(root, "pki", "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	caPublic, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenVPN Container CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	writeMutationFile(t, filepath.Join(root, "pki", "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o644)
	encodedCA, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	writeMutationFile(t, filepath.Join(root, "pki", "private", "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedCA}), 0o600)
	tls := "-----BEGIN OpenVPN Static key V1-----\n" + hex.EncodeToString(repeatByte(0x6a, 256)) + "\n-----END OpenVPN Static key V1-----\n"
	writeMutationFile(t, filepath.Join(root, "secrets", "tls-crypt.key"), []byte(tls), 0o600)
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
	instance := storesqlite.InstanceState{ID: queryInstanceID, CreatedAt: now, CAFingerprint: sha256.Sum256(ca.Raw), Applied: snapshot}
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
	executor := &issueExecutor{ca: ca, caKey: caKey, now: now}
	runner, err := pki.NewRunner(pki.Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
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
	paths := render.Paths{DataDir: root, RuntimeDir: filepath.Join(t.TempDir(), "run")}
	manager, err := NewManager(store, local, runner, renderer, paths)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now }
	return mutationFixture{manager: manager, store: store, artifacts: local, root: root, instance: instance, executor: executor, now: now}
}

func TestCreateAndRenameClientTransaction(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if created.OperationID == "" || created.Client.Name != "laptop" || created.Client.Status != "active" || created.Client.IPv4.Mode != "static" || created.Client.IPv4.Address == nil || *created.Client.IPv4.Address != "10.42.0.2" {
		t.Fatalf("created result=%+v", created)
	}
	profile := readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
	if !strings.Contains(profile, "# ovpn-client-id: "+created.Client.ID) || !strings.Contains(profile, "# ovpn-client-name: laptop") {
		t.Fatalf("created profile:\n%s", profile)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "ccd/"+created.Client.ID); got != "ifconfig-push 10.42.0.2 255.255.255.0\n" {
		t.Fatalf("created CCD=%q", got)
	}
	certificateBefore := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt")
	operation, err := fixture.store.LoadOperation(context.Background(), created.OperationID)
	if err != nil || operation.State != storesqlite.OperationCommitted || bytesContainSecret(operation.RecoveryPayload) {
		t.Fatalf("create operation=%+v err=%v", operation, err)
	}

	renamed, err := fixture.manager.Rename(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)}, "office-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Client.ID != created.Client.ID || renamed.Client.Name != "office-laptop" || renamed.Client.IPv4.Address == nil || *renamed.Client.IPv4.Address != "10.42.0.2" {
		t.Fatalf("renamed result=%+v", renamed)
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "clients", "active", "laptop.ovpn"))
	renamedProfile := readMutationArtifact(t, fixture.artifacts, "clients/active/office-laptop.ovpn")
	if !strings.Contains(renamedProfile, "# ovpn-client-name: office-laptop") || strings.Contains(renamedProfile, "# ovpn-client-name: laptop\n") {
		t.Fatalf("renamed profile:\n%s", renamedProfile)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt"); got != certificateBefore {
		t.Fatal("rename changed client certificate")
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Client.Name != "office-laptop" || loaded.Assignment == nil || loaded.Assignment.Address.String() != "10.42.0.2" {
		t.Fatalf("renamed state=%+v err=%v", loaded, err)
	}
	pending, err := fixture.artifacts.PendingOperations()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending file operations=%v err=%v", pending, err)
	}
	workspaces, _ := filepath.Glob(filepath.Join(fixture.root, ".client-work-*"))
	if len(workspaces) != 0 {
		t.Fatalf("client workspaces remain: %v", workspaces)
	}
}

func TestCreateDynamicAndRejectDuplicateStaticAddress(t *testing.T) {
	fixture := newMutationFixture(t)
	dynamic, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "phone", IPv4: "dynamic"})
	if err != nil || dynamic.Client.IPv4.Mode != "dynamic" || dynamic.Client.IPv4.Address != nil {
		t.Fatalf("dynamic create=%+v err=%v", dynamic, err)
	}
	first, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "first", IPv4: "10.42.0.10"})
	if err != nil || first.Client.IPv4.Address == nil {
		t.Fatalf("static create=%+v err=%v", first, err)
	}
	if _, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "duplicate", IPv4: "10.42.0.10"}); err == nil || !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("duplicate static error=%v", err)
	}
	clients, err := fixture.store.ListClients(context.Background(), fixture.instance.ID)
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients after rejected create=%d err=%v", len(clients), err)
	}
}

func TestCreateFailureRollsBackJournalWorkspaceAndFiles(t *testing.T) {
	fixture := newMutationFixture(t)
	fixture.executor.failure = fmt.Errorf("%w: injected issuance", pki.ErrCommand)
	if _, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "failed", IPv4: "dynamic"}); !errors.Is(err, pki.ErrCommand) {
		t.Fatalf("create failure=%v", err)
	}
	clients, err := fixture.store.ListClients(context.Background(), fixture.instance.ID)
	if err != nil || len(clients) != 0 {
		t.Fatalf("failed create clients=%+v err=%v", clients, err)
	}
	issued, err := os.ReadDir(filepath.Join(fixture.root, "pki", "issued"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(issued) != 0 {
		t.Fatalf("failed create installed certificates: %v", issued)
	}
	pending, err := fixture.store.PendingOperations(context.Background(), fixture.instance.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed create pending DB operations=%+v err=%v", pending, err)
	}
	filePending, err := fixture.artifacts.PendingOperations()
	if err != nil || len(filePending) != 0 {
		t.Fatalf("failed create pending file operations=%+v err=%v", filePending, err)
	}
	workspaces, _ := filepath.Glob(filepath.Join(fixture.root, ".client-work-*"))
	if len(workspaces) != 0 {
		t.Fatalf("failed create workspaces=%v", workspaces)
	}
}

func TestDatabaseCommitFailuresRestoreCreateAndRenameFiles(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fixture := newMutationFixture(t)
		injected := errors.New("injected create commit")
		manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, createErr: injected})
		if _, err := manager.Create(context.Background(), CreateRequest{Name: "failed", IPv4: "dynamic"}); !errors.Is(err, injected) {
			t.Fatalf("create commit error=%v", err)
		}
		clients, err := fixture.store.ListClients(context.Background(), fixture.instance.ID)
		if err != nil || len(clients) != 0 {
			t.Fatalf("failed create state=%+v err=%v", clients, err)
		}
		issued, err := os.ReadDir(filepath.Join(fixture.root, "pki", "issued"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if len(issued) != 0 {
			t.Fatalf("failed create files=%v", issued)
		}
	})
	t.Run("rename", func(t *testing.T) {
		fixture := newMutationFixture(t)
		created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "dynamic"})
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected rename commit")
		manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, renameErr: injected})
		if _, err := manager.Rename(context.Background(), Selector{Name: "laptop"}, "new-name"); !errors.Is(err, injected) {
			t.Fatalf("rename commit error=%v", err)
		}
		loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
		if err != nil || loaded.Client.Name != "laptop" {
			t.Fatalf("failed rename state=%+v err=%v", loaded.Client, err)
		}
		_ = readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
		assertMutationMissing(t, filepath.Join(fixture.root, "clients", "active", "new-name.ovpn"))
	})
}

type commitFailureStore struct {
	MutationStore
	createErr error
	renameErr error
}

func (store commitFailureStore) CommitCreateClientOperation(context.Context, string, string, storesqlite.ClientState, json.RawMessage, time.Time) error {
	return store.createErr
}

func (store commitFailureStore) CommitRenameClientOperation(context.Context, string, string, string, string, string, storesqlite.ArtifactMetadata, storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.renameErr
}

func managerWithStore(t *testing.T, fixture mutationFixture, state MutationStore) *Manager {
	t.Helper()
	manager, err := NewManager(state, fixture.artifacts, fixture.manager.pki, fixture.manager.renderer, fixture.manager.paths)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return fixture.now }
	return manager
}

func writeMutationFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func readMutationArtifact(t *testing.T, store *artifact.LocalStore, key string) string {
	t.Helper()
	content, _, err := store.Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertMutationMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s missing, error=%v", path, err)
	}
}

func envValue(values []string, key string) string {
	for _, value := range values {
		if strings.HasPrefix(value, key+"=") {
			return strings.TrimPrefix(value, key+"=")
		}
	}
	return ""
}

func repeatByte(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func bytesContainSecret(payload []byte) bool {
	value := string(payload)
	return strings.Contains(value, "PRIVATE KEY") || strings.Contains(value, "BEGIN CERTIFICATE") || strings.Contains(value, "OpenVPN Static key")
}
