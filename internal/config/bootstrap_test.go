package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapRequested(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  map[string]string
		enabled bool
		wantErr bool
	}{
		{name: "unset", values: map[string]string{}},
		{name: "false", values: map[string]string{BootstrapFromEnvironmentVariable: "false"}},
		{name: "true", values: map[string]string{BootstrapFromEnvironmentVariable: "true"}, enabled: true},
		{name: "invalid", values: map[string]string{BootstrapFromEnvironmentVariable: "sometimes"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			enabled, err := BootstrapRequested(mapLookup(test.values))
			if enabled != test.enabled || (err != nil) != test.wantErr {
				t.Fatalf("enabled=%t err=%v", enabled, err)
			}
			if test.wantErr && !errors.Is(err, ErrBootstrapInput) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBootstrapConfigUsesYAMLDefaults(t *testing.T) {
	value, err := BootstrapConfig(mapLookup(map[string]string{
		"OVPN_BOOTSTRAP_ENDPOINT":     "VPN.EXAMPLE.COM",
		"OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if value.Endpoint != "vpn.example.com" || value.Protocol != "udp" || value.TransportFamily != "auto" || value.Port != 1194 || !value.ClientToClient {
		t.Fatalf("server defaults=%+v", value)
	}
	if value.IPv4.DynamicPoolSize != 126 || value.IPv4.NATEnabled || value.IPv4.NATInterface != "auto" || value.IPv4.RedirectGateway || len(value.IPv4.DNS) != 0 || len(value.IPv4.Routes) != 0 {
		t.Fatalf("IPv4 defaults=%+v", value.IPv4)
	}
	if value.Logging.MaxBytes != 10485760 || value.Logging.Backups != 5 {
		t.Fatalf("logging defaults=%+v", value.Logging)
	}
}

func TestBootstrapConfigParsesAllSupportedValues(t *testing.T) {
	value, err := BootstrapConfig(mapLookup(map[string]string{
		"OVPN_BOOTSTRAP_ENDPOINT":          "vpn.example.test",
		"OVPN_BOOTSTRAP_PROTOCOL":          "tcp",
		"OVPN_BOOTSTRAP_FAMILY":            "ipv6",
		"OVPN_BOOTSTRAP_PORT":              "443",
		"OVPN_BOOTSTRAP_CLIENT_TO_CLIENT":  "false",
		"OVPN_BOOTSTRAP_IPV4_NETWORK":      "10.60.0.0/24",
		"OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE": "0",
		"OVPN_BOOTSTRAP_NAT_ENABLED":       "true",
		"OVPN_BOOTSTRAP_NAT_INTERFACE":     "eth0",
		"OVPN_BOOTSTRAP_REDIRECT_GATEWAY":  "true",
		"OVPN_BOOTSTRAP_DNS":               "1.1.1.1, 8.8.8.8",
		"OVPN_BOOTSTRAP_ROUTES":            "192.168.10.0/24,192.168.20.0/24",
		"OVPN_BOOTSTRAP_LOG_MAX_BYTES":     "2048",
		"OVPN_BOOTSTRAP_LOG_BACKUPS":       "2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	view := NewView(value)
	if view.Server.Protocol != "tcp" || view.Server.Family != "ipv6" || view.Server.Port != 443 || view.Server.ClientToClient {
		t.Fatalf("server=%+v", view.Server)
	}
	if view.IPv4.DynamicPoolSize != 0 || !view.IPv4.NATEnabled || view.IPv4.NATInterface != "eth0" || !view.IPv4.RedirectGateway {
		t.Fatalf("IPv4=%+v", view.IPv4)
	}
	if len(view.IPv4.DNS) != 2 || view.IPv4.DNS[1] != "8.8.8.8" || len(view.IPv4.Routes) != 2 || view.Logging.MaxBytes != 2048 || view.Logging.Backups != 2 {
		t.Fatalf("view=%+v", view)
	}
}

func TestBootstrapConfigRejectsInvalidEnvironment(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{name: "missing endpoint", values: map[string]string{"OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24"}},
		{name: "missing network", values: map[string]string{"OVPN_BOOTSTRAP_ENDPOINT": "vpn.example.test"}},
		{name: "invalid boolean", values: bootstrapValues("OVPN_BOOTSTRAP_NAT_ENABLED", "maybe")},
		{name: "invalid port", values: bootstrapValues("OVPN_BOOTSTRAP_PORT", "70000")},
		{name: "empty list item", values: bootstrapValues("OVPN_BOOTSTRAP_DNS", "1.1.1.1,,8.8.8.8")},
		{name: "invalid route", values: bootstrapValues("OVPN_BOOTSTRAP_ROUTES", "192.168.1.1/24")},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := BootstrapConfig(mapLookup(test.values))
			if !errors.Is(err, ErrBootstrapInput) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEnsureBootstrapFileIsAtomicPrivateAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.yaml")
	values := map[string]string{
		"OVPN_BOOTSTRAP_ENDPOINT":     "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24",
	}
	created, err := EnsureBootstrapFile(path, mapLookup(values))
	if err != nil || !created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatal(err)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config.yaml.bootstrap-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary files=%v err=%v", matches, err)
	}
	created, err = EnsureBootstrapFile(path, mapLookup(values))
	if err != nil || created {
		t.Fatalf("idempotent created=%t err=%v", created, err)
	}
	values["OVPN_BOOTSTRAP_ENDPOINT"] = "other.example.test"
	if _, err := EnsureBootstrapFile(path, mapLookup(values)); !errors.Is(err, ErrBootstrapConflict) {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestEnsureBootstrapFileHandlesConcurrentInitializers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	lookup := mapLookup(map[string]string{
		"OVPN_BOOTSTRAP_ENDPOINT":     "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24",
	})
	type result struct {
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			created, err := EnsureBootstrapFile(path, lookup)
			results <- result{created: created, err: err}
		}()
	}
	close(start)
	createdCount := 0
	for range 2 {
		value := <-results
		if value.err != nil {
			t.Fatal(value.err)
		}
		if value.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d", createdCount)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatal(err)
	}
}

func mapLookup(values map[string]string) EnvironmentLookup {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

func bootstrapValues(name, value string) map[string]string {
	return map[string]string{
		"OVPN_BOOTSTRAP_ENDPOINT":     "vpn.example.test",
		"OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24",
		name:                          value,
	}
}
