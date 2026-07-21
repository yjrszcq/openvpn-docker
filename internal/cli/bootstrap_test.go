package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
)

func TestPrepareInitializationConfigCreatesAndReusesYAML(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ".ovpn-data.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "nested", "config.yaml")
	lookup := cliBootstrapLookup(map[string]string{
		configservice.BootstrapFromEnvironmentVariable: "true",
		"OVPN_BOOTSTRAP_ENDPOINT":                      "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK":                  "10.42.0.0/24",
	})
	var stdout, stderr bytes.Buffer
	if err := prepareInitializationConfig(dataDir, configFile, &stdout, &stderr, lookup); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), "generated initial declarative configuration") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := configservice.LoadFile(configFile); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := prepareInitializationConfig(dataDir, configFile, &stdout, &stderr, lookup); err != nil || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("idempotent err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestPrepareInitializationConfigRejectsConflictingYAML(t *testing.T) {
	dataDir := t.TempDir()
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	first := cliBootstrapLookup(map[string]string{
		configservice.BootstrapFromEnvironmentVariable: "true",
		"OVPN_BOOTSTRAP_ENDPOINT":                      "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK":                  "10.42.0.0/24",
	})
	if err := prepareInitializationConfig(dataDir, configFile, &bytes.Buffer{}, &bytes.Buffer{}, first); err != nil {
		t.Fatal(err)
	}
	conflicting := cliBootstrapLookup(map[string]string{
		configservice.BootstrapFromEnvironmentVariable: "true",
		"OVPN_BOOTSTRAP_ENDPOINT":                      "other.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK":                  "10.42.0.0/24",
	})
	if err := prepareInitializationConfig(dataDir, configFile, &bytes.Buffer{}, &bytes.Buffer{}, conflicting); !errors.Is(err, configservice.ErrBootstrapConflict) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareInitializationConfigIgnoresBootstrapForNonEmptyData(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "existing"), []byte("state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	lookup := cliBootstrapLookup(map[string]string{
		configservice.BootstrapFromEnvironmentVariable: "true",
		"OVPN_BOOTSTRAP_ENDPOINT":                      "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK":                  "10.42.0.0/24",
	})
	var stdout, stderr bytes.Buffer
	if err := prepareInitializationConfig(dataDir, configFile, &stdout, &stderr, lookup); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "bootstrap environment ignored") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(configFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration should not be created: %v", err)
	}

	stderr.Reset()
	invalid := cliBootstrapLookup(map[string]string{
		configservice.BootstrapFromEnvironmentVariable: "not-a-boolean",
	})
	if err := prepareInitializationConfig(dataDir, configFile, &stdout, &stderr, invalid); err != nil {
		t.Fatalf("initialized data should ignore invalid bootstrap environment: %v", err)
	}
	if !strings.Contains(stderr.String(), "bootstrap environment ignored") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestBootstrapEnvironmentActive(t *testing.T) {
	for _, test := range []struct {
		value  string
		active bool
	}{
		{value: ""},
		{value: "false"},
		{value: "true", active: true},
		{value: "invalid", active: true},
	} {
		values := map[string]string{}
		if test.value != "" {
			values[configservice.BootstrapFromEnvironmentVariable] = test.value
		}
		if active := bootstrapEnvironmentActive(cliBootstrapLookup(values)); active != test.active {
			t.Errorf("value=%q active=%t", test.value, active)
		}
	}
}

func cliBootstrapLookup(values map[string]string) configservice.EnvironmentLookup {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}
