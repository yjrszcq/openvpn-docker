package broker

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrokerProxySerializesCommandsWaitsForReloadAndLogs(t *testing.T) {
	root := t.TempDir()
	backendPath := filepath.Join(root, "backend.sock")
	listenPath := filepath.Join(root, "broker.sock")
	logPath := filepath.Join(root, "openvpn.log")
	fake := startFakeBackend(t, backendPath)
	service, err := New(Config{Listen: listenPath, Backend: backendPath, RawLog: logPath, MaxBytes: 160, Backups: 2, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx) }()
	waitForSocket(t, listenPath)

	if response := brokerCommand(t, listenPath, "broker-health"); !strings.HasPrefix(response, "SUCCESS: broker connected") {
		t.Fatalf("health response=%q", response)
	}
	started := time.Now()
	if response := brokerCommand(t, listenPath, "signal SIGHUP"); !strings.HasPrefix(response, "SUCCESS:") {
		t.Fatalf("reload response=%q", response)
	}
	if time.Since(started) < 35*time.Millisecond {
		t.Fatal("reload returned before initialization event")
	}

	var wait sync.WaitGroup
	errors := make(chan string, 12)
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			response := brokerCommand(t, listenPath, fmt.Sprintf("echo-%d", value))
			if !strings.HasPrefix(response, "SUCCESS:") {
				errors <- response
			}
		}(index)
	}
	wait.Wait()
	close(errors)
	for response := range errors {
		t.Fatalf("concurrent response=%q", response)
	}
	if fake.maxActive() != 1 {
		t.Fatalf("backend observed %d active commands", fake.maxActive())
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("rotated log is missing: %v", err)
	}
	info, err := os.Stat(logPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("raw log mode=%v err=%v", info.Mode().Perm(), err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker did not stop")
	}
	if _, err := os.Lstat(listenPath); !os.IsNotExist(err) {
		t.Fatalf("broker socket remains: %v", err)
	}
}

func TestBrokerRejectsUnsafeSocketReplacement(t *testing.T) {
	root := t.TempDir()
	listen := filepath.Join(root, "broker.sock")
	if err := os.WriteFile(listen, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{Listen: listen, Backend: filepath.Join(root, "backend.sock"), RawLog: filepath.Join(root, "log"), MaxBytes: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("unsafe replacement error=%v", err)
	}
}

type fakeBackend struct {
	listener net.Listener
	mu       sync.Mutex
	active   int
	maximum  int
}

func startFakeBackend(t *testing.T, path string) *fakeBackend {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeBackend{listener: listener}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go fake.serve(connection)
		}
	}()
	return fake
}

func (fake *fakeBackend) serve(connection net.Conn) {
	defer connection.Close()
	_, _ = fmt.Fprintln(connection, ">INFO:OpenVPN Management Interface")
	scanner := bufio.NewScanner(connection)
	for scanner.Scan() {
		command := scanner.Text()
		fake.mu.Lock()
		fake.active++
		if fake.active > fake.maximum {
			fake.maximum = fake.active
		}
		fake.mu.Unlock()
		_, _ = fmt.Fprintf(connection, ">LOG:%d,%s\n", time.Now().Unix(), command)
		switch command {
		case "log on all":
			_, _ = fmt.Fprintln(connection, "SUCCESS: real-time log notification set to ON")
		case "signal SIGHUP":
			_, _ = fmt.Fprintln(connection, "SUCCESS: signal SIGHUP thrown")
			time.Sleep(40 * time.Millisecond)
			_, _ = fmt.Fprintf(connection, ">LOG:%d,Initialization Sequence Completed\n", time.Now().Unix())
		case "status 3":
			_, _ = fmt.Fprintln(connection, "TITLE,OpenVPN 2.x")
			_, _ = fmt.Fprintln(connection, "END")
		default:
			time.Sleep(2 * time.Millisecond)
			_, _ = fmt.Fprintln(connection, "SUCCESS: "+command)
		}
		fake.mu.Lock()
		fake.active--
		fake.mu.Unlock()
	}
}

func (fake *fakeBackend) maxActive() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.maximum
}

func brokerCommand(t *testing.T, path, command string) string {
	t.Helper()
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(connection, command); err != nil {
		t.Fatal(err)
	}
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(response)
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("socket was not created")
}
