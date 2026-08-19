package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
)

type capabilityRunner struct {
	version []byte
	help    []byte
	err     error
}

func (runner capabilityRunner) CombinedOutput(_ context.Context, _ string, args ...string) ([]byte, error) {
	if runner.err != nil {
		return nil, runner.err
	}
	if reflect.DeepEqual(args, []string{"--version"}) {
		return runner.version, nil
	}
	return runner.help, nil
}

func capabilitiesContractPath() string {
	return filepath.Join("..", "..", "compatibility", "contract.json")
}

func supportedOpenVPNVersion(t *testing.T) string {
	t.Helper()
	contract, err := compatibility.Load(capabilitiesContractPath())
	if err != nil {
		t.Fatal(err)
	}
	return contract.SupportedOpenVPNVersions[0]
}

func TestRuntimeCapabilitiesJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	version := supportedOpenVPNVersion(t)
	runner := capabilityRunner{
		version: []byte(fmt.Sprintf("OpenVPN %s test-build\n", version)),
		help:    []byte("--tls-crypt key\n--data-ciphers list\n--crl-verify crl\n--topology t: 'subnet'\n"),
	}
	code := runRuntimeCapabilitiesWith([]string{"--json"}, &stdout, &stderr, capabilitiesContractPath(), "openvpn", runner)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"topology_subnet":true`) {
		t.Fatalf("capabilities code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRuntimeCapabilitiesHumanOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	version := supportedOpenVPNVersion(t)
	runner := capabilityRunner{
		version: []byte(fmt.Sprintf("OpenVPN %s test-build\n", version)),
		help:    []byte("--tls-crypt key\n--data-ciphers list\n--crl-verify crl\n--topology t: 'subnet'\n"),
	}
	code := runRuntimeCapabilitiesWith(nil, &stdout, &stderr, capabilitiesContractPath(), "openvpn", runner)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "OpenVPN: "+version+" (verified)") || !strings.Contains(stdout.String(), "feature topology-subnet: true") {
		t.Fatalf("capabilities code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRuntimeCapabilitiesPolicyAndDependencyErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := capabilityRunner{version: []byte("OpenVPN 99.0.0 test-build\n")}
	code := runRuntimeCapabilitiesWith([]string{"--json"}, &stdout, &stderr, capabilitiesContractPath(), "openvpn", runner)
	if code != 78 || !strings.Contains(stdout.String(), `"supported_version":false`) || !strings.Contains(stderr.String(), `"kind":"unsupported_openvpn"`) {
		t.Fatalf("unsupported capabilities code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runRuntimeCapabilitiesWith([]string{"--json"}, &stdout, &stderr, capabilitiesContractPath(), "openvpn", capabilityRunner{err: errors.New("missing")})
	if code != 69 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"openvpn_unavailable"`) {
		t.Fatalf("unavailable capabilities code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
