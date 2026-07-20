package domain

// Protocol is the OpenVPN transport protocol.
type Protocol string

const (
	ProtocolUDP Protocol = "udp"
	ProtocolTCP Protocol = "tcp"
)

// TransportFamily controls only the public OpenVPN transport socket.
type TransportFamily string

const (
	TransportAuto TransportFamily = "auto"
	TransportIPv4 TransportFamily = "ipv4"
	TransportIPv6 TransportFamily = "ipv6"
)

// Config is the normalized, storage-independent desired/applied configuration.
// Parsing, defaults, and validation are implemented in Phase 2.1.
type Config struct {
	Endpoint        string
	Protocol        Protocol
	TransportFamily TransportFamily
	Port            uint16
	ClientToClient  bool
	IPv4            IPv4Config
	Logging         LoggingConfig
}

// IPv4Config describes the schema 4 tunnel data plane.
type IPv4Config struct {
	Network         Network
	DynamicPoolSize uint64
	NATEnabled      bool
	NATInterface    string
	RedirectGateway bool
	DNS             []Address
	Routes          []Network
}

// LoggingConfig controls persistent log rotation.
type LoggingConfig struct {
	MaxBytes uint64
	Backups  uint32
}
