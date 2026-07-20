package hook

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const hookConfig = `version: 1
server:
  endpoint: vpn.example.test
ipv4:
  network: 10.42.0.0/24
  dynamicPoolSize: 100
`

func TestExecuteRecordsDynamicLeaseAndStructuredEvents(t *testing.T) {
	dataDir, instanceID, clientID := hookFixture(t, "dynamic")
	now := time.Date(2026, 7, 20, 14, 15, 16, 0, time.UTC)
	input := Input{ScriptType: "client-connect", ClientID: clientID, VirtualAddress: "10.42.0.200", RemoteAddress: "192.0.2.10", RemotePort: "44321", BytesReceived: "12", BytesSent: "34", DurationSeconds: "5"}
	result, err := Execute(context.Background(), dataDir, input, now)
	if err != nil || result.EventError != nil {
		t.Fatalf("connect hook result=%+v err=%v", result, err)
	}
	database, err := storesqlite.Open(context.Background(), filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.LoadClient(context.Background(), instanceID, clientID)
	_ = database.Close()
	if err != nil || state.Lease == nil || state.Lease.Address.String() != "10.42.0.200" || !state.Lease.UpdatedAt.Equal(now) {
		t.Fatalf("lease=%+v err=%v", state.Lease, err)
	}
	input.ScriptType = "client-disconnect"
	input.BytesReceived = "120"
	if result, err = Execute(context.Background(), dataDir, input, now.Add(time.Minute)); err != nil || result.EventError != nil {
		t.Fatalf("disconnect hook result=%+v err=%v", result, err)
	}
	file, err := os.Open(filepath.Join(dataDir, "logs", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var events []event
	for scanner.Scan() {
		var value event
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		events = append(events, value)
	}
	if err := scanner.Err(); err != nil || len(events) != 2 || events[0].Operation != "connect" || events[1].Operation != "disconnect" || events[0].ClientName != "hook-client" || events[0].RemotePort == nil || *events[0].RemotePort != 44321 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	info, err := os.Stat(filepath.Join(dataDir, "logs", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("event mode=%v", info.Mode())
	}
}

func TestExecuteDoesNotRecordStaticLease(t *testing.T) {
	dataDir, instanceID, clientID := hookFixture(t, "static")
	result, err := Execute(context.Background(), dataDir, Input{ScriptType: "client-connect", ClientID: clientID, VirtualAddress: "10.42.0.10"}, time.Now().UTC())
	if err != nil || result.EventError != nil {
		t.Fatalf("static hook result=%+v err=%v", result, err)
	}
	database, err := storesqlite.Open(context.Background(), filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.LoadClient(context.Background(), instanceID, clientID)
	_ = database.Close()
	if err != nil || state.Lease != nil {
		t.Fatalf("static lease=%+v err=%v", state.Lease, err)
	}
}

func TestExecuteRejectsInvalidInputs(t *testing.T) {
	for _, input := range []Input{
		{ScriptType: "route-up", ClientID: "11111111-1111-4111-8111-111111111111"},
		{ScriptType: "client-connect", ClientID: "not-an-id"},
		{ScriptType: "client-disconnect", ClientID: "11111111-1111-4111-8111-111111111111", RemoteAddress: "bad\naddress"},
	} {
		if _, err := Execute(context.Background(), t.TempDir(), input, time.Now()); !errors.Is(err, ErrInput) {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
}

func TestAppendEventUsesWholeConcurrentJSONLines(t *testing.T) {
	dataDir := t.TempDir()
	const writers = 40
	errorsSeen := make(chan error, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsSeen <- appendEvent(dataDir, event{Version: 1, Timestamp: "2026-07-20T00:00:00Z", Event: "client_connection", Operation: "connect", Outcome: "applied", ClientID: "11111111-1111-4111-8111-111111111111", ClientName: "parallel"})
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(filepath.Join(dataDir, "logs", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var decoded map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			t.Fatalf("interleaved event line: %q: %v", scanner.Text(), err)
		}
		count++
	}
	if err := scanner.Err(); err != nil || count != writers {
		t.Fatalf("event count=%d err=%v", count, err)
	}
}

func hookFixture(t *testing.T, kind string) (string, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, "meta"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := storesqlite.Create(context.Background(), filepath.Join(dataDir, "meta", "state.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	configValue, err := configservice.Parse([]byte(hookConfig))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := configservice.NewAppliedSnapshot(1, configValue)
	if err != nil {
		t.Fatal(err)
	}
	instance := storesqlite.InstanceState{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CreatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Applied: applied}
	if err := database.CreateInstance(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	instance, err = database.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	assignment := storesqlite.AddressAssignment{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", NetworkID: instance.NetworkID, Kind: kind, Status: storesqlite.AssignmentActive, CreatedAt: now, UpdatedAt: now}
	if kind == "static" {
		value, err := domain.ParseAddress("10.42.0.10")
		if err != nil {
			t.Fatal(err)
		}
		assignment.Address = &value
	}
	clientID := "11111111-1111-4111-8111-111111111111"
	client := storesqlite.ClientState{Client: domain.Client{ID: clientID, Name: "hook-client", Status: domain.ClientActive}, CreatedAt: now, Assignment: &assignment}
	if err := database.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir, instance.ID, clientID
}
