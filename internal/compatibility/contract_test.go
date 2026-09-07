package compatibility_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
)

func contractWithEmptyProbes(t *testing.T, data []byte) []byte {
	t.Helper()
	var contract compatibility.Contract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.RequiredFeatures) == 0 {
		t.Fatal("repository contract has no required features")
	}
	contract.RequiredFeatures[0].HelpContains = []string{}
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func repositoryContract(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func repositoryOpenVPNVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "versions.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if version, found := strings.CutPrefix(line, "OPENVPN_VERSION="); found {
			return version
		}
	}
	t.Fatal("versions.env does not define OPENVPN_VERSION")
	return ""
}

func TestRepositoryContract(t *testing.T) {
	contract, err := compatibility.Parse(repositoryContract(t))
	if err != nil {
		t.Fatal(err)
	}
	expected := repositoryOpenVPNVersion(t)
	if len(contract.SupportedOpenVPNVersions) != 1 || !contract.SupportsVersion(expected) {
		t.Fatalf("unexpected supported versions: %v", contract.SupportedOpenVPNVersions)
	}
	if contract.Adapter.Name != "openvpn-2.7" || contract.Adapter.TemplateFamily != "openvpn-2.7" {
		t.Fatalf("unexpected adapter: %+v", contract.Adapter)
	}
	if len(contract.RequiredFeatures) != 4 {
		t.Fatalf("required feature count=%d", len(contract.RequiredFeatures))
	}
}

func TestContractStrictness(t *testing.T) {
	validBytes := repositoryContract(t)
	valid := string(validBytes)
	supported := repositoryOpenVPNVersion(t)
	tests := map[string][]byte{
		"unknown-field":      []byte(strings.Replace(valid, `"version": 1,`, `"version": 1, "unknown": true,`, 1)),
		"duplicate-field":    []byte(strings.Replace(valid, `"version": 1,`, `"version": 1, "version": 1,`, 1)),
		"trailing-document":  []byte(valid + `{}`),
		"unordered-versions": []byte(strings.Replace(valid, `"`+supported+`"`, `"99.0.0", "`+supported+`"`, 1)),
		"duplicate-feature":  []byte(strings.Replace(valid, `"name": "data-ciphers"`, `"name": "tls-crypt"`, 1)),
		"empty-probes":       contractWithEmptyProbes(t, validBytes),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := compatibility.Parse(data); err == nil {
				t.Fatal("invalid compatibility contract was accepted")
			}
		})
	}
}

func TestContractRequiresCanonicalVersions(t *testing.T) {
	supported := repositoryOpenVPNVersion(t)
	data := strings.Replace(string(repositoryContract(t)), `"`+supported+`"`, `"0`+supported+`"`, 1)
	if _, err := compatibility.Parse([]byte(data)); err == nil {
		t.Fatal("noncanonical OpenVPN version was accepted")
	}
}

func TestVersionLookupUsesSemanticOrder(t *testing.T) {
	contract, err := compatibility.Parse(repositoryContract(t))
	if err != nil {
		t.Fatal(err)
	}
	contract.SupportedOpenVPNVersions = []string{"2.9.0", "2.10.0"}
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	if !contract.SupportsVersion("2.10.0") || contract.SupportsVersion("2.8.0") {
		t.Fatal("semantic version lookup returned an incorrect result")
	}
}
