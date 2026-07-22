package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrControlUnavailable = errors.New("runtime supervisor control is unavailable")
	ErrControlRejected    = errors.New("runtime supervisor control rejected the request")
)

const controlSocketName = "supervisor.sock"

type ApplySession struct {
	connection net.Conn
	reader     *bufio.Reader
}

func BeginApply(ctx context.Context, runtimeDir string) (*ApplySession, error) {
	path, err := controlSocketPath(runtimeDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrControlUnavailable, err)
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: connect supervisor: %v", ErrControlUnavailable, err)
	}
	session := &ApplySession{connection: connection, reader: bufio.NewReaderSize(connection, 1024)}
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if _, err := connection.Write([]byte("APPLY\n")); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("%w: request supervisor pause: %v", ErrControlUnavailable, err)
	}
	response, err := session.readResponse()
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if response != "READY" {
		_ = connection.Close()
		return nil, fmt.Errorf("%w: supervisor did not enter apply mode", ErrControlRejected)
	}
	_ = connection.SetDeadline(time.Time{})
	return session, nil
}

func (session *ApplySession) Resume(ctx context.Context) error {
	if session == nil || session.connection == nil {
		return fmt.Errorf("%w: apply session is closed", ErrControlUnavailable)
	}
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = session.connection.SetDeadline(deadline)
	if _, err := session.connection.Write([]byte("RESUME\n")); err != nil {
		_ = session.Close()
		return fmt.Errorf("%w: request supervisor restart: %v", ErrControlUnavailable, err)
	}
	response, err := session.readResponse()
	_ = session.Close()
	if err != nil {
		return err
	}
	if response != "OK" {
		return fmt.Errorf("%w: supervisor could not restart the runtime", ErrControlRejected)
	}
	return nil
}

func (session *ApplySession) Close() error {
	if session == nil || session.connection == nil {
		return nil
	}
	err := session.connection.Close()
	session.connection = nil
	return err
}

func (session *ApplySession) readResponse() (string, error) {
	line, err := session.reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("%w: read supervisor response: %v", ErrControlUnavailable, err)
	}
	if len(line) > 1024 {
		return "", fmt.Errorf("%w: supervisor response is too large", ErrControlRejected)
	}
	return strings.TrimSpace(line), nil
}

type applyControlRequest struct {
	ready  chan error
	resume chan bool
	done   chan error
}

type controlServer struct {
	listener net.Listener
	requests chan *applyControlRequest
	busy     chan struct{}
	path     string
	cancel   context.CancelFunc
	close    sync.Once
	closeErr error
}

func startControlServer(ctx context.Context, runtimeDir string) (*controlServer, error) {
	path, err := controlSocketPath(runtimeDir)
	if err != nil {
		return nil, err
	}
	if err := removeControlSocket(path); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on supervisor control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secure supervisor control socket: %w", err)
	}
	serverContext, cancel := context.WithCancel(ctx)
	server := &controlServer{listener: listener, requests: make(chan *applyControlRequest), busy: make(chan struct{}, 1), path: path, cancel: cancel}
	go server.serve(serverContext)
	return server, nil
}

func (server *controlServer) Close() error {
	if server == nil {
		return nil
	}
	server.close.Do(func() {
		server.cancel()
		server.closeErr = server.listener.Close()
		if removeErr := os.Remove(server.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			server.closeErr = errors.Join(server.closeErr, removeErr)
		}
	})
	return server.closeErr
}

func (server *controlServer) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(ctx, connection)
	}
}

func (server *controlServer) handle(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	handlerDone := make(chan struct{})
	defer close(handlerDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-handlerDone:
		}
	}()
	_ = connection.SetReadDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReaderSize(connection, 1024)
	command, err := reader.ReadString('\n')
	if err != nil || len(command) > 1024 || strings.TrimSpace(command) != "APPLY" {
		_, _ = connection.Write([]byte("ERROR\n"))
		return
	}
	select {
	case server.busy <- struct{}{}:
		defer func() { <-server.busy }()
	default:
		_, _ = connection.Write([]byte("ERROR\n"))
		return
	}
	request := &applyControlRequest{ready: make(chan error, 1), resume: make(chan bool, 1), done: make(chan error, 1)}
	select {
	case server.requests <- request:
	case <-ctx.Done():
		return
	}
	select {
	case err := <-request.ready:
		if err != nil {
			_, _ = connection.Write([]byte("ERROR\n"))
			return
		}
	case <-ctx.Done():
		return
	}
	_ = connection.SetDeadline(time.Time{})
	if _, err := connection.Write([]byte("READY\n")); err != nil {
		request.resume <- false
		return
	}
	command, err = reader.ReadString('\n')
	if err != nil || len(command) > 1024 || strings.TrimSpace(command) != "RESUME" {
		request.resume <- false
		return
	}
	request.resume <- true
	select {
	case err := <-request.done:
		if err != nil {
			_, _ = connection.Write([]byte("ERROR\n"))
			return
		}
		_, _ = connection.Write([]byte("OK\n"))
	case <-ctx.Done():
	}
}

func controlSocketPath(runtimeDir string) (string, error) {
	if runtimeDir == "" || !filepath.IsAbs(runtimeDir) || filepath.Clean(runtimeDir) != runtimeDir {
		return "", fmt.Errorf("runtime directory must be clean and absolute")
	}
	return filepath.Join(runtimeDir, controlSocketName), nil
}

func removeControlSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect supervisor control socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to replace non-socket supervisor control path")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale supervisor control socket: %w", err)
	}
	return nil
}
