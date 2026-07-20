// Package broker owns the single OpenVPN management connection and exposes a
// serialized local Unix-socket proxy for concurrent control-plane clients.
package broker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxLine = 64 << 10

type Config struct {
	Listen   string
	Backend  string
	RawLog   string
	MaxBytes int64
	Backups  int
	Timeout  time.Duration
}

type Service struct {
	config    Config
	backend   *Backend
	listener  net.Listener
	closeOnce sync.Once
}

func New(config Config) (*Service, error) {
	for _, value := range []string{config.Listen, config.Backend, config.RawLog} {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return nil, fmt.Errorf("broker paths must be clean and absolute")
		}
	}
	if config.Listen == config.Backend || config.MaxBytes < 1 || config.Backups < 0 || config.Timeout <= 0 {
		return nil, fmt.Errorf("invalid broker configuration")
	}
	log, err := newRotatingLog(config.RawLog, config.MaxBytes, config.Backups)
	if err != nil {
		return nil, err
	}
	return &Service{config: config, backend: newBackend(config.Backend, log, config.Timeout)}, nil
}

func (service *Service) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(service.config.Listen), 0o750); err != nil {
		return err
	}
	if err := removeSocket(service.config.Listen); err != nil {
		return err
	}
	listener, err := net.Listen("unix", service.config.Listen)
	if err != nil {
		return err
	}
	service.listener = listener
	if err := os.Chmod(service.config.Listen, 0o600); err != nil {
		listener.Close()
		return err
	}
	maintainCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go service.backend.Maintain(maintainCtx)
	go func() {
		<-ctx.Done()
		service.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go service.serveClient(ctx, connection)
	}
}

func (service *Service) Close() error {
	var result error
	service.closeOnce.Do(func() {
		service.backend.Close()
		if service.listener != nil {
			result = service.listener.Close()
		}
		if err := removeSocket(service.config.Listen); err != nil {
			result = errors.Join(result, err)
		}
	})
	return result
}

func (service *Service) serveClient(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	if _, err := io.WriteString(connection, ">INFO:OpenVPN Management Broker Version 1\n"); err != nil {
		return
	}
	reader := bufio.NewReaderSize(connection, maxLine)
	for {
		line, err := readLine(reader)
		if err != nil {
			return
		}
		if !utf8.ValidString(line) {
			_, _ = io.WriteString(connection, "ERROR: invalid UTF-8 management command\n")
			return
		}
		if line == "quit" {
			return
		}
		if line == "" {
			continue
		}
		var response []string
		if line == "broker-health" {
			_, err = service.backend.Request(ctx, "version", false)
			if err == nil {
				response = []string{"SUCCESS: broker connected to OpenVPN"}
			}
		} else {
			response, err = service.backend.Request(ctx, line, line == "signal SIGHUP")
		}
		if err != nil {
			response = []string{"ERROR: management backend unavailable: " + err.Error()}
		}
		if _, err := io.WriteString(connection, strings.Join(response, "\n")+"\n"); err != nil {
			return
		}
	}
}

type pendingResponse struct {
	lines []string
	done  chan struct{}
	err   error
	once  sync.Once
}

func (pending *pendingResponse) add(line string) {
	pending.lines = append(pending.lines, line)
	if line == "END" || strings.HasPrefix(line, "SUCCESS:") || strings.HasPrefix(line, "ERROR:") {
		pending.once.Do(func() { close(pending.done) })
	}
}

func (pending *pendingResponse) fail(err error) {
	pending.err = err
	pending.once.Do(func() { close(pending.done) })
}

type Backend struct {
	path        string
	log         *rotatingLog
	timeout     time.Duration
	commandMu   sync.Mutex
	mu          sync.Mutex
	conn        net.Conn
	pending     *pendingResponse
	generation  uint64
	initChanged chan struct{}
	reloadDone  chan struct{}
	closed      bool
}

func newBackend(path string, log *rotatingLog, timeout time.Duration) *Backend {
	return &Backend{path: path, log: log, timeout: timeout, initChanged: make(chan struct{})}
}

func (backend *Backend) Request(ctx context.Context, command string, reload bool) ([]string, error) {
	if command == "" || strings.ContainsAny(command, "\r\n") || len(command) > maxLine {
		return nil, fmt.Errorf("invalid management command")
	}
	for {
		backend.mu.Lock()
		wait := backend.reloadDone
		backend.mu.Unlock()
		if wait != nil {
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		backend.commandMu.Lock()
		backend.mu.Lock()
		if backend.reloadDone != nil {
			backend.mu.Unlock()
			backend.commandMu.Unlock()
			continue
		}
		if backend.closed {
			backend.mu.Unlock()
			backend.commandMu.Unlock()
			return nil, net.ErrClosed
		}
		connection := backend.conn
		generation := backend.generation
		var reloadDone chan struct{}
		if reload {
			reloadDone = make(chan struct{})
			backend.reloadDone = reloadDone
		}
		backend.mu.Unlock()
		if connection == nil {
			var err error
			connection, err = backend.connect(ctx)
			if err != nil {
				backend.finishReload(reloadDone)
				backend.commandMu.Unlock()
				return nil, err
			}
		}
		response, err := backend.exchange(ctx, connection, command)
		backend.commandMu.Unlock()
		if err == nil && reload && len(response) > 0 && strings.HasPrefix(response[0], "SUCCESS:") {
			err = backend.waitInitialization(ctx, generation)
		}
		backend.finishReload(reloadDone)
		return response, err
	}
}

func (backend *Backend) EnsureConnected(ctx context.Context) error {
	backend.commandMu.Lock()
	defer backend.commandMu.Unlock()
	backend.mu.Lock()
	connection, closed := backend.conn, backend.closed
	backend.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	if connection == nil {
		_, err := backend.connect(ctx)
		return err
	}
	return nil
}

func (backend *Backend) Maintain(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, backend.timeout)
		_ = backend.EnsureConnected(attempt)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (backend *Backend) connect(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: backend.timeout}
	connection, err := dialer.DialContext(ctx, "unix", backend.path)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(connection, maxLine)
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetReadDeadline(deadline)
	} else {
		_ = connection.SetReadDeadline(time.Now().Add(backend.timeout))
	}
	if _, err := readLine(reader); err != nil {
		connection.Close()
		return nil, fmt.Errorf("read management greeting: %w", err)
	}
	_ = connection.SetReadDeadline(time.Time{})
	backend.mu.Lock()
	if backend.closed {
		backend.mu.Unlock()
		connection.Close()
		return nil, net.ErrClosed
	}
	backend.conn = connection
	backend.mu.Unlock()
	go backend.read(connection, reader)
	response, err := backend.exchange(ctx, connection, "log on all")
	if err != nil || len(response) == 0 || !strings.HasPrefix(response[0], "SUCCESS:") {
		if err == nil {
			err = fmt.Errorf("OpenVPN log subscription was rejected")
		}
		backend.disconnect(connection, err)
		return nil, err
	}
	return connection, nil
}

func (backend *Backend) exchange(ctx context.Context, connection net.Conn, command string) ([]string, error) {
	pending := &pendingResponse{done: make(chan struct{})}
	backend.mu.Lock()
	if backend.conn != connection || backend.pending != nil {
		backend.mu.Unlock()
		return nil, fmt.Errorf("management connection changed or is busy")
	}
	backend.pending = pending
	backend.mu.Unlock()
	if _, err := io.WriteString(connection, command+"\n"); err != nil {
		backend.disconnect(connection, err)
	}
	timer := time.NewTimer(backend.timeout)
	defer timer.Stop()
	select {
	case <-pending.done:
	case <-ctx.Done():
		backend.disconnect(connection, ctx.Err())
	case <-timer.C:
		backend.disconnect(connection, fmt.Errorf("OpenVPN management response timed out"))
	}
	backend.mu.Lock()
	if backend.pending == pending {
		backend.pending = nil
	}
	backend.mu.Unlock()
	if pending.err != nil {
		return nil, pending.err
	}
	return append([]string(nil), pending.lines...), nil
}

func (backend *Backend) read(connection net.Conn, reader *bufio.Reader) {
	for {
		line, err := readLine(reader)
		if err != nil {
			backend.disconnect(connection, err)
			return
		}
		if strings.HasPrefix(line, ">") {
			_ = backend.log.Write(line)
			if strings.HasSuffix(line, ",Initialization Sequence Completed") {
				backend.mu.Lock()
				backend.generation++
				close(backend.initChanged)
				backend.initChanged = make(chan struct{})
				backend.mu.Unlock()
			}
			continue
		}
		backend.mu.Lock()
		pending := backend.pending
		backend.mu.Unlock()
		if pending == nil {
			_ = backend.log.Write(">ORPHAN:" + line)
			continue
		}
		pending.add(line)
	}
}

func (backend *Backend) waitInitialization(ctx context.Context, generation uint64) error {
	deadline := time.NewTimer(backend.timeout)
	defer deadline.Stop()
	for {
		backend.mu.Lock()
		if backend.generation > generation {
			backend.mu.Unlock()
			return nil
		}
		changed := backend.initChanged
		backend.mu.Unlock()
		select {
		case <-changed:
		case <-deadline.C:
			return fmt.Errorf("OpenVPN did not complete initialization after reload")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (backend *Backend) finishReload(done chan struct{}) {
	if done == nil {
		return
	}
	backend.mu.Lock()
	if backend.reloadDone == done {
		backend.reloadDone = nil
		close(done)
	}
	backend.mu.Unlock()
}

func (backend *Backend) disconnect(connection net.Conn, cause error) {
	backend.mu.Lock()
	if backend.conn != connection {
		backend.mu.Unlock()
		return
	}
	backend.conn = nil
	pending := backend.pending
	backend.pending = nil
	backend.mu.Unlock()
	_ = connection.Close()
	if pending != nil {
		pending.fail(cause)
	}
}

func (backend *Backend) Close() {
	backend.mu.Lock()
	backend.closed = true
	connection := backend.conn
	backend.mu.Unlock()
	if connection != nil {
		backend.disconnect(connection, net.ErrClosed)
	}
}

type rotatingLog struct {
	path     string
	maxBytes int64
	backups  int
	mu       sync.Mutex
}

func newRotatingLog(path string, maxBytes int64, backups int) (*rotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	return &rotatingLog{path: path, maxBytes: maxBytes, backups: backups}, nil
}

func (log *rotatingLog) Write(line string) error {
	log.mu.Lock()
	defer log.mu.Unlock()
	payload := []byte(line + "\n")
	if info, err := os.Stat(log.path); err == nil && info.Size() > 0 && info.Size()+int64(len(payload)) > log.maxBytes {
		if err := log.rotate(); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(log.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(payload)
	}
	return errors.Join(err, file.Close())
}

func (log *rotatingLog) rotate() error {
	if log.backups == 0 {
		if err := os.Remove(log.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	for index := log.backups; index > 1; index-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", log.path, index-1), fmt.Sprintf("%s.%d", log.path, index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(log.path, log.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if len(line) > maxLine {
		return "", fmt.Errorf("management line is too large")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func removeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket %s", path)
	}
	return os.Remove(path)
}
