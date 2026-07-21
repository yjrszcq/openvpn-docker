package network

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func TestExecRunnerPreservesCommandExitStatus(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "exit 1")
	if !errors.Is(err, ErrUnavailable) || !commandAbsent(err) {
		t.Fatalf("exit status was not preserved: %v", err)
	}
}

const networkInstance = "80808080-8080-4808-8808-808080808080"

func TestReconcileCreatesIdempotentInstanceChainsAndNAT(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	config := networkConfig(t, true, true, nil)
	if err := reconciler.Reconcile(context.Background(), networkInstance, config); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), networkInstance, config); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(forward)
	if err != nil || strings.TrimSpace(string(value)) != "1" {
		t.Fatalf("forwarding=%q err=%v", value, err)
	}
	filter, nat, comment, _ := identities(networkInstance)
	if !runner.chains["filter/"+filter] || !runner.chains["nat/"+nat] {
		t.Fatalf("chains=%v", runner.chains)
	}
	if got := runner.countRule("filter", "FORWARD", jumpRule(comment+":forward", filter)); got != 1 {
		t.Fatalf("filter jump count=%d", got)
	}
	if got := runner.countRule("nat", "POSTROUTING", jumpRule(comment+":nat", nat)); got != 1 {
		t.Fatalf("NAT jump count=%d", got)
	}
	if len(runner.rules["filter/"+filter]) != 3 || len(runner.rules["nat/"+nat]) != 2 {
		t.Fatalf("instance rules=%v", runner.rules)
	}
	if len(runner.ipCalls) != 2 || !reflect.DeepEqual(runner.ipCalls[0], []string{"-4", "route", "show", "default"}) {
		t.Fatalf("ip calls=%v", runner.ipCalls)
	}
}

func TestCleanupRemovesOnlyOwnedRules(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	unrelated := encodeRule([]string{"-s", "192.0.2.0/24", "-j", "ACCEPT"})
	runner.rules["filter/FORWARD"] = append(runner.rules["filter/FORWARD"], unrelated)
	if err := reconciler.Reconcile(context.Background(), networkInstance, networkConfig(t, true, false, []string{"192.168.0.0/16"})); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Cleanup(context.Background(), networkInstance); err != nil {
		t.Fatal(err)
	}
	filter, nat, comment, _ := identities(networkInstance)
	if runner.chains["filter/"+filter] || runner.chains["nat/"+nat] {
		t.Fatalf("owned chains remain: %v", runner.chains)
	}
	if got := runner.rules["filter/FORWARD"]; len(got) != 1 || got[0] != unrelated {
		t.Fatalf("unrelated rule changed: %v", got)
	}
	if runner.countRule("filter", "FORWARD", jumpRule(comment+":forward", filter)) != 0 {
		t.Fatal("owned jump remains")
	}
}

func TestNoForwardingPolicyConvergesStaleRulesWithoutEnablingHost(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	if err := reconciler.Reconcile(context.Background(), networkInstance, networkConfig(t, true, false, nil)); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), networkInstance, networkConfig(t, false, false, nil)); err != nil {
		t.Fatal(err)
	}
	value, _ := os.ReadFile(forward)
	if strings.TrimSpace(string(value)) != "1" {
		t.Fatalf("reconciler must not disable host forwarding: %q", value)
	}
	filter, nat, _, _ := identities(networkInstance)
	if runner.chains["filter/"+filter] || runner.chains["nat/"+nat] {
		t.Fatalf("disabled policy left chains: %v", runner.chains)
	}
}

func TestAutoInterfaceFailureLeavesNoPartialChains(t *testing.T) {
	runner := newFakeRunner()
	runner.defaultRoute = "default via 192.0.2.1\n"
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	err := reconciler.Reconcile(context.Background(), networkInstance, networkConfig(t, true, false, nil))
	if err == nil || !strings.Contains(err.Error(), "egress interface") {
		t.Fatalf("interface error=%v", err)
	}
	filter, nat, _, _ := identities(networkInstance)
	if runner.chains["filter/"+filter] || runner.chains["nat/"+nat] {
		t.Fatalf("failed reconcile left chains: %v", runner.chains)
	}
}

func TestInstancesNeverRemoveEachOthersChains(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	other := "81818181-8181-4818-8818-818181818181"
	config := networkConfig(t, true, false, nil)
	if err := reconciler.Reconcile(context.Background(), networkInstance, config); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), other, config); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Cleanup(context.Background(), networkInstance); err != nil {
		t.Fatal(err)
	}
	otherFilter, otherNAT, otherComment, _ := identities(other)
	if !runner.chains["filter/"+otherFilter] || !runner.chains["nat/"+otherNAT] {
		t.Fatalf("other instance chains were removed: %v", runner.chains)
	}
	if runner.countRule("filter", "FORWARD", jumpRule(otherComment+":forward", otherFilter)) != 1 || runner.countRule("nat", "POSTROUTING", jumpRule(otherComment+":nat", otherNAT)) != 1 {
		t.Fatalf("other instance jumps changed: %v", runner.rules)
	}
}

func TestCleanupRefusesForeignSameNameChain(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	filter, _, _, _ := identities(networkInstance)
	key := "filter/" + filter
	runner.chains[key] = true
	foreign := encodeRule([]string{"-s", "198.51.100.0/24", "-j", "ACCEPT"})
	runner.rules[key] = []string{foreign}
	err := reconciler.Cleanup(context.Background(), networkInstance)
	if err == nil || !strings.Contains(err.Error(), "unowned network chain") {
		t.Fatalf("foreign chain error=%v", err)
	}
	if !runner.chains[key] || len(runner.rules[key]) != 1 || runner.rules[key][0] != foreign {
		t.Fatalf("foreign chain was modified: chains=%v rules=%v", runner.chains, runner.rules[key])
	}
}

func TestCleanupRecognizesLegacyOwnedJumpWithoutMarker(t *testing.T) {
	runner := newFakeRunner()
	forward := filepath.Join(t.TempDir(), "ip_forward")
	if err := os.WriteFile(forward, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconciler := testReconciler(t, runner, forward)
	filter, _, comment, _ := legacyIdentities(networkInstance)
	key := "filter/" + filter
	runner.chains[key] = true
	runner.rules[key] = []string{encodeRule([]string{"-s", "10.42.0.0/24", "-j", "ACCEPT"})}
	runner.rules["filter/FORWARD"] = []string{jumpRule(comment+":forward", filter)}
	if err := reconciler.Cleanup(context.Background(), networkInstance); err != nil {
		t.Fatal(err)
	}
	if runner.chains[key] || len(runner.rules["filter/FORWARD"]) != 0 {
		t.Fatalf("legacy owned state remains: chains=%v rules=%v", runner.chains, runner.rules)
	}
}

func testReconciler(t *testing.T, runner CommandRunner, forwarding string) *Reconciler {
	t.Helper()
	value, err := New(Config{IPBinary: "ip-test", IPTablesBinary: "iptables-test", ForwardingFile: forwarding, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func networkConfig(t *testing.T, nat, redirect bool, routes []string) domain.Config {
	t.Helper()
	routeYAML := "[]"
	if len(routes) > 0 {
		routeYAML = "[" + strings.Join(routes, ", ") + "]"
	}
	data := fmt.Sprintf("version: 1\nserver:\n  endpoint: vpn.example.test\nipv4:\n  network: 10.42.0.0/24\n  dynamicPoolSize: 64\n  nat:\n    enabled: %t\n    interface: auto\n  redirectGateway: %t\n  routes: %s\n", nat, redirect, routeYAML)
	value, err := configservice.Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type fakeExit int

func (status fakeExit) Error() string { return fmt.Sprintf("exit %d", status) }
func (status fakeExit) ExitCode() int { return int(status) }

type fakeRunner struct {
	defaultRoute string
	chains       map[string]bool
	rules        map[string][]string
	ipCalls      [][]string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{defaultRoute: "default via 192.0.2.1 dev eth0 proto static\n", chains: make(map[string]bool), rules: make(map[string][]string)}
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	if name == "ip-test" {
		runner.ipCalls = append(runner.ipCalls, append([]string(nil), args...))
		return runner.defaultRoute, nil
	}
	if name != "iptables-test" || len(args) < 5 || args[0] != "-w" || args[1] != "-t" {
		return "", fmt.Errorf("unexpected command %s %v", name, args)
	}
	table, operation, chain := args[2], args[3], args[4]
	key := table + "/" + chain
	switch operation {
	case "-S":
		if !runner.chains[key] {
			return "", fakeExit(1)
		}
	case "-N":
		if runner.chains[key] {
			return "", fakeExit(1)
		}
		runner.chains[key] = true
	case "-F":
		if !runner.chains[key] {
			return "", fakeExit(1)
		}
		runner.rules[key] = nil
	case "-X":
		if !runner.chains[key] {
			return "", fakeExit(1)
		}
		delete(runner.chains, key)
		delete(runner.rules, key)
	case "-A":
		runner.rules[key] = append(runner.rules[key], encodeRule(args[5:]))
	case "-I":
		if len(args) < 7 {
			return "", fmt.Errorf("invalid insert command")
		}
		position, err := strconv.Atoi(args[5])
		if err != nil || position < 1 {
			return "", fmt.Errorf("invalid insert position")
		}
		rule := encodeRule(args[6:])
		index := position - 1
		if index > len(runner.rules[key]) {
			index = len(runner.rules[key])
		}
		runner.rules[key] = append(runner.rules[key], "")
		copy(runner.rules[key][index+1:], runner.rules[key][index:])
		runner.rules[key][index] = rule
	case "-C":
		if runner.countRule(table, chain, encodeRule(args[5:])) == 0 {
			return "", fakeExit(1)
		}
	case "-D":
		rule := encodeRule(args[5:])
		for index, existing := range runner.rules[key] {
			if existing == rule {
				runner.rules[key] = append(runner.rules[key][:index], runner.rules[key][index+1:]...)
				return "", nil
			}
		}
		return "", fakeExit(1)
	default:
		return "", fmt.Errorf("unexpected iptables operation %s", operation)
	}
	return "", nil
}

func (runner *fakeRunner) countRule(table, chain, rule string) int {
	count := 0
	for _, existing := range runner.rules[table+"/"+chain] {
		if existing == rule {
			count++
		}
	}
	return count
}

func jumpRule(comment, chain string) string {
	return encodeRule([]string{"-m", "comment", "--comment", comment, "-j", chain})
}

func encodeRule(values []string) string { return strings.Join(values, "\x00") }
