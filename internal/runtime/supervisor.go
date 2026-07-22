// Package runtime coordinates the single-node OpenVPN runtime processes.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	networkcontrol "github.com/yjrszcq/openvpn-docker/internal/network"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type NetworkCoordinator interface {
	Reconcile(context.Context, string, domain.Config) error
	Cleanup(context.Context, string) error
}

type Supervisor struct {
	DataDir        string
	RuntimeDir     string
	OpenVPNBinary  string
	BrokerBinary   string
	StartupTimeout time.Duration
	StopTimeout    time.Duration
	LockTimeout    time.Duration
	Network        NetworkCoordinator
	ReloadInstance func(context.Context, string) (storesqlite.InstanceState, error)
}

var commandBuilder = exec.Command

func (supervisor Supervisor) Run(ctx context.Context, hup <-chan os.Signal, instance storesqlite.InstanceState) error {
	if supervisor.StartupTimeout <= 0 {
		supervisor.StartupTimeout = 5 * time.Second
	}
	if supervisor.StopTimeout <= 0 {
		supervisor.StopTimeout = 5 * time.Second
	}
	if supervisor.LockTimeout <= 0 {
		supervisor.LockTimeout = 250 * time.Millisecond
	}
	for _, value := range []string{supervisor.DataDir, supervisor.RuntimeDir} {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("runtime directories must be clean and absolute")
		}
	}
	if supervisor.OpenVPNBinary == "" || supervisor.BrokerBinary == "" {
		return fmt.Errorf("runtime binaries are required")
	}
	if err := secureRuntimeDirectory(supervisor.RuntimeDir); err != nil {
		return err
	}
	serverLock, err := acquireLock(ctx, filepath.Join(supervisor.RuntimeDir, ".server.lock"), artifact.LockExclusive, supervisor.LockTimeout)
	if err != nil {
		return err
	}
	defer serverLock.Release()
	control, err := startControlServer(ctx, supervisor.RuntimeDir)
	if err != nil {
		return err
	}
	defer control.Close()
	runtimeLock, err := acquireLock(ctx, artifact.RuntimeLockPath(supervisor.DataDir), artifact.LockShared, supervisor.LockTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if runtimeLock != nil {
			_ = runtimeLock.Release()
		}
	}()
	coordinator := supervisor.Network
	if coordinator == nil {
		coordinator, err = networkcontrol.New(networkcontrol.Config{
			IPBinary:       environmentOr("OVPN_IP_BIN", "ip"),
			IPTablesBinary: environmentOr("OVPN_IPTABLES_BIN", "iptables"),
			ForwardingFile: environmentOr("OVPN_IP_FORWARD_FILE", "/proc/sys/net/ipv4/ip_forward"),
		})
		if err != nil {
			return err
		}
	}
	reload := supervisor.ReloadInstance
	if reload == nil {
		reload = LoadInstance
	}
	var processes *runtimeProcesses
	networkActive := false
	cleanupNetwork := func(strict bool) error {
		if !networkActive {
			return nil
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := coordinator.Cleanup(cleanupContext, instance.ID); err != nil {
			if strict {
				return fmt.Errorf("clean up IPv4 network before config apply: %w", err)
			}
			fmt.Fprintf(os.Stderr, "ovpn: warning: IPv4 network cleanup failed: %v\n", err)
		}
		networkActive = false
		return nil
	}
	stopRuntime := func() {
		if processes != nil {
			_ = signalProcess(processes.openvpn, syscall.SIGTERM)
			_ = signalProcess(processes.broker, syscall.SIGTERM)
			waitBoth(processes.openvpn, processes.broker, supervisor.StopTimeout)
			processes = nil
		}
	}
	startRuntime := func() error {
		if err := coordinator.Reconcile(ctx, instance.ID, instance.Applied.Config); err != nil {
			return fmt.Errorf("reconcile IPv4 network: %w", err)
		}
		networkActive = true
		started, err := supervisor.startProcesses(ctx, instance)
		if err != nil {
			_ = cleanupNetwork(false)
			return err
		}
		processes = started
		return nil
	}
	if err := startRuntime(); err != nil {
		return err
	}
	defer func() {
		stopRuntime()
		_ = cleanupNetwork(false)
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hup:
			if err := signalProcess(processes.openvpn, syscall.SIGHUP); err != nil {
				return fmt.Errorf("forward HUP to OpenVPN: %w", err)
			}
		case <-processes.openvpn.done:
			terminate(processes.broker, supervisor.StopTimeout)
			err := processes.openvpn.err
			if err == nil {
				return fmt.Errorf("OpenVPN exited unexpectedly")
			}
			return fmt.Errorf("OpenVPN exited: %w", err)
		case <-processes.broker.done:
			terminate(processes.openvpn, supervisor.StopTimeout)
			err := processes.broker.err
			if err == nil {
				return fmt.Errorf("management broker exited unexpectedly")
			}
			return fmt.Errorf("management broker exited: %w", err)
		case request := <-control.requests:
			stopRuntime()
			if err := cleanupNetwork(true); err != nil {
				request.ready <- err
				return err
			}
			if err := runtimeLock.Release(); err != nil {
				request.ready <- err
				return err
			}
			runtimeLock = nil
			request.ready <- nil
			select {
			case resume := <-request.resume:
				if !resume {
					return fmt.Errorf("configuration apply disconnected while the runtime was stopped")
				}
			case <-ctx.Done():
				return nil
			}
			runtimeLock, err = acquireLock(ctx, artifact.RuntimeLockPath(supervisor.DataDir), artifact.LockShared, supervisor.LockTimeout)
			if err == nil {
				instance, err = reload(ctx, supervisor.DataDir)
			}
			if err == nil {
				err = startRuntime()
			}
			request.done <- err
			if err != nil {
				return fmt.Errorf("restart runtime after configuration apply: %w", err)
			}
		}
	}
}

type runtimeProcesses struct {
	openvpn *childProcess
	broker  *childProcess
}

func (supervisor Supervisor) startProcesses(ctx context.Context, instance storesqlite.InstanceState) (*runtimeProcesses, error) {
	listen := filepath.Join(supervisor.RuntimeDir, "management.sock")
	backend := filepath.Join(supervisor.RuntimeDir, "openvpn-management.sock")
	if err := removeSocket(backend); err != nil {
		return nil, err
	}
	brokerCommand := commandBuilder(supervisor.BrokerBinary,
		"--listen", listen, "--backend", backend,
		"--raw-log", filepath.Join(supervisor.DataDir, "logs", "openvpn.log"),
		"--max-bytes", strconv.FormatUint(instance.Applied.Config.Logging.MaxBytes, 10),
		"--backups", strconv.FormatUint(uint64(instance.Applied.Config.Logging.Backups), 10),
		"--timeout", "5s")
	openvpnCommand := commandBuilder(supervisor.OpenVPNBinary, "--config", filepath.Join(supervisor.DataDir, "server", "server.conf"))
	brokerCommand.Stderr = os.Stderr
	openvpnCommand.Stderr = os.Stderr
	brokerProcess, err := startProcess(brokerCommand)
	if err != nil {
		return nil, fmt.Errorf("start management broker: %w", err)
	}
	if err := waitSocket(ctx, listen, brokerProcess, supervisor.StartupTimeout); err != nil {
		terminate(brokerProcess, supervisor.StopTimeout)
		return nil, err
	}
	openvpnProcess, err := startProcess(openvpnCommand)
	if err != nil {
		terminate(brokerProcess, supervisor.StopTimeout)
		return nil, fmt.Errorf("start OpenVPN: %w", err)
	}
	return &runtimeProcesses{openvpn: openvpnProcess, broker: brokerProcess}, nil
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func acquireLock(ctx context.Context, path string, mode artifact.LockMode, timeout time.Duration) (*artifact.FileLock, error) {
	lockContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return artifact.AcquireLock(lockContext, path, mode)
}

func removeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect management backend socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to replace non-socket management backend path")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale management backend socket: %w", err)
	}
	return nil
}

func secureRuntimeDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o750)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("runtime directory is unsafe")
	}
	return nil
}

type childProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

func startProcess(command *exec.Cmd) (*childProcess, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	process := &childProcess{command: command, done: make(chan struct{})}
	go func() {
		process.err = command.Wait()
		close(process.done)
	}()
	return process, nil
}

func waitSocket(ctx context.Context, path string, process *childProcess, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-process.done:
			if process.err == nil {
				return fmt.Errorf("management broker exited unexpectedly during startup")
			}
			return fmt.Errorf("management broker exited during startup: %w", process.err)
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("management broker did not create its socket")
		case <-ticker.C:
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
				connection, err := net.DialTimeout("unix", path, 100*time.Millisecond)
				if err == nil {
					connection.Close()
					return nil
				}
			}
		}
	}
}

func signalProcess(process *childProcess, value syscall.Signal) error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return os.ErrProcessDone
	}
	select {
	case <-process.done:
		return os.ErrProcessDone
	default:
		return process.command.Process.Signal(value)
	}
}

func terminate(process *childProcess, timeout time.Duration) {
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	select {
	case <-process.done:
		return
	default:
	}
	_ = process.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-process.done:
	case <-time.After(timeout):
		_ = process.command.Process.Kill()
		<-process.done
	}
}

func waitBoth(first, second *childProcess, timeout time.Duration) {
	terminate(first, timeout)
	terminate(second, timeout)
}
