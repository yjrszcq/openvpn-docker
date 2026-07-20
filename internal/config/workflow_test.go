package config_test

import (
	"math"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/config"
)

func mustConfig(t *testing.T, data string) config.AppliedSnapshot {
	t.Helper()
	value, err := config.Parse([]byte(data))
	if err != nil {
		t.Fatalf("parse configuration: %v", err)
	}
	snapshot, err := config.NewAppliedSnapshot(7, value)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	return snapshot
}

func TestDigestIsCanonicalAndStable(t *testing.T) {
	minimal := mustConfig(t, minimalConfig)
	explicit := mustConfig(t, `version: 1
server:
  endpoint: vpn.example.com
  transport: {protocol: udp, family: auto, port: 1194}
  clientToClient: true
ipv4:
  network: 10.42.0.0/24
  dynamicPoolSize: 126
  nat: {enabled: false, interface: auto}
  redirectGateway: false
  dns: []
  routes: []
logging: {maxBytes: 10485760, backups: 5}
`)
	if minimal.Digest != explicit.Digest {
		t.Fatalf("equivalent configs have different digests: %s != %s", minimal.Digest, explicit.Digest)
	}
	const expected = "0b70acc026a5b509e294178d07ab8d13bab3f9d24b5137b434ec7a43816c7d58"
	if minimal.Digest != expected {
		t.Fatalf("canonical digest changed: got %s, want %s", minimal.Digest, expected)
	}
}

func TestExportRoundTrip(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	data, err := config.ExportYAML(snapshot)
	if err != nil {
		t.Fatalf("export YAML: %v", err)
	}
	for _, field := range []string{"clientToClient: true", "dynamicPoolSize: 126", "dns: []", "maxBytes: 10485760"} {
		if !strings.Contains(string(data), field) {
			t.Errorf("export is missing %q:\n%s", field, data)
		}
	}
	parsed, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse exported YAML: %v", err)
	}
	equal, err := config.EqualCanonical(snapshot.Config, parsed)
	if err != nil || !equal {
		t.Fatalf("exported configuration changed semantics: equal=%t err=%v", equal, err)
	}
}

func TestShowRejectsDigestMismatch(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	view, err := config.Show(snapshot)
	if err != nil || view.Revision != 7 || view.Digest != snapshot.Digest || view.Config.IPv4.Network != "10.42.0.0/24" {
		t.Fatalf("unexpected applied view: view=%+v err=%v", view, err)
	}
	snapshot.Digest = strings.Repeat("0", 64)
	if _, err := config.Show(snapshot); err == nil {
		t.Fatal("corrupt applied digest was accepted")
	}
}

func TestInSyncPlanKeepsRevision(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	plan, err := config.BuildPlan(&snapshot, snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Initial || !plan.InSync || len(plan.Changes) != 0 || plan.TargetRevision != snapshot.Revision || plan.Impact.RestartRequired {
		t.Fatalf("unexpected in-sync plan: %+v", plan)
	}
	if plan.Impact.DerivedArtifacts == nil {
		t.Fatal("empty impact artifacts must be a stable empty array")
	}
}

func TestEndpointPlanRequiresProfileRedistribution(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	desired := snapshot.Config
	desired.Endpoint = "new.example.test"
	plan, err := config.BuildPlan(&snapshot, desired)
	if err != nil {
		t.Fatal(err)
	}
	if plan.InSync || plan.TargetRevision != 8 || len(plan.Changes) != 1 || plan.Changes[0].Field != "server.endpoint" {
		t.Fatalf("unexpected endpoint plan: %+v", plan)
	}
	if !plan.Impact.RestartRequired || !plan.Impact.ProfileRedistribution || plan.Impact.AddressRemap || plan.Impact.FirewallReconcile {
		t.Fatalf("unexpected endpoint impact: %+v", plan.Impact)
	}
	if strings.Join(plan.Impact.DerivedArtifacts, ",") != "client_profiles" {
		t.Fatalf("unexpected endpoint artifacts: %v", plan.Impact.DerivedArtifacts)
	}
}

func TestNetworkPlanMarksAddressAndFirewallImpact(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	desired := mustConfig(t, strings.Replace(minimalConfig, "10.42.0.0/24", "10.43.0.0/24", 1)).Config
	plan, err := config.BuildPlan(&snapshot, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Field != "ipv4.network" {
		t.Fatalf("unexpected network changes: %+v", plan.Changes)
	}
	if !plan.Impact.AddressRemap || !plan.Impact.FirewallReconcile || plan.Impact.ProfileRedistribution {
		t.Fatalf("unexpected network impact: %+v", plan.Impact)
	}
	if strings.Join(plan.Impact.DerivedArtifacts, ",") != "server_config,ccd" {
		t.Fatalf("unexpected network artifacts: %v", plan.Impact.DerivedArtifacts)
	}
}

func TestRoutePlanMarksServerAndForwardingImpact(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	desired := mustConfig(t, minimalConfig+"  routes: [192.168.0.0/16]\n").Config
	plan, err := config.BuildPlan(&snapshot, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Field != "ipv4.routes" || !plan.Impact.FirewallReconcile {
		t.Fatalf("unexpected route plan: %+v", plan)
	}
	if strings.Join(plan.Impact.DerivedArtifacts, ",") != "server_config" {
		t.Fatalf("unexpected route artifacts: %v", plan.Impact.DerivedArtifacts)
	}
}

func TestInitialPlanListsEveryField(t *testing.T) {
	desired := mustConfig(t, minimalConfig).Config
	plan, err := config.BuildPlan(nil, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Initial || plan.InSync || plan.CurrentRevision != 0 || plan.TargetRevision != 1 || len(plan.Changes) != 14 {
		t.Fatalf("unexpected initial plan: %+v", plan)
	}
	if !plan.Impact.RestartRequired || !plan.Impact.AddressRemap || !plan.Impact.FirewallReconcile || !plan.Impact.ProfileRedistribution {
		t.Fatalf("incomplete initial impact: %+v", plan.Impact)
	}
}

func TestPlanRejectsRevisionOverflow(t *testing.T) {
	snapshot := mustConfig(t, minimalConfig)
	snapshot.Revision = config.Revision(math.MaxUint64)
	desired := snapshot.Config
	desired.Endpoint = "new.example.test"
	if _, err := config.BuildPlan(&snapshot, desired); err == nil {
		t.Fatal("revision overflow was accepted")
	}
}
