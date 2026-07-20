// Package config parses and normalizes declarative OpenVPN configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"go.yaml.in/yaml/v3"
)

const DefaultPath = "/etc/openvpn-config/config.yaml"

const (
	defaultPort        = 1194
	defaultLogMaxBytes = 10 * 1024 * 1024
	defaultLogBackups  = 5
)

var (
	endpointPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
	interfacePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
	errMultipleDocs    = errors.New("configuration must contain exactly one YAML document")
	errMissingDocument = errors.New("configuration is empty")
)

type yamlConfig struct {
	Version int         `yaml:"version"`
	Server  yamlServer  `yaml:"server"`
	IPv4    yamlIPv4    `yaml:"ipv4"`
	Logging yamlLogging `yaml:"logging"`
}

type yamlServer struct {
	Endpoint       string        `yaml:"endpoint"`
	Transport      yamlTransport `yaml:"transport"`
	ClientToClient *bool         `yaml:"clientToClient"`
}

type yamlTransport struct {
	Protocol *string `yaml:"protocol"`
	Family   *string `yaml:"family"`
	Port     *uint16 `yaml:"port"`
}

type yamlIPv4 struct {
	Network         string   `yaml:"network"`
	DynamicPoolSize *uint64  `yaml:"dynamicPoolSize"`
	NAT             yamlNAT  `yaml:"nat"`
	RedirectGateway bool     `yaml:"redirectGateway"`
	DNS             []string `yaml:"dns"`
	Routes          []string `yaml:"routes"`
}

type yamlNAT struct {
	Enabled   bool    `yaml:"enabled"`
	Interface *string `yaml:"interface"`
}

type yamlLogging struct {
	MaxBytes *uint64 `yaml:"maxBytes"`
	Backups  *uint32 `yaml:"backups"`
}

// LoadFile parses one strict YAML v1 document from path.
func LoadFile(path string) (domain.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Config{}, fmt.Errorf("read configuration %s: %w", path, err)
	}
	return Parse(data)
}

// Parse strictly decodes, defaults, validates, and normalizes YAML v1.
func Parse(data []byte) (domain.Config, error) {
	if err := rejectNullValues(data); err != nil {
		return domain.Config{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var raw yamlConfig
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return domain.Config{}, errMissingDocument
		}
		return domain.Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var additional any
	if err := decoder.Decode(&additional); err == nil {
		return domain.Config{}, errMultipleDocs
	} else if !errors.Is(err, io.EOF) {
		return domain.Config{}, fmt.Errorf("decode trailing YAML document: %w", err)
	}
	return normalize(raw)
}

func rejectNullValues(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return errMissingDocument
		}
		return fmt.Errorf("decode configuration: %w", err)
	}
	var visit func(yaml.Node) error
	visit = func(node yaml.Node) error {
		if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
			return fmt.Errorf("configuration fields must not be null")
		}
		for _, child := range node.Content {
			if err := visit(*child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(document)
}

func normalize(raw yamlConfig) (domain.Config, error) {
	if raw.Version != 1 {
		return domain.Config{}, fmt.Errorf("version must be 1")
	}
	endpoint, err := normalizeEndpoint(raw.Server.Endpoint)
	if err != nil {
		return domain.Config{}, err
	}
	protocol := domain.ProtocolUDP
	if raw.Server.Transport.Protocol != nil {
		protocol = domain.Protocol(*raw.Server.Transport.Protocol)
	}
	if protocol != domain.ProtocolUDP && protocol != domain.ProtocolTCP {
		return domain.Config{}, fmt.Errorf("server.transport.protocol must be udp or tcp")
	}
	transportFamily := domain.TransportAuto
	if raw.Server.Transport.Family != nil {
		transportFamily = domain.TransportFamily(*raw.Server.Transport.Family)
	}
	switch transportFamily {
	case domain.TransportAuto, domain.TransportIPv4, domain.TransportIPv6:
	default:
		return domain.Config{}, fmt.Errorf("server.transport.family must be auto, ipv4, or ipv6")
	}
	port := uint16(defaultPort)
	if raw.Server.Transport.Port != nil {
		port = *raw.Server.Transport.Port
		if port == 0 {
			return domain.Config{}, fmt.Errorf("server.transport.port must be between 1 and 65535")
		}
	}
	clientToClient := true
	if raw.Server.ClientToClient != nil {
		clientToClient = *raw.Server.ClientToClient
	}

	network, err := domain.ParseNetwork(raw.IPv4.Network)
	if err != nil {
		return domain.Config{}, fmt.Errorf("ipv4.network: %w", err)
	}
	if network.Family() != domain.FamilyIPv4 {
		return domain.Config{}, fmt.Errorf("ipv4.network must use IPv4")
	}
	if network.Prefix().Bits() > 30 {
		return domain.Config{}, fmt.Errorf("ipv4.network must provide at least one client address")
	}
	capacity := (uint64(1) << (32 - network.Prefix().Bits())) - 3
	dynamicPoolSize := capacity / 2
	if raw.IPv4.DynamicPoolSize != nil {
		dynamicPoolSize = *raw.IPv4.DynamicPoolSize
	}
	if dynamicPoolSize > capacity {
		return domain.Config{}, fmt.Errorf("ipv4.dynamicPoolSize must be between 0 and %d", capacity)
	}
	natInterface := "auto"
	if raw.IPv4.NAT.Interface != nil {
		natInterface = *raw.IPv4.NAT.Interface
	}
	if natInterface != "auto" && !interfacePattern.MatchString(natInterface) {
		return domain.Config{}, fmt.Errorf("ipv4.nat.interface must be auto or a Linux interface name")
	}

	dns := make([]domain.Address, 0, len(raw.IPv4.DNS))
	for index, input := range raw.IPv4.DNS {
		address, parseErr := domain.ParseAddress(input)
		if parseErr != nil || address.Family() != domain.FamilyIPv4 {
			return domain.Config{}, fmt.Errorf("ipv4.dns[%d] must be an IPv4 address", index)
		}
		dns = append(dns, address)
	}
	routes := make([]domain.Network, 0, len(raw.IPv4.Routes))
	for index, input := range raw.IPv4.Routes {
		route, parseErr := domain.ParseNetwork(input)
		if parseErr != nil || route.Family() != domain.FamilyIPv4 {
			return domain.Config{}, fmt.Errorf("ipv4.routes[%d] must be a canonical IPv4 network", index)
		}
		routes = append(routes, route)
	}

	maxBytes := uint64(defaultLogMaxBytes)
	if raw.Logging.MaxBytes != nil {
		maxBytes = *raw.Logging.MaxBytes
	}
	if maxBytes == 0 {
		return domain.Config{}, fmt.Errorf("logging.maxBytes must be positive")
	}
	backups := uint32(defaultLogBackups)
	if raw.Logging.Backups != nil {
		backups = *raw.Logging.Backups
	}

	return domain.Config{
		Endpoint:        endpoint,
		Protocol:        protocol,
		TransportFamily: transportFamily,
		Port:            port,
		ClientToClient:  clientToClient,
		IPv4: domain.IPv4Config{
			Network:         network,
			DynamicPoolSize: dynamicPoolSize,
			NATEnabled:      raw.IPv4.NAT.Enabled,
			NATInterface:    natInterface,
			RedirectGateway: raw.IPv4.RedirectGateway,
			DNS:             dns,
			Routes:          routes,
		},
		Logging: domain.LoggingConfig{MaxBytes: maxBytes, Backups: backups},
	}, nil
}

func normalizeEndpoint(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("server.endpoint is required")
	}
	if strings.ContainsAny(input, "\r\n") {
		return "", fmt.Errorf("server.endpoint must be one line")
	}
	if address, err := netip.ParseAddr(input); err == nil {
		if address.Zone() != "" {
			return "", fmt.Errorf("server.endpoint must not contain an IPv6 zone")
		}
		return address.String(), nil
	}
	if !endpointPattern.MatchString(input) || strings.Contains(input, "..") {
		return "", fmt.Errorf("server.endpoint must be a hostname or IP address")
	}
	return strings.ToLower(input), nil
}

// View is the stable JSON-friendly normalized configuration shape.
type View struct {
	Version int `json:"version"`
	Server  struct {
		Endpoint       string `json:"endpoint"`
		Protocol       string `json:"protocol"`
		Family         string `json:"family"`
		Port           uint16 `json:"port"`
		ClientToClient bool   `json:"client_to_client"`
	} `json:"server"`
	IPv4 struct {
		Network         string   `json:"network"`
		DynamicPoolSize uint64   `json:"dynamic_pool_size"`
		NATEnabled      bool     `json:"nat_enabled"`
		NATInterface    string   `json:"nat_interface"`
		RedirectGateway bool     `json:"redirect_gateway"`
		DNS             []string `json:"dns"`
		Routes          []string `json:"routes"`
	} `json:"ipv4"`
	Logging struct {
		MaxBytes uint64 `json:"max_bytes"`
		Backups  uint32 `json:"backups"`
	} `json:"logging"`
}

// NewView converts normalized domain configuration to stable output fields.
func NewView(value domain.Config) View {
	var view View
	view.Version = 1
	view.Server.Endpoint = value.Endpoint
	view.Server.Protocol = string(value.Protocol)
	view.Server.Family = string(value.TransportFamily)
	view.Server.Port = value.Port
	view.Server.ClientToClient = value.ClientToClient
	view.IPv4.Network = value.IPv4.Network.String()
	view.IPv4.DynamicPoolSize = value.IPv4.DynamicPoolSize
	view.IPv4.NATEnabled = value.IPv4.NATEnabled
	view.IPv4.NATInterface = value.IPv4.NATInterface
	view.IPv4.RedirectGateway = value.IPv4.RedirectGateway
	view.IPv4.DNS = make([]string, len(value.IPv4.DNS))
	for index, address := range value.IPv4.DNS {
		view.IPv4.DNS[index] = address.String()
	}
	view.IPv4.Routes = make([]string, len(value.IPv4.Routes))
	for index, route := range value.IPv4.Routes {
		view.IPv4.Routes[index] = route.String()
	}
	view.Logging.MaxBytes = value.Logging.MaxBytes
	view.Logging.Backups = value.Logging.Backups
	return view
}
