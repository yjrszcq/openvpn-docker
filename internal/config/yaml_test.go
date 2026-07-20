package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const minimalConfig = `version: 1
server:
  endpoint: VPN.Example.COM
ipv4:
  network: 10.42.0.0/24
`

func TestMinimalConfigDefaultsAndNormalizes(t *testing.T) {
	value, err := config.Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatalf("parse minimal configuration: %v", err)
	}
	if value.Endpoint != "vpn.example.com" || value.Protocol != domain.ProtocolUDP || value.TransportFamily != domain.TransportAuto || value.Port != 1194 {
		t.Fatalf("unexpected server defaults: %+v", value)
	}
	if !value.ClientToClient || value.IPv4.DynamicPoolSize != 126 || value.IPv4.NATInterface != "auto" {
		t.Fatalf("unexpected IPv4 defaults: %+v", value.IPv4)
	}
	if value.IPv4.DNS == nil || value.IPv4.Routes == nil {
		t.Fatal("normalized empty lists must be non-nil")
	}
	if value.Logging.MaxBytes != 10485760 || value.Logging.Backups != 5 {
		t.Fatalf("unexpected logging defaults: %+v", value.Logging)
	}
}

func TestFullConfig(t *testing.T) {
	data := `version: 1
server:
  endpoint: 2001:db8::1
  transport:
    protocol: tcp
    family: ipv6
    port: 443
  clientToClient: false
ipv4:
  network: 10.42.0.0/30
  dynamicPoolSize: 1
  nat:
    enabled: true
    interface: eth0
  redirectGateway: true
  dns: [1.1.1.1, 8.8.8.8]
  routes: [192.168.0.0/16]
logging:
  maxBytes: 4096
  backups: 0
`
	value, err := config.Parse([]byte(data))
	if err != nil {
		t.Fatalf("parse full configuration: %v", err)
	}
	if value.Endpoint != "2001:db8::1" || value.Protocol != domain.ProtocolTCP || value.Port != 443 || value.ClientToClient {
		t.Fatalf("unexpected server configuration: %+v", value)
	}
	if value.IPv4.DynamicPoolSize != 1 || !value.IPv4.NATEnabled || value.IPv4.NATInterface != "eth0" || !value.IPv4.RedirectGateway {
		t.Fatalf("unexpected IPv4 configuration: %+v", value.IPv4)
	}
	if len(value.IPv4.DNS) != 2 || len(value.IPv4.Routes) != 1 || value.Logging.Backups != 0 {
		t.Fatalf("unexpected list/logging configuration: %+v", value)
	}
}

func TestStrictYAMLFailures(t *testing.T) {
	tests := map[string]string{
		"unknown field": `version: 1
server: {endpoint: vpn.example.test, surprise: true}
ipv4: {network: 10.42.0.0/24}
`,
		"duplicate field": `version: 1
server:
  endpoint: one.example.test
  endpoint: two.example.test
ipv4: {network: 10.42.0.0/24}
`,
		"multiple documents": minimalConfig + "---\nversion: 1\n",
		"wrong type": `version: 1
server: {endpoint: vpn.example.test}
ipv4: {network: 10.42.0.0/24, dynamicPoolSize: many}
`,
		"null defaulted field": `version: 1
server: {endpoint: vpn.example.test, transport: {protocol: null}}
ipv4: {network: 10.42.0.0/24}
`,
		"missing endpoint": `version: 1
ipv4: {network: 10.42.0.0/24}
`,
		"missing network": `version: 1
server: {endpoint: vpn.example.test}
`,
		"noncanonical network": `version: 1
server: {endpoint: vpn.example.test}
ipv4: {network: 10.42.0.1/24}
`,
		"IPv6 tunnel": `version: 1
server: {endpoint: vpn.example.test}
ipv4: {network: "2001:db8::/64"}
`,
		"pool overflow": `version: 1
server: {endpoint: vpn.example.test}
ipv4: {network: 10.42.0.0/30, dynamicPoolSize: 2}
`,
		"zero port": `version: 1
server: {endpoint: vpn.example.test, transport: {port: 0}}
ipv4: {network: 10.42.0.0/24}
`,
		"zero log size": `version: 1
server: {endpoint: vpn.example.test}
ipv4: {network: 10.42.0.0/24}
logging: {maxBytes: 0}
`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Parse([]byte(data)); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestBoundaryNetworks(t *testing.T) {
	for _, test := range []struct {
		network string
		pool    uint64
	}{
		{"10.42.0.0/30", 0},
		{"10.42.0.0/24", 126},
		{"0.0.0.0/0", 2147483646},
	} {
		data := strings.Replace(minimalConfig, "10.42.0.0/24", test.network, 1)
		value, err := config.Parse([]byte(data))
		if err != nil {
			t.Fatalf("parse %s: %v", test.network, err)
		}
		if value.IPv4.DynamicPoolSize != test.pool {
			t.Errorf("%s pool=%d, want %d", test.network, value.IPv4.DynamicPoolSize, test.pool)
		}
	}
}

func TestViewUsesStableJSONFields(t *testing.T) {
	value, err := config.Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config.NewView(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{`"dynamic_pool_size":126`, `"dns":[]`, `"max_bytes":10485760`} {
		if !strings.Contains(text, field) {
			t.Errorf("normalized JSON is missing %s: %s", field, text)
		}
	}
}
