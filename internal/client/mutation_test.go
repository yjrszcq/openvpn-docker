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
	"github.com/yjrszcq/openvpn-docker/internal/domain"
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
	ca             *x509.Certificate
	caKey          ed25519.PrivateKey
	now            time.Time
	failure        error
	calls          int
	revokedSerials []*big.Int
}

func (executor *issueExecutor) Run(_ context.Context, invocation pki.Invocation) error {
	executor.calls++
	if executor.failure != nil {
		return executor.failure
	}
	pkiDir := envValue(invocation.Env, "EASYRSA_PKI")
	if len(invocation.Args) == 0 {
		return fmt.Errorf("unexpected invocation: %+v", invocation)
	}
	switch invocation.Args[0] {
	case "build-client-full":
		if len(invocation.Args) < 2 {
			return fmt.Errorf("missing client identity")
		}
		return executor.issue(pkiDir, invocation.Args[1])
	case "revoke":
		if len(invocation.Args) < 2 {
			return fmt.Errorf("missing revoke identity")
		}
		certificate, err := os.ReadFile(filepath.Join(pkiDir, "issued", invocation.Args[1]+".crt"))
		if err != nil {
			return err
		}
		block, _ := pem.Decode(certificate)
		if block == nil {
			return fmt.Errorf("invalid certificate")
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		executor.revokedSerials = append(executor.revokedSerials, new(big.Int).Set(parsed.SerialNumber))
		return nil
	case "gen-crl":
		entries := make([]x509.RevocationListEntry, 0, len(executor.revokedSerials))
		for _, serial := range executor.revokedSerials {
			entries = append(entries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: executor.now})
		}
		der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(int64(len(entries) + 1)), ThisUpdate: executor.now.Add(-time.Minute), NextUpdate: executor.now.Add(24 * time.Hour), RevokedCertificateEntries: entries}, executor.ca, executor.caKey)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(pkiDir, "crl.pem"), pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}), 0o644)
	default:
		return fmt.Errorf("unexpected invocation: %+v", invocation)
	}
}

func (executor *issueExecutor) issue(pkiDir, clientID string) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: new(big.Int).SetUint64(uint64(executor.calls + 1)), Subject: pkix.Name{CommonName: clientID}, NotBefore: executor.now.Add(-time.Hour), NotAfter: executor.now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
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
	renderer, err := render.New(filepath.Join("..", "..", "templates"), contract)
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
	if created.Version != 1 || created.OperationID == "" || !created.ProfileRedistributionRequired || created.Client.Name != "laptop" || created.Client.Status != "active" || created.Client.IPv4.Mode != "static" || created.Client.IPv4.Address == nil || *created.Client.IPv4.Address != "10.42.0.2" {
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
	if !renamed.ProfileRedistributionRequired || renamed.Client.ID != created.Client.ID || renamed.Client.Name != "office-laptop" || renamed.Client.IPv4.Address == nil || *renamed.Client.IPv4.Address != "10.42.0.2" {
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

func TestRevokeRetainsAssignmentAndReissueRestoresArtifacts(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "10.42.0.20"})
	if err != nil {
		t.Fatal(err)
	}
	certificateBefore := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt")
	revoked, err := fixture.manager.Revoke(context.Background(), Selector{Name: "laptop"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.KickRequired || revoked.ProfileRedistributionRequired || revoked.Client.Status != "revoked" || revoked.Client.IPv4.State != "retained" || revoked.Client.IPv4.Address == nil || *revoked.Client.IPv4.Address != "10.42.0.20" {
		t.Fatalf("revoked result=%+v", revoked)
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "clients", "active", "laptop.ovpn"))
	_ = readMutationArtifact(t, fixture.artifacts, "clients/revoked/laptop.ovpn")
	assertMutationMissing(t, filepath.Join(fixture.root, "ccd", created.Client.ID))
	if _, err := os.Stat(filepath.Join(fixture.root, "pki", "crl.pem")); err != nil {
		t.Fatal(err)
	}

	reissued, err := fixture.manager.Reissue(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reissued.KickRequired || !reissued.ProfileRedistributionRequired || reissued.Client.Status != "active" || reissued.Client.IPv4.State != "configured" || reissued.Client.IPv4.Address == nil || *reissued.Client.IPv4.Address != "10.42.0.20" {
		t.Fatalf("reissued result=%+v", reissued)
	}
	certificateAfter := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt")
	if certificateAfter == certificateBefore {
		t.Fatal("reissue did not replace the certificate")
	}
	_ = readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
	_ = readMutationArtifact(t, fixture.artifacts, "ccd/"+created.Client.ID)
	assertMutationMissing(t, filepath.Join(fixture.root, "clients", "revoked", "laptop.ovpn"))
	operation, err := fixture.store.LoadOperation(context.Background(), reissued.OperationID)
	if err != nil || operation.State != storesqlite.OperationCommitted || bytesContainSecret(operation.RecoveryPayload) {
		t.Fatalf("reissue operation=%+v err=%v", operation, err)
	}
}

func TestRevokeReleaseAndReissueDynamic(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "phone", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := fixture.manager.Revoke(context.Background(), Selector{Name: "phone"}, true)
	if err != nil || revoked.Client.IPv4.State != "unavailable" {
		t.Fatalf("released revoke=%+v err=%v", revoked, err)
	}
	reissued, err := fixture.manager.Reissue(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)}, "dynamic")
	if err != nil || reissued.Client.Status != "active" || reissued.Client.IPv4.Mode != "dynamic" || reissued.Client.IPv4.Address != nil {
		t.Fatalf("dynamic reissue=%+v err=%v", reissued, err)
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "ccd", created.Client.ID))
}

func TestDeleteActiveClientKeepsTombstoneAndAllowsNameReuse(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "tablet", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.manager.Delete(context.Background(), Selector{Name: "tablet"})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.KickRequired || deleted.Client.Status != "deleted" || deleted.Client.ID != created.Client.ID || deleted.Client.IPv4.State != "unavailable" {
		t.Fatalf("deleted result=%+v", deleted)
	}
	for _, path := range []string{
		filepath.Join(fixture.root, "pki", "issued", created.Client.ID+".crt"),
		filepath.Join(fixture.root, "pki", "private", created.Client.ID+".key"),
		filepath.Join(fixture.root, "clients", "active", "tablet.ovpn"),
		filepath.Join(fixture.root, "ccd", created.Client.ID),
	} {
		assertMutationMissing(t, path)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Client.Status != domain.ClientDeleted || loaded.DeletedAt == nil || loaded.RevokedAt == nil || loaded.Assignment != nil {
		t.Fatalf("tombstone=%+v err=%v", loaded, err)
	}
	listed, err := fixture.store.ListClients(context.Background(), fixture.instance.ID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("listed after delete=%+v err=%v", listed, err)
	}
	replacement, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "tablet", IPv4: "auto"})
	if err != nil || replacement.Client.ID == created.Client.ID {
		t.Fatalf("replacement=%+v err=%v", replacement, err)
	}
}

func TestDeleteRevokedClientStillRequiresRuntimeConvergence(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "stale-session", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Revoke(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)}, false); err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.manager.Delete(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.KickRequired || deleted.Client.Status != "deleted" {
		t.Fatalf("deleted revoked result=%+v", deleted)
	}
}

func TestLifecyclePKIFailureLeavesActiveStateAndFiles(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	profileBefore := readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
	fixture.executor.failure = fmt.Errorf("%w: injected revoke", pki.ErrCommand)
	if _, err := fixture.manager.Revoke(context.Background(), Selector{Name: "laptop"}, false); !errors.Is(err, pki.ErrCommand) {
		t.Fatalf("revoke failure=%v", err)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Client.Status != domain.ClientActive || loaded.Assignment == nil || loaded.Assignment.Status != storesqlite.AssignmentActive {
		t.Fatalf("state after failure=%+v err=%v", loaded, err)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn"); got != profileBefore {
		t.Fatal("failed revoke changed active profile")
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "clients", "revoked", "laptop.ovpn"))
	pending, err := fixture.artifacts.PendingOperations()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending lifecycle file operations=%v err=%v", pending, err)
	}
}

func TestLifecycleDatabaseCommitFailuresRestoreStateAndFiles(t *testing.T) {
	t.Run("revoke", func(t *testing.T) {
		fixture := newMutationFixture(t)
		created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected revoke commit")
		manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, revokeErr: injected})
		if _, err := manager.Revoke(context.Background(), Selector{Name: "laptop"}, false); !errors.Is(err, injected) {
			t.Fatalf("revoke commit error=%v", err)
		}
		loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
		if err != nil || loaded.Client.Status != domain.ClientActive {
			t.Fatalf("revoke rollback state=%+v err=%v", loaded, err)
		}
		_ = readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
		assertMutationMissing(t, filepath.Join(fixture.root, "clients", "revoked", "laptop.ovpn"))
	})
	t.Run("reissue", func(t *testing.T) {
		fixture := newMutationFixture(t)
		created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.manager.Revoke(context.Background(), Selector{Name: "laptop"}, false); err != nil {
			t.Fatal(err)
		}
		certificateBefore := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt")
		injected := errors.New("injected reissue commit")
		manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, reissueErr: injected})
		if _, err := manager.Reissue(context.Background(), Selector{Name: "laptop"}, ""); !errors.Is(err, injected) {
			t.Fatalf("reissue commit error=%v", err)
		}
		loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
		if err != nil || loaded.Client.Status != domain.ClientRevoked || loaded.Assignment == nil || loaded.Assignment.Status != storesqlite.AssignmentRetained {
			t.Fatalf("reissue rollback state=%+v err=%v", loaded, err)
		}
		if got := readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt"); got != certificateBefore {
			t.Fatal("reissue rollback did not restore certificate")
		}
		_ = readMutationArtifact(t, fixture.artifacts, "clients/revoked/laptop.ovpn")
	})
	t.Run("delete", func(t *testing.T) {
		fixture := newMutationFixture(t)
		created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("injected delete commit")
		manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, deleteErr: injected})
		if _, err := manager.Delete(context.Background(), Selector{Name: "laptop"}); !errors.Is(err, injected) {
			t.Fatalf("delete commit error=%v", err)
		}
		loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
		if err != nil || loaded.Client.Status != domain.ClientActive {
			t.Fatalf("delete rollback state=%+v err=%v", loaded, err)
		}
		_ = readMutationArtifact(t, fixture.artifacts, "pki/issued/"+created.Client.ID+".crt")
		_ = readMutationArtifact(t, fixture.artifacts, "clients/active/laptop.ovpn")
	})
}

func TestAddressSetSynchronizesCCDAndClearsLease(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "phone", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := fixture.manager.AddressSet(context.Background(), Selector{Name: "phone"}, "dynamic")
	if err != nil || dynamic.Version != 1 || dynamic.Clients[0].IPv4.Mode != "dynamic" || len(dynamic.KickRequired) != 1 {
		t.Fatalf("dynamic address result=%+v err=%v", dynamic, err)
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "ccd", created.Client.ID))
	if repeated, err := fixture.manager.AddressSet(context.Background(), Selector{Name: "phone"}, "dynamic"); err != nil || repeated.Clients[0].IPv4.Mode != "dynamic" {
		t.Fatalf("file-noop dynamic update=%+v err=%v", repeated, err)
	}
	leaseAddress, _ := domain.ParseAddress("10.42.0.200")
	if err := fixture.store.RecordLease(context.Background(), created.Client.ID, storesqlite.ClientLease{NetworkID: fixture.instance.NetworkID, Address: leaseAddress, UpdatedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	static, err := fixture.manager.AddressSet(context.Background(), Selector{IDPrefix: ShortID(created.Client.ID)}, "10.42.0.30")
	if err != nil || static.Clients[0].IPv4.Address == nil || *static.Clients[0].IPv4.Address != "10.42.0.30" {
		t.Fatalf("static address result=%+v err=%v", static, err)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "ccd/"+created.Client.ID); got != "ifconfig-push 10.42.0.30 255.255.255.0\n" {
		t.Fatalf("updated CCD=%q", got)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Lease != nil {
		t.Fatalf("lease after assignment change=%+v err=%v", loaded.Lease, err)
	}
}

func TestRevokedAddressSetAndReleaseRemainOffline(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "tablet", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Revoke(context.Background(), Selector{Name: "tablet"}, false); err != nil {
		t.Fatal(err)
	}
	changed, err := fixture.manager.AddressSet(context.Background(), Selector{Name: "tablet"}, "10.42.0.40")
	if err != nil || changed.Clients[0].IPv4.State != "retained" || changed.Clients[0].IPv4.Address == nil || *changed.Clients[0].IPv4.Address != "10.42.0.40" || len(changed.KickRequired) != 0 {
		t.Fatalf("revoked address set=%+v err=%v", changed, err)
	}
	assertMutationMissing(t, filepath.Join(fixture.root, "ccd", created.Client.ID))
	released, err := fixture.manager.AddressRelease(context.Background(), Selector{Name: "tablet"})
	if err != nil || released.Clients[0].IPv4.State != "unavailable" {
		t.Fatalf("address release=%+v err=%v", released, err)
	}
	if _, err := fixture.manager.AddressRelease(context.Background(), Selector{Name: "tablet"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("second release error=%v", err)
	}
}

func TestAddressEditAtomicallySwapsStaticAddresses(t *testing.T) {
	fixture := newMutationFixture(t)
	first, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "alpha", IPv4: "10.42.0.20"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "beta", IPv4: "10.42.0.21"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.manager.AddressEdit(context.Background(), AddressEditRequest{Selectors: []Selector{{Name: "alpha"}, {IDPrefix: ShortID(second.Client.ID)}}, Edit: func(path string) error {
		return os.WriteFile(path, []byte("# client,ipv4\nalpha,10.42.0.21\nbeta,10.42.0.20\n"), 0o600)
	}})
	if err != nil || len(result.Clients) != 2 || len(result.KickRequired) != 2 {
		t.Fatalf("address edit=%+v err=%v", result, err)
	}
	loadedFirst, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, first.Client.ID)
	if err != nil || loadedFirst.Assignment == nil || loadedFirst.Assignment.Address.String() != "10.42.0.21" {
		t.Fatalf("first swapped state=%+v err=%v", loadedFirst, err)
	}
	loadedSecond, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, second.Client.ID)
	if err != nil || loadedSecond.Assignment == nil || loadedSecond.Assignment.Address.String() != "10.42.0.20" {
		t.Fatalf("second swapped state=%+v err=%v", loadedSecond, err)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "ccd/"+first.Client.ID); !strings.Contains(got, "10.42.0.21") {
		t.Fatalf("first swapped CCD=%q", got)
	}
}

func TestAddressEditRejectsDuplicateAndKeepsAssignments(t *testing.T) {
	fixture := newMutationFixture(t)
	first, _ := fixture.manager.Create(context.Background(), CreateRequest{Name: "alpha", IPv4: "10.42.0.20"})
	second, _ := fixture.manager.Create(context.Background(), CreateRequest{Name: "beta", IPv4: "10.42.0.21"})
	_, err := fixture.manager.AddressEdit(context.Background(), AddressEditRequest{All: true, Edit: func(path string) error {
		return os.WriteFile(path, []byte("# client,ipv4\nalpha,10.42.0.30\nbeta,10.42.0.30\n"), 0o600)
	}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate edit error=%v", err)
	}
	for id, expected := range map[string]string{first.Client.ID: "10.42.0.20", second.Client.ID: "10.42.0.21"} {
		loaded, loadErr := fixture.store.LoadClient(context.Background(), fixture.instance.ID, id)
		if loadErr != nil || loaded.Assignment == nil || loaded.Assignment.Address.String() != expected {
			t.Fatalf("unchanged assignment id=%s state=%+v err=%v", id, loaded, loadErr)
		}
	}
}

func TestAddressEditRejectsUnsafeEditorFile(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "alpha", IPv4: "10.42.0.20"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.manager.AddressEdit(context.Background(), AddressEditRequest{All: true, Edit: func(path string) error {
		return os.Chmod(path, 0o644)
	}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe editor file error=%v", err)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Assignment == nil || loaded.Assignment.Address.String() != "10.42.0.20" {
		t.Fatalf("state after unsafe editor file=%+v err=%v", loaded, err)
	}
}

func TestAddressCommitFailureRestoresCCDAndAssignment(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "10.42.0.20"})
	if err != nil {
		t.Fatal(err)
	}
	ccdBefore := readMutationArtifact(t, fixture.artifacts, "ccd/"+created.Client.ID)
	injected := errors.New("injected address commit")
	manager := managerWithStore(t, fixture, commitFailureStore{MutationStore: fixture.store, addressErr: injected})
	if _, err := manager.AddressSet(context.Background(), Selector{Name: "laptop"}, "dynamic"); !errors.Is(err, injected) {
		t.Fatalf("address commit error=%v", err)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Assignment == nil || loaded.Assignment.Address.String() != "10.42.0.20" {
		t.Fatalf("address rollback state=%+v err=%v", loaded, err)
	}
	if got := readMutationArtifact(t, fixture.artifacts, "ccd/"+created.Client.ID); got != ccdBefore {
		t.Fatal("address rollback did not restore CCD")
	}
}

func TestAddressSetRejectsZeroDynamicCapacityBeforeMutation(t *testing.T) {
	fixture := newMutationFixture(t)
	created, err := fixture.manager.Create(context.Background(), CreateRequest{Name: "laptop", IPv4: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	clients, err := fixture.store.ListClients(context.Background(), fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	instance := fixture.instance
	instance.Applied.Config.IPv4.DynamicPoolSize = 0
	_, err = fixture.manager.applyAddressesLocked(context.Background(), instance, clients, clients, map[string]string{created.Client.ID: "dynamic"}, "client.address.set")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero dynamic capacity error=%v", err)
	}
	loaded, err := fixture.store.LoadClient(context.Background(), fixture.instance.ID, created.Client.ID)
	if err != nil || loaded.Assignment == nil || loaded.Assignment.Kind != "static" {
		t.Fatalf("state after rejected dynamic set=%+v err=%v", loaded, err)
	}
}

type commitFailureStore struct {
	MutationStore
	createErr  error
	renameErr  error
	revokeErr  error
	reissueErr error
	deleteErr  error
	addressErr error
}

func (store commitFailureStore) CommitCreateClientOperation(context.Context, string, string, storesqlite.ClientState, json.RawMessage, time.Time) error {
	return store.createErr
}

func (store commitFailureStore) CommitRenameClientOperation(context.Context, string, string, string, string, string, storesqlite.ArtifactMetadata, storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.renameErr
}

func (store commitFailureStore) CommitRevokeClientOperation(context.Context, string, string, string, string, bool, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.revokeErr
}

func (store commitFailureStore) CommitReissueClientOperation(context.Context, string, string, string, string, domain.ClientStatus, storesqlite.AddressAssignment, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.reissueErr
}

func (store commitFailureStore) CommitDeleteClientOperation(context.Context, string, string, string, string, domain.ClientStatus, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.deleteErr
}

func (store commitFailureStore) CommitClientAddressOperation(context.Context, string, string, string, []storesqlite.ClientAddressChange, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error {
	return store.addressErr
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
