package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/render"
)

const fullConfig = `version: 1
server:
  endpoint: vpn.example.test
  clientToClient: true
ipv4:
  network: 10.88.0.0/24
  redirectGateway: true
  dns: [1.1.1.1, 8.8.8.8]
  routes: [192.168.50.0/24]
`

func newRenderer(t *testing.T) render.Renderer {
	t.Helper()
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(filepath.Join("..", "..", "templates"), contract)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}

func parseConfig(t *testing.T, input string) domain.Config {
	t.Helper()
	config, err := configservice.Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func testPaths() render.Paths {
	return render.Paths{DataDir: "/test/openvpn", RuntimeDir: "/test/run"}
}

func TestServerGolden(t *testing.T) {
	output, err := newRenderer(t).Server(parseConfig(t, fullConfig), testPaths())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "server.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(want) {
		t.Fatalf("server render differs from golden\n--- got ---\n%s\n--- want ---\n%s", output, want)
	}
}

func TestClientGolden(t *testing.T) {
	output, err := newRenderer(t).Client(parseConfig(t, fullConfig), testPaths(), render.ClientMaterial{
		ID:          "11111111-1111-4111-8111-111111111111",
		Name:        "laptop",
		CACert:      "TEST CA CERT\n",
		Certificate: "TEST CLIENT CERT\n",
		PrivateKey:  "TEST CLIENT KEY\n",
		TLSCryptKey: "TEST TLS CRYPT KEY\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "client.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(want) {
		t.Fatalf("client render differs from golden\n--- got ---\n%s\n--- want ---\n%s", output, want)
	}
}

func TestTransportRenderMatrix(t *testing.T) {
	tests := []struct {
		family      string
		protocol    string
		endpoint    string
		serverProto string
		clientProto string
		bind        bool
	}{
		{"auto", "udp", "vpn.example.test", "udp6", "udp", false},
		{"auto", "tcp", "vpn.example.test", "tcp6-server", "tcp", false},
		{"auto", "udp", "198.51.100.10", "udp4", "udp4", false},
		{"auto", "tcp", "198.51.100.10", "tcp4-server", "tcp4-client", false},
		{"auto", "udp", "::1", "udp6", "udp6", true},
		{"auto", "tcp", "::ffff:192.0.2.1", "tcp6-server", "tcp6-client", true},
		{"ipv4", "udp", "vpn.example.test", "udp4", "udp4", false},
		{"ipv4", "tcp", "vpn.example.test", "tcp4-server", "tcp4-client", false},
		{"ipv6", "udp", "2001:db8::10", "udp6", "udp6", true},
		{"ipv6", "tcp", "vpn6.example.test", "tcp6-server", "tcp6-client", true},
	}
	for _, test := range tests {
		t.Run(test.family+"-"+test.protocol+"-"+test.endpoint, func(t *testing.T) {
			input := "version: 1\nserver:\n  endpoint: " + test.endpoint + "\n  transport: {family: " + test.family + ", protocol: " + test.protocol + "}\nipv4: {network: 10.88.0.0/24}\n"
			config := parseConfig(t, input)
			server, err := newRenderer(t).Server(config, testPaths())
			if err != nil {
				t.Fatal(err)
			}
			client, err := newRenderer(t).Client(config, testPaths(), validMaterial())
			if err != nil {
				t.Fatal(err)
			}
			if !hasLine(server, "proto "+test.serverProto) || !hasLine(client, "proto "+test.clientProto) {
				t.Fatalf("unexpected protocols\nserver:\n%s\nclient:\n%s", server, client)
			}
			if hasLine(server, "bind ipv6only") != test.bind {
				t.Fatalf("bind ipv6only=%t, want %t\n%s", hasLine(server, "bind ipv6only"), test.bind, server)
			}
		})
	}
}

func TestPureStaticRenderOmitsPool(t *testing.T) {
	config := parseConfig(t, "version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.88.0.0/24, dynamicPoolSize: 0}\n")
	output, err := newRenderer(t).Server(config, testPaths())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "ifconfig-pool ") {
		t.Fatalf("pure-static render contains dynamic pool:\n%s", output)
	}
}

func TestRendererRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	renderer := newRenderer(t)
	config := parseConfig(t, fullConfig)
	if _, err := renderer.Server(config, render.Paths{DataDir: "relative", RuntimeDir: "/run"}); err == nil {
		t.Fatal("relative data directory was accepted")
	}
	ipv6Route, err := domain.ParseNetwork("2001:db8::/64")
	if err != nil {
		t.Fatal(err)
	}
	config.IPv4.Routes = []domain.Network{ipv6Route}
	if _, err := renderer.Server(config, testPaths()); err == nil {
		t.Fatal("IPv6 route business state was accepted")
	}
	config = parseConfig(t, fullConfig)
	material := validMaterial()
	material.PrivateKey = ""
	if _, err := renderer.Client(config, testPaths(), material); err == nil {
		t.Fatal("empty private key was accepted")
	}
}

func TestRendererRejectsUnknownTemplateVariable(t *testing.T) {
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := filepath.Join(root, contract.Adapter.TemplateFamily)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.conf.tpl"), []byte("{{UNDECLARED_VALUE}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(root, contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderer.Server(parseConfig(t, fullConfig), testPaths()); err == nil {
		t.Fatal("unknown template variable was accepted")
	}
}

func validMaterial() render.ClientMaterial {
	return render.ClientMaterial{
		ID:          "11111111-1111-4111-8111-111111111111",
		Name:        "laptop",
		CACert:      "CA",
		Certificate: "CERT",
		PrivateKey:  "KEY",
		TLSCryptKey: "TLS",
	}
}

func hasLine(output []byte, want string) bool {
	for _, line := range strings.Split(string(output), "\n") {
		if line == want {
			return true
		}
	}
	return false
}
