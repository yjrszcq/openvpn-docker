// Package render generates OpenVPN artifacts from typed domain state.
package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
)

const DefaultTemplateRoot = "/usr/local/share/openvpn-container/templates"

// Paths are instance-specific absolute paths embedded in runtime artifacts.
type Paths struct {
	DataDir    string
	RuntimeDir string
}

// DefaultPaths returns the public schema 4 filesystem defaults.
func DefaultPaths() Paths {
	return Paths{DataDir: "/etc/openvpn", RuntimeDir: "/run/openvpn-container"}
}

// ClientMaterial contains verified public and private material for one profile.
type ClientMaterial struct {
	ID          string
	Name        string
	CACert      string
	Certificate string
	PrivateKey  string
	TLSCryptKey string
}

// Renderer uses the template family selected by immutable compatibility data.
type Renderer struct {
	templateDir string
}

// New constructs a renderer without probing the runtime executable.
func New(templateRoot string, contract compatibility.Contract) (Renderer, error) {
	if err := contract.Validate(); err != nil {
		return Renderer{}, fmt.Errorf("invalid compatibility contract: %w", err)
	}
	if templateRoot == "" {
		return Renderer{}, fmt.Errorf("template root is empty")
	}
	return Renderer{templateDir: filepath.Join(templateRoot, contract.Adapter.TemplateFamily)}, nil
}

// Server renders server.conf from normalized configuration.
func (renderer Renderer) Server(config domain.Config, paths Paths) ([]byte, error) {
	if err := validatePaths(paths); err != nil {
		return nil, err
	}
	layout, err := ipam.NewIPv4Layout(config.IPv4.Network, config.IPv4.DynamicPoolSize)
	if err != nil {
		return nil, fmt.Errorf("build IPv4 layout: %w", err)
	}
	serverProtocol, clientProtocol, bindDirective, err := transportDirectives(config)
	if err != nil {
		return nil, err
	}
	values := baseValues(config, paths, serverProtocol, clientProtocol, bindDirective)
	values["OVPN_NETWORK_ADDRESS"] = config.IPv4.Network.Prefix().Addr().String()
	values["OVPN_NETWORK_NETMASK"] = layout.Netmask.String()
	values["OVPN_CCD_DIR"] = filepath.Join(paths.DataDir, "ccd")
	values["OVPN_OPENVPN_MANAGEMENT_SOCKET"] = filepath.Join(paths.RuntimeDir, "openvpn-management.sock")
	if !layout.Dynamic.Empty() {
		values["OVPN_DYNAMIC_POOL_DIRECTIVE"] = "ifconfig-pool " + layout.Dynamic.First.String() + " " + layout.Dynamic.Last.String()
	}
	if config.ClientToClient {
		values["OVPN_CLIENT_TO_CLIENT_DIRECTIVE"] = "client-to-client"
	}
	if config.IPv4.RedirectGateway {
		values["OVPN_REDIRECT_GATEWAY_PUSH"] = `push "redirect-gateway def1"`
	}
	routes, err := routePushes(config.IPv4.Routes)
	if err != nil {
		return nil, err
	}
	dns, err := dnsPushes(config.IPv4.DNS)
	if err != nil {
		return nil, err
	}
	values["OVPN_ROUTE_PUSHES"] = routes
	values["OVPN_DNS_PUSHES"] = dns
	return renderer.execute("server.conf.tpl", values)
}

// Client renders a self-contained client profile from verified material.
func (renderer Renderer) Client(config domain.Config, paths Paths, material ClientMaterial) ([]byte, error) {
	if err := validatePaths(paths); err != nil {
		return nil, err
	}
	if _, err := ipam.NewIPv4Layout(config.IPv4.Network, config.IPv4.DynamicPoolSize); err != nil {
		return nil, fmt.Errorf("build IPv4 layout: %w", err)
	}
	if _, err := domain.NewClient(material.ID, material.Name, domain.ClientActive); err != nil {
		return nil, fmt.Errorf("invalid client identity: %w", err)
	}
	serverProtocol, clientProtocol, bindDirective, err := transportDirectives(config)
	if err != nil {
		return nil, err
	}
	values := baseValues(config, paths, serverProtocol, clientProtocol, bindDirective)
	values["CLIENT_ID"] = material.ID
	values["CLIENT_NAME"] = material.Name
	materials := []struct {
		key   string
		value string
	}{
		{"CA_CERT", material.CACert},
		{"CLIENT_CERT", material.Certificate},
		{"CLIENT_KEY", material.PrivateKey},
		{"TLS_CRYPT_KEY", material.TLSCryptKey},
	}
	for _, item := range materials {
		value := strings.TrimRight(item.value, "\r\n")
		if value == "" {
			return nil, fmt.Errorf("client material %s is empty", item.key)
		}
		values[item.key] = value
	}
	return renderer.execute("client.ovpn.tpl", values)
}

func baseValues(config domain.Config, paths Paths, serverProtocol, clientProtocol, bindDirective string) map[string]string {
	values := make(map[string]string, len(templateVariables))
	for _, name := range templateVariables {
		values[name] = ""
	}
	values["OVPN_PORT"] = strconv.FormatUint(uint64(config.Port), 10)
	values["OVPN_SERVER_PROTO"] = serverProtocol
	values["OVPN_BIND_DIRECTIVE"] = bindDirective
	values["OVPN_CLIENT_PROTO"] = clientProtocol
	values["OVPN_ENDPOINT"] = config.Endpoint
	values["OVPN_DATA_DIR"] = paths.DataDir
	return values
}

var templateVariables = []string{
	"OVPN_PORT", "OVPN_SERVER_PROTO", "OVPN_BIND_DIRECTIVE", "OVPN_CLIENT_PROTO",
	"OVPN_ENDPOINT", "OVPN_DATA_DIR", "OVPN_NETWORK_ADDRESS", "OVPN_NETWORK_NETMASK",
	"OVPN_CCD_DIR", "OVPN_DYNAMIC_POOL_DIRECTIVE", "OVPN_LEASE_DIR",
	"OVPN_MANAGEMENT_SOCKET", "OVPN_OPENVPN_MANAGEMENT_SOCKET",
	"OVPN_CLIENT_TO_CLIENT_DIRECTIVE", "OVPN_REDIRECT_GATEWAY_PUSH", "OVPN_ROUTE_PUSHES",
	"OVPN_DNS_PUSHES", "CA_CERT", "CLIENT_ID", "CLIENT_NAME", "CLIENT_CERT", "CLIENT_KEY",
	"TLS_CRYPT_KEY",
}

func (renderer Renderer) execute(name string, values map[string]string) ([]byte, error) {
	path := filepath.Join(renderer.templateDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", path, err)
	}
	functions := make(template.FuncMap, len(templateVariables))
	for _, name := range templateVariables {
		value := values[name]
		functions[name] = func() string { return value }
	}
	parsed, err := template.New(filepath.Base(path)).Funcs(functions).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", path, err)
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, nil); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", path, err)
	}
	return output.Bytes(), nil
}

func transportDirectives(config domain.Config) (server, client, bind string, err error) {
	family := config.TransportFamily
	if family == domain.TransportAuto {
		if address, parseErr := netip.ParseAddr(config.Endpoint); parseErr == nil && address.Is4() {
			family = domain.TransportIPv4
		} else if strings.Contains(config.Endpoint, ":") {
			family = domain.TransportIPv6
		}
	}
	switch family {
	case domain.TransportAuto:
		if config.Protocol == domain.ProtocolUDP {
			return "udp6", "udp", "", nil
		}
		if config.Protocol == domain.ProtocolTCP {
			return "tcp6-server", "tcp", "", nil
		}
	case domain.TransportIPv4:
		if config.Protocol == domain.ProtocolUDP {
			return "udp4", "udp4", "", nil
		}
		if config.Protocol == domain.ProtocolTCP {
			return "tcp4-server", "tcp4-client", "", nil
		}
	case domain.TransportIPv6:
		if config.Protocol == domain.ProtocolUDP {
			return "udp6", "udp6", "bind ipv6only", nil
		}
		if config.Protocol == domain.ProtocolTCP {
			return "tcp6-server", "tcp6-client", "bind ipv6only", nil
		}
	}
	return "", "", "", fmt.Errorf("unsupported transport family/protocol %q/%q", config.TransportFamily, config.Protocol)
}

func routePushes(routes []domain.Network) (string, error) {
	lines := make([]string, 0, len(routes))
	for index, route := range routes {
		if route.Family() != domain.FamilyIPv4 {
			return "", fmt.Errorf("route %d must use IPv4", index)
		}
		lines = append(lines, fmt.Sprintf(`push "route %s %s"`, route.Prefix().Addr(), ipv4Netmask(route.Prefix()).String()))
	}
	return strings.Join(lines, "\n"), nil
}

func dnsPushes(addresses []domain.Address) (string, error) {
	lines := make([]string, 0, len(addresses))
	for index, address := range addresses {
		if address.Family() != domain.FamilyIPv4 {
			return "", fmt.Errorf("DNS server %d must use IPv4", index)
		}
		lines = append(lines, fmt.Sprintf(`push "dhcp-option DNS %s"`, address))
	}
	return strings.Join(lines, "\n"), nil
}

func ipv4Netmask(prefix netip.Prefix) domain.Address {
	bits := prefix.Bits()
	var mask uint32
	if bits > 0 {
		mask = ^uint32(0) << (32 - bits)
	}
	var packed [4]byte
	binary.BigEndian.PutUint32(packed[:], mask)
	return domain.AddressFrom4(packed)
}

func validatePaths(paths Paths) error {
	values := []struct {
		name  string
		value string
	}{
		{"data directory", paths.DataDir},
		{"runtime directory", paths.RuntimeDir},
	}
	for _, item := range values {
		if item.value == "" || !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value || strings.ContainsAny(item.value, "\r\n") {
			return fmt.Errorf("%s must be a clean absolute path", item.name)
		}
	}
	return nil
}
