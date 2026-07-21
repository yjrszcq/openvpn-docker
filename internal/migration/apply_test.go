package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
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
	for _, key := range []string{"meta/state.db", "server/server.conf", "clients/active/laptop.ovpn", SnapshotRelativePath} {
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
	shared, err := artifact.AcquireLock(context.Background(), filepath.Join(options.RuntimeDir, ".runtime.lock"), artifact.LockShared)
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
