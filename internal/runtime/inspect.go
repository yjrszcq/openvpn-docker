package runtime

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var ErrUnavailable = errors.New("runtime management is unavailable")

type Status struct {
	Version     int            `json:"version"`
	Daemon      string         `json:"daemon"`
	Management  string         `json:"management"`
	ClientCount int            `json:"client_count"`
	Clients     []StatusClient `json:"clients"`
}

type StatusClient struct {
	ClientID       string `json:"client_id"`
	ClientName     string `json:"client_name,omitempty"`
	RemoteAddress  string `json:"remote_address,omitempty"`
	VirtualAddress string `json:"virtual_address,omitempty"`
}

func Request(ctx context.Context, socket, command string) ([]string, error) {
	if socket == "" || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || command == "" || strings.ContainsAny(command, "\r\n") {
		return nil, fmt.Errorf("%w: invalid management request", ErrUnavailable)
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("%w: connect broker: %v", ErrUnavailable, err)
	}
	defer connection.Close()
	deadline := time.Now().Add(5 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	reader := bufio.NewReaderSize(connection, 64<<10)
	greeting, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(strings.TrimSpace(greeting), ">INFO:OpenVPN Management Broker") {
		return nil, fmt.Errorf("%w: invalid broker greeting", ErrUnavailable)
	}
	if _, err := io.WriteString(connection, command+"\n"); err != nil {
		return nil, fmt.Errorf("%w: write broker command: %v", ErrUnavailable, err)
	}
	lines := make([]string, 0)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("%w: read broker response: %v", ErrUnavailable, err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if len(line) > 64<<10 {
			return nil, fmt.Errorf("%w: management response line is too large", ErrUnavailable)
		}
		lines = append(lines, line)
		if line == "END" || strings.HasPrefix(line, "SUCCESS:") {
			return lines, nil
		}
		if strings.HasPrefix(line, "ERROR:") {
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, line)
		}
	}
}

func Health(ctx context.Context, socket string) error {
	response, err := Request(ctx, socket, "broker-health")
	if err != nil {
		return err
	}
	if len(response) != 1 || response[0] != "SUCCESS: broker connected to OpenVPN" {
		return fmt.Errorf("%w: broker health response is invalid", ErrUnavailable)
	}
	return nil
}

func QueryStatus(ctx context.Context, socket string, identities map[string]string) (Status, error) {
	response, err := Request(ctx, socket, "status 3")
	if err != nil {
		return Status{}, err
	}
	status := Status{Version: 1, Daemon: "running", Management: "connected", Clients: make([]StatusClient, 0)}
	var columns map[string]int
	for _, line := range response {
		reader := csv.NewReader(strings.NewReader(line))
		reader.Comma = '\t'
		record, err := reader.Read()
		if err != nil || len(record) < 2 {
			continue
		}
		switch record[0] {
		case "HEADER":
			if record[1] != "CLIENT_LIST" {
				continue
			}
			columns = make(map[string]int, len(record)-2)
			for index, name := range record[2:] {
				columns[name] = index + 1
			}
		case "CLIENT_LIST":
			if columns == nil {
				return Status{}, fmt.Errorf("%w: CLIENT_LIST appeared before its header", ErrUnavailable)
			}
			common, ok := csvField(record, columns, "Common Name")
			if !ok || common == "" {
				return Status{}, fmt.Errorf("%w: client status is missing common name", ErrUnavailable)
			}
			remote, _ := csvField(record, columns, "Real Address")
			virtual, _ := csvField(record, columns, "Virtual Address")
			status.Clients = append(status.Clients, StatusClient{ClientID: common, ClientName: identities[common], RemoteAddress: remote, VirtualAddress: virtual})
		}
	}
	status.ClientCount = len(status.Clients)
	return status, nil
}

func csvField(record []string, columns map[string]int, name string) (string, bool) {
	index, ok := columns[name]
	if !ok || index >= len(record) {
		return "", false
	}
	return record[index], true
}

func LoadIdentities(ctx context.Context, dataDir string) (map[string]string, error) {
	database, err := storesqlite.OpenRuntime(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return nil, err
	}
	defer database.Close()
	instance, err := database.LoadOnlyInstance(ctx)
	if err != nil {
		return nil, err
	}
	identities, err := database.ClientIdentities(ctx, instance.ID)
	if err != nil {
		return nil, err
	}
	return identities, nil
}

type IdentityResolver struct {
	dataDir  string
	modified time.Time
	size     int64
	values   map[string]string
}

func NewIdentityResolver(ctx context.Context, dataDir string) (*IdentityResolver, error) {
	resolver := &IdentityResolver{dataDir: dataDir}
	if err := resolver.refresh(ctx, true); err != nil {
		return nil, err
	}
	return resolver, nil
}

func (resolver *IdentityResolver) Translate(ctx context.Context, line string, fullID bool) (string, error) {
	if err := resolver.refresh(ctx, false); err != nil {
		return "", err
	}
	return TranslateIdentities(line, resolver.values, fullID), nil
}

func (resolver *IdentityResolver) refresh(ctx context.Context, force bool) error {
	path := filepath.Join(resolver.dataDir, "meta", "state.db")
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !force && info.ModTime() == resolver.modified && info.Size() == resolver.size {
		return nil
	}
	values, err := LoadIdentities(ctx, resolver.dataDir)
	if err != nil {
		return err
	}
	resolver.modified, resolver.size, resolver.values = info.ModTime(), info.Size(), values
	return nil
}

func SocketPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, "management.sock")
}
