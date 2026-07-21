package runtime

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthAndStatusUseBrokerProtocol(t *testing.T) {
	healthSocket, healthCommand := brokerFixture(t, []string{"SUCCESS: broker connected to OpenVPN"})
	if err := Health(context.Background(), healthSocket); err != nil {
		t.Fatal(err)
	}
	if command := <-healthCommand; command != "broker-health" {
		t.Fatalf("health command=%q", command)
	}
	clientID := "11111111-1111-4111-8111-111111111111"
	statusSocket, statusCommand := brokerFixture(t, []string{
		"TITLE\tOpenVPN 2.7", "HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address", "CLIENT_LIST\t" + clientID + "\t192.0.2.10:44321\t10.42.0.200", "END",
	})
	status, err := QueryStatus(context.Background(), statusSocket, map[string]string{clientID: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if command := <-statusCommand; command != "status 3" {
		t.Fatalf("status command=%q", command)
	}
	if status.Version != 1 || status.Daemon != "running" || status.Management != "connected" || status.ClientCount != 1 || status.Clients[0].ClientName != "laptop" || status.Clients[0].VirtualAddress != "10.42.0.200" {
		t.Fatalf("status=%+v", status)
	}
}

func TestRequestRejectsBrokerError(t *testing.T) {
	socket, _ := brokerFixture(t, []string{"ERROR: management backend unavailable"})
	if _, err := Request(context.Background(), socket, "status 3"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("broker error=%v", err)
	}
}

func brokerFixture(t *testing.T, response []string) (string, <-chan string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan string, 1)
	go func() {
		defer listener.Close()
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = connection.Write([]byte(">INFO:OpenVPN Management Broker Version 1\n"))
		command, _ := bufio.NewReader(connection).ReadString('\n')
		commands <- strings.TrimSpace(command)
		for _, line := range response {
			_, _ = connection.Write([]byte(line + "\n"))
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(path) })
	return path, commands
}

func TestManagementRequestHonorsContextDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			time.Sleep(time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Request(ctx, path, "status 3"); err == nil {
		t.Fatal("request ignored its deadline")
	}
}
