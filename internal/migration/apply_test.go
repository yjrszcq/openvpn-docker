package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestApplyMigratesSchema3AndIsIdempotent(t *testing.T) {
	fixture := makeLegacyFixture(t)
	writeLegacy(t, fixture.root, "meta/audit.jsonl", "{\"timestamp\":\"2026-07-21T00:00:00Z\",\"event\":\"client_ip_apply\",\"outcome\":\"applied\",\"client_id\":null,\"client_name\":null,\"legacy\":false}\n", 0o600)
	options := migrationOptions(t, fixture)
	result, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Recovered || result.InstanceID != testInstanceID || result.FinalState != "HEALTHY" || result.Clients != 1 {
		t.Fatalf("result=%+v", result)
	}
	for _, key := range []string{"config/project.env", "config/schema-version", "meta/instance.json", "meta/instance-id", "meta/client-state.csv", "meta/client-ip.csv", "meta/audit.jsonl", "cache/client-leases"} {
		if _, err := os.Lstat(filepath.Join(fixture.root, filepath.FromSlash(key))); !os.IsNotExist(err) {
			t.Errorf("legacy path remains: %s err=%v", key, err)
		}
	}
	for _, key := range []string{"meta/state.db", "server/server.conf", "clients/active/laptop.ovpn", SnapshotRelativePath, SnapshotDigestRelativePath} {
		if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(key))); err != nil {
			t.Errorf("missing %s: %v", key, err)
		}
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(fixture.root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client, err := store.LoadClient(context.Background(), instance.ID, testClientID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	legacyFound := false
	for _, event := range events {
		if event.Type == "legacy.client_ip_apply" {
			legacyFound = true
		}
	}
	if client.Client.Name != "laptop" || client.Assignment == nil || client.Assignment.Kind != "dynamic" || client.Lease == nil || len(client.Artifacts) != 3 || len(events) < 5 || !legacyFound {
		t.Fatalf("client=%+v events=%d", client, len(events))
	}
	report := statecontrol.Scan(context.Background(), statecontrol.Options{DataDir: fixture.root, ConfigFile: "", ServerName: "openvpn-server", Renderer: options.Renderer, Paths: options.Paths, Now: fixture.now})
	if report.State != statecontrol.Healthy {
		t.Fatalf("doctor=%+v", report)
	}
	second, err := Apply(context.Background(), options)
	if err != nil || second.Applied || second.SourceSchema != 4 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestApplyImportsLargeClientSet(t *testing.T) {
	fixture := makeLegacyFixture(t)
	for index := 0; index < 48; index++ {
		id := fmt.Sprintf("%08x-1000-4000-8000-%012x", index+1, index+1)
		addLegacyClient(t, fixture, id, fmt.Sprintf("zbulk-%03d", index), domain.ClientActive, int64(100+index))
	}
	options := migrationOptions(t, fixture)
	result, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Clients != 49 {
		t.Fatalf("client count=%d", result.Clients)
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(fixture.root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clients, err := store.ListClients(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 49 {
		t.Fatalf("stored clients=%d", len(clients))
	}
}

func TestApplyImportsRevokedAndDeletedLifecycle(t *testing.T) {
	fixture := makeLegacyFixture(t)
	revokedID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	deletedID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	addLegacyClient(t, fixture, revokedID, "z-revoked", domain.ClientRevoked, 50)
	addDeletedLegacyClient(t, fixture, deletedID, "zz-deleted")
	writeLegacy(t, fixture.root, "meta/audit.jsonl", fmt.Sprintf("{\"timestamp\":\"2026-07-21T01:00:00Z\",\"event\":\"client_lifecycle\",\"operation\":\"revoke\",\"outcome\":\"applied\",\"client_id\":\"%s\",\"client_name\":\"z-revoked\",\"legacy\":false}\n{\"timestamp\":\"2026-07-21T02:00:00Z\",\"event\":\"client_lifecycle\",\"operation\":\"delete\",\"outcome\":\"applied\",\"client_id\":\"%s\",\"client_name\":\"zz-deleted\",\"legacy\":false}\n", revokedID, deletedID), 0o600)
	options := migrationOptions(t, fixture)
	if _, err := Apply(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	store, err := storesqlite.Open(context.Background(), filepath.Join(fixture.root, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := store.LoadClient(context.Background(), instance.ID, revokedID)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.LoadClient(context.Background(), instance.ID, deletedID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Client.Status != domain.ClientRevoked || revoked.RevokedAt == nil || revoked.Assignment == nil || revoked.Assignment.Status != storesqlite.AssignmentRetained || len(revoked.Artifacts) != 3 {
		t.Fatalf("revoked=%+v", revoked)
	}
	if deleted.Client.Status != domain.ClientDeleted || deleted.DeletedAt == nil || deleted.Assignment != nil || len(deleted.Artifacts) != 0 {
		t.Fatalf("deleted=%+v", deleted)
	}
}

func TestMigrationSnapshotRestoresExactSchema3Handoff(t *testing.T) {
	fixture := makeLegacyFixture(t)
	writeLegacy(t, fixture.root, "data/opaque-state", "preserve-me\n", 0o600)
	certificatePath := filepath.Join(fixture.root, "pki", "issued", testClientID+".crt")
	before, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	options := migrationOptions(t, fixture)
	result, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(result.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(result.SnapshotDigestPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(sidecar))
	if len(fields) != 2 || fields[0] != digest || fields[1] != filepath.Base(result.SnapshotPath) {
		t.Fatalf("invalid digest sidecar %q", sidecar)
	}
	if err := restoreSnapshot(fixture.root, result.SnapshotPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "meta", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("SQLite remains after rollback: %v", err)
	}
	after, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rollback changed the UUID certificate")
	}
	if opaque, err := os.ReadFile(filepath.Join(fixture.root, "data", "opaque-state")); err != nil || string(opaque) != "preserve-me\n" {
		t.Fatalf("opaque backup unit state=%q err=%v", opaque, err)
	}
	source, err := ReadSchema3(context.Background(), fixture.root, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if source.Instance.ID != testInstanceID || len(source.Clients) != 1 || source.Clients[0].Client.ID != testClientID {
		t.Fatalf("restored source=%+v", source)
	}
	plan, err := BuildPlan(context.Background(), fixture.root, fixture.now)
	if err != nil || plan.Status != "ready" {
		t.Fatalf("rollback plan=%+v err=%v", plan, err)
	}
}

func TestApplyRejectsSymlinkInSnapshotUnitBeforeMarker(t *testing.T) {
	fixture := makeLegacyFixture(t)
	if err := os.MkdirAll(filepath.Join(fixture.root, "server"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../config/project.env", filepath.Join(fixture.root, "server", "unsafe")); err != nil {
		t.Fatal(err)
	}
	options := migrationOptions(t, fixture)
	if _, err := Apply(context.Background(), options); err == nil {
		t.Fatal("snapshot symlink was accepted")
	}
	for _, key := range []string{SnapshotRelativePath, ManifestRelativePath} {
		if _, err := os.Lstat(filepath.Join(fixture.root, filepath.FromSlash(key))); !os.IsNotExist(err) {
			t.Fatalf("transaction state %s remains: %v", key, err)
		}
	}
}

func TestMigrationMarkerRejectsSymlinkAndInvalidState(t *testing.T) {
	root := t.TempDir()
	operationID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	marker := transactionMarker{Version: 1, OperationID: operationID, State: "snapshot-ready", Stage: "repair/migrations/stage-" + operationID, Snapshot: SnapshotRelativePath, SnapshotDigest: strings.Repeat("a", 64), Installed: []string{}}
	if err := writeMarker(root, marker); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(ManifestRelativePath))
	target := filepath.Join(root, "target.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarker(root); err == nil {
		t.Fatal("symlink marker was accepted")
	}
	marker.State = "unknown"
	if err := validateMarker(marker); err == nil {
		t.Fatal("unknown marker state was accepted")
	}
}

func TestApplyRecoversEveryCrashBoundary(t *testing.T) {
	for _, point := range []CrashPoint{CrashAfterSnapshot, CrashAfterStage, CrashAfterInstall, CrashAfterCommit} {
		t.Run(string(point), func(t *testing.T) {
			fixture := makeLegacyFixture(t)
			options := migrationOptions(t, fixture)
			injected := errors.New("injected crash")
			fired := false
			options.Crash = func(current CrashPoint) error {
				if current == point && !fired {
					fired = true
					return injected
				}
				return nil
			}
			if _, err := Apply(context.Background(), options); !errors.Is(err, injected) {
				t.Fatalf("first error=%v", err)
			}
			if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(ManifestRelativePath))); err != nil {
				t.Fatalf("marker missing: %v", err)
			}
			options.Crash = nil
			result, err := Apply(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Recovered {
				t.Fatalf("recovery not reported: %+v", result)
			}
			plan, err := BuildPlan(context.Background(), fixture.root, fixture.now)
			if err != nil || plan.Status != "current" {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(ManifestRelativePath))); !os.IsNotExist(err) {
				t.Fatalf("marker remains: %v", err)
			}
		})
	}
}

func TestApplyRefusesCorruptRecoverySnapshot(t *testing.T) {
	fixture := makeLegacyFixture(t)
	options := migrationOptions(t, fixture)
	options.Crash = func(point CrashPoint) error {
		if point == CrashAfterSnapshot {
			return errors.New("stop")
		}
		return nil
	}
	if _, err := Apply(context.Background(), options); err == nil {
		t.Fatal("crash was not injected")
	}
	snapshot := filepath.Join(fixture.root, filepath.FromSlash(SnapshotRelativePath))
	file, err := os.OpenFile(snapshot, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("damaged"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	options.Crash = nil
	if _, err := Apply(context.Background(), options); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(ManifestRelativePath))); err != nil {
		t.Fatalf("recovery marker was lost: %v", err)
	}
}

func TestApplyRequiresMaintenanceAndStoppedRuntime(t *testing.T) {
	fixture := makeLegacyFixture(t)
	options := migrationOptions(t, fixture)
	options.Maintenance = false
	if _, err := Apply(context.Background(), options); !errors.Is(err, ErrMaintenanceRequired) {
		t.Fatalf("maintenance error=%v", err)
	}
	options.Maintenance = true
	shared, err := artifact.AcquireLock(context.Background(), artifact.RuntimeLockPath(options.DataDir), artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Apply(ctx, options); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("lock error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(SnapshotRelativePath))); !os.IsNotExist(err) {
		t.Fatalf("snapshot created before lock: %v", err)
	}
}

func migrationOptions(t *testing.T, fixture legacyFixture) ApplyOptions {
	t.Helper()
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"), contract)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(t.TempDir(), "run")
	return ApplyOptions{DataDir: fixture.root, RuntimeDir: runtimeDir, Maintenance: true, Version: "4.0.0-test", Renderer: renderer, Paths: render.Paths{DataDir: fixture.root, RuntimeDir: runtimeDir}, Now: fixture.now}
}
