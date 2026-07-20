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
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type Supervisor struct {
	DataDir        string
	RuntimeDir     string
	OpenVPNBinary  string
	BrokerBinary   string
	StartupTimeout time.Duration
	StopTimeout    time.Duration
	LockTimeout    time.Duration
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
	runtimeLock, err := acquireLock(ctx, filepath.Join(supervisor.RuntimeDir, ".runtime.lock"), artifact.LockShared, supervisor.LockTimeout)
	if err != nil {
		return err
	}
	defer runtimeLock.Release()
	listen := filepath.Join(supervisor.RuntimeDir, "management.sock")
	backend := filepath.Join(supervisor.RuntimeDir, "openvpn-management.sock")
	if err := removeSocket(backend); err != nil {
		return err
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
		return fmt.Errorf("start management broker: %w", err)
	}
	if err := waitSocket(ctx, listen, brokerProcess, supervisor.StartupTimeout); err != nil {
		terminate(brokerProcess, supervisor.StopTimeout)
		return err
	}
	openvpnProcess, err := startProcess(openvpnCommand)
	if err != nil {
		terminate(brokerProcess, supervisor.StopTimeout)
		return fmt.Errorf("start OpenVPN: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			_ = signalProcess(openvpnProcess, syscall.SIGTERM)
			_ = signalProcess(brokerProcess, syscall.SIGTERM)
			waitBoth(openvpnProcess, brokerProcess, supervisor.StopTimeout)
			return nil
		case <-hup:
			if err := signalProcess(openvpnProcess, syscall.SIGHUP); err != nil {
				waitBoth(openvpnProcess, brokerProcess, supervisor.StopTimeout)
				return fmt.Errorf("forward HUP to OpenVPN: %w", err)
			}
		case <-openvpnProcess.done:
			terminate(brokerProcess, supervisor.StopTimeout)
			err := openvpnProcess.err
			if err == nil {
				return fmt.Errorf("OpenVPN exited unexpectedly")
			}
			return fmt.Errorf("OpenVPN exited: %w", err)
		case <-brokerProcess.done:
			terminate(openvpnProcess, supervisor.StopTimeout)
			err := brokerProcess.err
			if err == nil {
				return fmt.Errorf("management broker exited unexpectedly")
			}
			return fmt.Errorf("management broker exited: %w", err)
		}
	}
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
