package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("OVPN_RUNTIME_HELPER") != "1" {
		return
	}
	separator := 0
	for index, value := range os.Args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	role, args := os.Args[separator+1], os.Args[separator+2:]
	markerDir := os.Getenv("OVPN_RUNTIME_MARKERS")
	switch role {
	case "broker":
		listen := option(args, "--listen")
		listener, err := net.Listen("unix", listen)
		if err != nil {
			os.Exit(3)
		}
		_ = os.Chmod(listen, 0o600)
		_ = os.WriteFile(filepath.Join(markerDir, "broker-started"), []byte("ok"), 0o600)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		_ = listener.Close()
		_ = os.Remove(listen)
	case "openvpn":
		_ = os.WriteFile(filepath.Join(markerDir, "openvpn-started"), []byte("ok"), 0o600)
		if os.Getenv("OVPN_RUNTIME_OPENVPN_EXIT") == "1" {
			os.Exit(4)
		}
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		for value := range signals {
			if value == syscall.SIGHUP {
				_ = os.WriteFile(filepath.Join(markerDir, "openvpn-hup"), []byte("ok"), 0o600)
				continue
			}
			return
		}
	default:
		os.Exit(2)
	}
}

func TestSupervisorStartsForwardsHUPAndStopsProcesses(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(filepath.Join(dataDir, "server"), 0o750); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hup := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- testSupervisor(dataDir, runtimeDir).Run(ctx, hup, testInstance())
	}()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	hup <- syscall.SIGHUP
	waitForFile(t, filepath.Join(markers, "openvpn-hup"))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("supervisor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if _, err := os.Lstat(filepath.Join(runtimeDir, "management.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker socket remains after shutdown: %v", err)
	}
}

func TestSupervisorStopsBrokerWhenOpenVPNExits(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	t.Setenv("OVPN_RUNTIME_OPENVPN_EXIT", "1")
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "OpenVPN exited") {
		t.Fatalf("unexpected critical exit: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(markers, "broker-started")); statErr != nil {
		t.Fatalf("broker never started: %v", statErr)
	}
}

func TestSupervisorRefusesUnsafeBackendPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "openvpn-management.sock"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), runtimeDir)
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("unsafe backend path was not refused: %v", err)
	}
}

func installHelperBuilder(t *testing.T) {
	t.Helper()
	previous := commandBuilder
	commandBuilder = func(name string, args ...string) *exec.Cmd {
		role := "openvpn"
		if strings.Contains(name, "broker") {
			role = "broker"
		}
		helperArgs := []string{"-test.run=TestRuntimeHelperProcess", "--", role}
		return exec.Command(os.Args[0], append(helperArgs, args...)...)
	}
	t.Cleanup(func() { commandBuilder = previous })
}

func testSupervisor(dataDir, runtimeDir string) Supervisor {
	return Supervisor{DataDir: dataDir, RuntimeDir: runtimeDir, OpenVPNBinary: "fake-openvpn", BrokerBinary: "fake-broker", StartupTimeout: time.Second, StopTimeout: time.Second}
}

func testInstance() storesqlite.InstanceState {
	return storesqlite.InstanceState{Applied: configservice.AppliedSnapshot{Config: domain.Config{Logging: domain.LoggingConfig{MaxBytes: 1024, Backups: 1}}}}
}

func option(args []string, name string) string {
	for index := 0; index+1 < len(args); index += 2 {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
