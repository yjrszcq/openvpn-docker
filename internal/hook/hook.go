// Package hook handles short-lived OpenVPN connection callbacks.
package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var ErrInput = errors.New("hook input is invalid")

type Input struct {
	ScriptType      string
	ClientID        string
	VirtualAddress  string
	RemoteAddress   string
	RemotePort      string
	BytesReceived   string
	BytesSent       string
	DurationSeconds string
}

type Result struct {
	EventError error
}

type event struct {
	Version         int     `json:"version"`
	Timestamp       string  `json:"timestamp"`
	Event           string  `json:"event"`
	Operation       string  `json:"operation"`
	Outcome         string  `json:"outcome"`
	ClientID        string  `json:"client_id"`
	ClientName      string  `json:"client_name"`
	VirtualAddress  *string `json:"virtual_ip"`
	RemoteAddress   *string `json:"remote_ip"`
	RemotePort      *uint16 `json:"remote_port"`
	BytesReceived   *uint64 `json:"bytes_received"`
	BytesSent       *uint64 `json:"bytes_sent"`
	DurationSeconds *uint64 `json:"duration_seconds"`
}

func Execute(ctx context.Context, dataDir string, input Input, now time.Time) (Result, error) {
	if input.ScriptType != "client-connect" && input.ScriptType != "client-disconnect" {
		return Result{}, fmt.Errorf("%w: unsupported script_type", ErrInput)
	}
	if !domain.ValidUUID(input.ClientID) {
		return Result{}, fmt.Errorf("%w: common_name must be a client UUID", ErrInput)
	}
	for _, field := range []struct{ name, value string }{
		{name: "ifconfig_pool_remote_ip", value: input.VirtualAddress},
		{name: "trusted_ip", value: input.RemoteAddress},
	} {
		if len(field.value) > 255 || strings.ContainsAny(field.value, "\r\n") {
			return Result{}, fmt.Errorf("%w: %s is invalid", ErrInput, field.name)
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	database, err := storesqlite.OpenRuntime(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return Result{}, err
	}
	defer database.Close()
	instance, err := database.LoadOnlyInstance(ctx)
	if err != nil {
		return Result{}, err
	}
	client, err := database.LoadClient(ctx, instance.ID, input.ClientID)
	if err != nil {
		return Result{}, err
	}
	record, err := buildEvent(input, client.Client.Name, now.UTC())
	if err != nil {
		return Result{}, err
	}
	if input.ScriptType == "client-connect" && input.VirtualAddress != "" {
		if client.Assignment == nil {
			return Result{}, fmt.Errorf("%w: client has no address assignment", ErrInput)
		}
		if client.Assignment.Kind == "dynamic" {
			address, err := domain.ParseAddress(input.VirtualAddress)
			if err != nil || address.Family() != domain.FamilyIPv4 {
				return Result{}, fmt.Errorf("%w: virtual address must be IPv4", ErrInput)
			}
			lease := storesqlite.ClientLease{NetworkID: client.Assignment.NetworkID, Address: address, UpdatedAt: now.UTC()}
			if err := database.RecordLease(ctx, input.ClientID, lease); err != nil {
				return Result{}, err
			}
		}
	}
	return Result{EventError: appendEvent(dataDir, record)}, nil
}

func buildEvent(input Input, clientName string, now time.Time) (event, error) {
	operation := strings.TrimPrefix(input.ScriptType, "client-")
	remotePort, err := optionalUint16(input.RemotePort)
	if err != nil {
		return event{}, err
	}
	bytesReceived, err := optionalUint64("bytes_received", input.BytesReceived)
	if err != nil {
		return event{}, err
	}
	bytesSent, err := optionalUint64("bytes_sent", input.BytesSent)
	if err != nil {
		return event{}, err
	}
	duration, err := optionalUint64("duration_seconds", input.DurationSeconds)
	if err != nil {
		return event{}, err
	}
	return event{
		Version: 1, Timestamp: now.Truncate(time.Second).Format(time.RFC3339), Event: "client_connection", Operation: operation, Outcome: "applied",
		ClientID: input.ClientID, ClientName: clientName, VirtualAddress: optionalString(input.VirtualAddress), RemoteAddress: optionalString(input.RemoteAddress),
		RemotePort: remotePort, BytesReceived: bytesReceived, BytesSent: bytesSent, DurationSeconds: duration,
	}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func optionalUint16(value string) (*uint16, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("%w: remote_port is invalid", ErrInput)
	}
	result := uint16(parsed)
	return &result, nil
}

func optionalUint64(name, value string) (*uint64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is invalid", ErrInput, name)
	}
	return &parsed, nil
}

func appendEvent(dataDir string, record event) error {
	if _, err := artifact.NewLocal(dataDir); err != nil {
		return err
	}
	logDir := filepath.Join(dataDir, "logs")
	if err := os.Mkdir(logDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create event directory: %w", err)
	}
	info, err := os.Lstat(logDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("event directory is unsafe")
	}
	path := filepath.Join(logDir, "events.jsonl")
	descriptor, err := syscall.Open(path, syscall.O_APPEND|syscall.O_CREAT|syscall.O_WRONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("set event log permissions: %w", err)
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("event log is unsafe")
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	line = append(line, '\n')
	written, err := file.Write(line)
	if err == nil && written != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("append event: wrote %d of %d bytes: %w", written, len(line), err)
	}
	return nil
}
