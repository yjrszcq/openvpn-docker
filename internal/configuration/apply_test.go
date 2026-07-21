package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestApplyCommitsNetworkAddressAndArtifactsAsOneOperation(t *testing.T) {
	manager, store, root, runtimeDir, instance := applyFixture(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	address, _ := domain.ParseAddress("10.42.0.10")
	client := storesqlite.ClientState{Client: domain.Client{ID: "61616161-6161-4616-8616-616161616161", Name: "alpha", Status: domain.ClientActive}, CreatedAt: now, Assignment: &storesqlite.AddressAssignment{ID: "62626262-6262-4626-8626-626262626262", NetworkID: instance.NetworkID, Kind: "static", Address: &address, Status: storesqlite.AssignmentActive, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	desired := parseApplyConfig(t, strings.Replace(applyYAML("vpn.example.test", "10.42.0.0/24"), "10.42.0.0/24", "10.43.0.0/24", 1))
	result, err := manager.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.OperationID == "" || result.Plan.Configuration.TargetRevision != 2 {
		t.Fatalf("result=%+v", result)
	}
	loaded, err := store.LoadOnlyInstance(context.Background())
	if err != nil || loaded.Applied.Revision != 2 || loaded.Applied.Config.IPv4.Network.String() != "10.43.0.0/24" {
		t.Fatalf("instance=%+v err=%v", loaded, err)
	}
	loadedClient, err := store.LoadClient(context.Background(), instance.ID, client.Client.ID)
	if err != nil || loadedClient.Assignment.Address.String() != "10.43.0.10" {
		t.Fatalf("client=%+v err=%v", loadedClient, err)
	}
	server, err := os.ReadFile(filepath.Join(root, "server", "server.conf"))
	if err != nil || !strings.Contains(string(server), "server 10.43.0.0") {
		t.Fatalf("server config=%q err=%v", server, err)
	}
	ccd, err := os.ReadFile(filepath.Join(root, "ccd", client.Client.ID))
	if err != nil || string(ccd) != "ifconfig-push 10.43.0.10 255.255.255.0\n" {
		t.Fatalf("ccd=%q err=%v", ccd, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, ".runtime.lock")); err != nil {
		t.Fatalf("runtime lock file was not retained safely: %v", err)
	}
}

func TestApplyRollsBackInstalledFilesWhenSQLiteCommitFails(t *testing.T) {
	_, store, root, runtimeDir, instance := applyFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "server"), 0o700); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(root, "server", "server.conf")
	if err := os.WriteFile(serverPath, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	renderer := applyRenderer(t)
	failing := &failingConfigurationStore{Store: store}
	manager, err := NewManager(failing, local, renderer, render.Paths{DataDir: root, RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatal(err)
	}
	desired := parseApplyConfig(t, strings.Replace(applyYAML("vpn.example.test", "10.42.0.0/24"), "clientToClient: true", "clientToClient: false", 1))
	if _, err := manager.Apply(context.Background(), desired); err == nil || !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("apply error=%v", err)
	}
	content, err := os.ReadFile(serverPath)
	if err != nil || string(content) != "original\n" {
		t.Fatalf("server rollback=%q err=%v", content, err)
	}
	loaded, err := store.LoadOnlyInstance(context.Background())
	if err != nil || loaded.Applied.Revision != instance.Applied.Revision || !loaded.Applied.Config.ClientToClient {
		t.Fatalf("SQLite rollback=%+v err=%v", loaded, err)
	}
	pending, err := store.PendingOperations(context.Background(), instance.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending operations=%+v err=%v", pending, err)
	}
}

func TestApplyRefusesRunningSupervisorLock(t *testing.T) {
	manager, store, _, runtimeDir, instance := applyFixture(t)
	shared, err := artifact.AcquireLock(context.Background(), filepath.Join(runtimeDir, ".runtime.lock"), artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	desired := parseApplyConfig(t, strings.Replace(applyYAML("vpn.example.test", "10.42.0.0/24"), "vpn.example.test", "new.example.test", 1))
	if _, err := manager.Apply(ctx, desired); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("lock error=%v", err)
	}
	loaded, err := store.LoadOnlyInstance(context.Background())
	if err != nil || loaded.Applied.Revision != instance.Applied.Revision {
		t.Fatalf("locked apply changed state=%+v err=%v", loaded, err)
	}
}

func TestApplyRefusesPendingOperationBeforePlanningNewMutation(t *testing.T) {
	manager, store, _, _, instance := applyFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	operationID, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"version":1}`)
	if err := store.PrepareOperation(context.Background(), storesqlite.Operation{ID: operationID, InstanceID: instance.ID, Kind: "client.create", State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	desired := parseApplyConfig(t, strings.Replace(applyYAML("vpn.example.test", "10.42.0.0/24"), "vpn.example.test", "new.example.test", 1))
	if _, err := manager.Apply(context.Background(), desired); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("pending operation error=%v", err)
	}
	loaded, err := store.LoadOnlyInstance(context.Background())
	if err != nil || loaded.Applied.Revision != 1 {
		t.Fatalf("pending refusal changed state=%+v err=%v", loaded, err)
	}
}

func TestApplyPersistentChecksRuntimeLockBeforeOpeningSQLite(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data-without-database")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	lock, err := artifact.AcquireLock(context.Background(), filepath.Join(runtimeDir, ".runtime.lock"), artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = ApplyPersistent(ctx, domain.Config{}, render.Renderer{}, render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir})
	if !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("apply opened missing SQLite before checking runtime lock: %v", err)
	}
}

func TestApplyRefusesOrphanArtifactOperation(t *testing.T) {
	manager, store, root, _, instance := applyFixture(t)
	local, err := artifact.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	operation, err := local.BeginOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Rollback()
	desired := parseApplyConfig(t, strings.Replace(applyYAML("vpn.example.test", "10.42.0.0/24"), "vpn.example.test", "new.example.test", 1))
	if _, err := manager.Apply(context.Background(), desired); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("orphan artifact operation error=%v", err)
	}
	loaded, err := store.LoadOnlyInstance(context.Background())
	if err != nil || loaded.Applied.Revision != instance.Applied.Revision {
		t.Fatalf("orphan refusal changed state=%+v err=%v", loaded, err)
	}
}

type failingConfigurationStore struct {
	*storesqlite.Store
}

func (store *failingConfigurationStore) CommitConfigurationOperation(context.Context, storesqlite.ConfigurationCommit) error {
	return errors.New("injected commit failure")
}

func applyFixture(t *testing.T) (*Manager, *storesqlite.Store, string, string, storesqlite.InstanceState) {
	t.Helper()
	root := t.TempDir()
	runtimeDir := filepath.Join(t.TempDir(), "run")
	store, err := storesqlite.Create(context.Background(), filepath.Join(root, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	value := parseApplyConfig(t, applyYAML("vpn.example.test", "10.42.0.0/24"))
	snapshot, err := configservice.NewAppliedSnapshot(1, value)
	if err != nil {
		t.Fatal(err)
	}
	instance := storesqlite.InstanceState{ID: "60606060-6060-4606-8606-606060606060", CreatedAt: time.Now().UTC(), Applied: snapshot}
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
	manager, err := NewManager(store, local, applyRenderer(t), render.Paths{DataDir: root, RuntimeDir: runtimeDir})
	if err != nil {
		t.Fatal(err)
	}
	return manager, store, root, runtimeDir, instance
}

func applyRenderer(t *testing.T) render.Renderer {
	t.Helper()
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"), contract)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}

func parseApplyConfig(t *testing.T, data string) domain.Config {
	t.Helper()
	value, err := configservice.Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func applyYAML(endpoint, network string) string {
	return "version: 1\nserver:\n  endpoint: " + endpoint + "\n  clientToClient: true\nipv4:\n  network: " + network + "\n  dynamicPoolSize: 64\n"
}
