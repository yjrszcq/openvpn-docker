package configuration_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/configuration"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	instanceID = "10000000-0000-4000-8000-000000000001"
	alphaID    = "20000000-0000-4000-8000-000000000001"
	betaID     = "20000000-0000-4000-8000-000000000002"
)

func TestNetworkPlanRemapsStaticHostBitsAndClearsDynamicNetworkState(t *testing.T) {
	instance := testInstance(t, testYAML("10.42.0.0/24", 64, "vpn.example.test"))
	clients := []storesqlite.ClientState{
		testClient(t, alphaID, "alpha", domain.ClientActive, "static", "10.42.0.10"),
		testClient(t, betaID, "beta", domain.ClientRevoked, "dynamic", ""),
	}
	desired := parseConfig(t, testYAML("10.43.0.0/24", 64, "vpn.example.test"))
	plan, err := configuration.BuildPlan(instance, clients, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AddressChanges) != 2 {
		t.Fatalf("address changes=%+v", plan.AddressChanges)
	}
	if got := *plan.AddressChanges[0].After.Address; got != "10.43.0.10" {
		t.Fatalf("remapped address=%s", got)
	}
	if plan.AddressChanges[1].After.Mode != "dynamic" || plan.AddressChanges[1].After.Address != nil {
		t.Fatalf("dynamic remap=%+v", plan.AddressChanges[1])
	}
	if !plan.Firewall.Reconcile || plan.Firewall.Before.Network != "10.42.0.0/24" || plan.Firewall.After.Network != "10.43.0.0/24" {
		t.Fatalf("firewall impact=%+v", plan.Firewall)
	}
	if len(plan.Artifacts) != 3 || plan.Artifacts[0].Key != "server/server.conf" || plan.Artifacts[1].Action != "regenerate" || plan.Artifacts[2].Action != "delete" {
		t.Fatalf("artifact impacts=%+v", plan.Artifacts)
	}
}

func TestRemapPreservesValidAddressBeforeAllocatingCollidingHostBits(t *testing.T) {
	instance := testInstance(t, testYAML("10.42.0.0/16", 64, "vpn.example.test"))
	clients := []storesqlite.ClientState{
		testClient(t, alphaID, "alpha", domain.ClientActive, "static", "10.42.1.10"),
		testClient(t, betaID, "beta", domain.ClientActive, "static", "10.43.1.10"),
	}
	desired := parseConfig(t, testYAML("10.43.0.0/16", 64, "vpn.example.test"))
	plan, err := configuration.BuildPlan(instance, clients, desired)
	if err != nil {
		t.Fatal(err)
	}
	addresses := map[string]string{}
	for _, change := range plan.AddressChanges {
		addresses[change.Client.Name] = *change.After.Address
	}
	if addresses["beta"] != "10.43.1.10" || addresses["alpha"] == "10.43.1.10" {
		t.Fatalf("valid target address was stolen: %v", addresses)
	}
}

func TestPlanRejectsPoolThatCannotRetainAssignments(t *testing.T) {
	instance := testInstance(t, testYAML("10.42.0.0/24", 64, "vpn.example.test"))
	dynamic := []storesqlite.ClientState{testClient(t, alphaID, "alpha", domain.ClientActive, "dynamic", "")}
	desired := parseConfig(t, testYAML("10.42.0.0/24", 0, "vpn.example.test"))
	if _, err := configuration.BuildPlan(instance, dynamic, desired); !errors.Is(err, configuration.ErrPlanConflict) {
		t.Fatalf("dynamic pool removal error=%v", err)
	}

	static := []storesqlite.ClientState{testClient(t, alphaID, "alpha", domain.ClientActive, "static", "10.42.0.2")}
	desired = parseConfig(t, testYAML("10.50.0.0/30", 1, "vpn.example.test"))
	if _, err := configuration.BuildPlan(instance, static, desired); !errors.Is(err, configuration.ErrPlanConflict) {
		t.Fatalf("static exhaustion error=%v", err)
	}
}

func TestEndpointPlanRegeneratesAllProfilesButRedistributesOnlyActive(t *testing.T) {
	instance := testInstance(t, testYAML("10.42.0.0/24", 64, "vpn.example.test"))
	clients := []storesqlite.ClientState{
		testClient(t, alphaID, "alpha", domain.ClientActive, "dynamic", ""),
		testClient(t, betaID, "beta", domain.ClientRevoked, "dynamic", ""),
	}
	desired := parseConfig(t, testYAML("10.42.0.0/24", 64, "new.example.test"))
	plan, err := configuration.BuildPlan(instance, clients, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Artifacts) != 2 || plan.Artifacts[0].Key != "clients/active/alpha.ovpn" || plan.Artifacts[1].Key != "clients/revoked/beta.ovpn" {
		t.Fatalf("profile artifacts=%+v", plan.Artifacts)
	}
	if len(plan.ProfileRedistribution) != 1 || plan.ProfileRedistribution[0].Name != "alpha" {
		t.Fatalf("redistribution=%+v", plan.ProfileRedistribution)
	}
}

func TestInSyncDetailedPlanUsesStableEmptyArrays(t *testing.T) {
	instance := testInstance(t, testYAML("10.42.0.0/24", 64, "vpn.example.test"))
	plan, err := configuration.BuildPlan(instance, nil, instance.Applied.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Configuration.InSync || plan.AddressChanges == nil || plan.Artifacts == nil || plan.ProfileRedistribution == nil || plan.Firewall.Before != nil {
		t.Fatalf("in-sync plan=%+v", plan)
	}
}

func testInstance(t *testing.T, yaml string) storesqlite.InstanceState {
	t.Helper()
	value := parseConfig(t, yaml)
	snapshot, err := configservice.NewAppliedSnapshot(3, value)
	if err != nil {
		t.Fatal(err)
	}
	return storesqlite.InstanceState{ID: instanceID, NetworkID: "30000000-0000-4000-8000-000000000001", Applied: snapshot}
}

func testClient(t *testing.T, id, name string, status domain.ClientStatus, mode, address string) storesqlite.ClientState {
	t.Helper()
	client, err := domain.NewClient(id, name, status)
	if err != nil {
		t.Fatal(err)
	}
	assignmentStatus := storesqlite.AssignmentActive
	if status == domain.ClientRevoked {
		assignmentStatus = storesqlite.AssignmentRetained
	}
	assignment := &storesqlite.AddressAssignment{ID: id, NetworkID: "30000000-0000-4000-8000-000000000001", Kind: mode, Status: assignmentStatus, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if address != "" {
		parsed, err := domain.ParseAddress(address)
		if err != nil {
			t.Fatal(err)
		}
		assignment.Address = &parsed
	}
	return storesqlite.ClientState{Client: client, Assignment: assignment}
}

func parseConfig(t *testing.T, yaml string) domain.Config {
	t.Helper()
	value, err := configservice.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testYAML(network string, dynamic uint64, endpoint string) string {
	return "version: 1\nserver:\n  endpoint: " + endpoint + "\nipv4:\n  network: " + network + "\n  dynamicPoolSize: " + fmt.Sprint(dynamic) + "\n"
}
