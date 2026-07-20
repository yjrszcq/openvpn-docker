package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"go.yaml.in/yaml/v3"
)

// Revision is the monotonic applied configuration revision within an instance.
type Revision uint64

// AppliedSnapshot is the last operator-confirmed normalized configuration.
type AppliedSnapshot struct {
	Revision Revision      `json:"revision"`
	Digest   string        `json:"digest"`
	Config   domain.Config `json:"-"`
}

// NewAppliedSnapshot creates a self-consistent applied configuration snapshot.
func NewAppliedSnapshot(revision Revision, value domain.Config) (AppliedSnapshot, error) {
	if revision == 0 {
		return AppliedSnapshot{}, fmt.Errorf("applied revision must be positive")
	}
	digest, err := Digest(value)
	if err != nil {
		return AppliedSnapshot{}, err
	}
	return AppliedSnapshot{Revision: revision, Digest: digest, Config: value}, nil
}

// Validate checks the revision and canonical digest of a loaded snapshot.
func (snapshot AppliedSnapshot) Validate() error {
	if snapshot.Revision == 0 {
		return fmt.Errorf("applied revision must be positive")
	}
	digest, err := Digest(snapshot.Config)
	if err != nil {
		return err
	}
	if snapshot.Digest != digest {
		return fmt.Errorf("applied configuration digest mismatch")
	}
	return nil
}

// CanonicalJSON returns the deterministic normalized representation used for
// comparison and hashing. It is independent of YAML formatting and defaults.
func CanonicalJSON(value domain.Config) ([]byte, error) {
	return json.Marshal(NewView(value))
}

// Digest returns a lowercase SHA-256 digest of CanonicalJSON.
func Digest(value domain.Config) (string, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical configuration: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// AppliedView is the stable query object for an applied snapshot.
type AppliedView struct {
	Revision Revision `json:"revision"`
	Digest   string   `json:"digest"`
	Config   View     `json:"config"`
}

// Show returns an applied configuration query view after integrity validation.
func Show(snapshot AppliedSnapshot) (AppliedView, error) {
	if err := snapshot.Validate(); err != nil {
		return AppliedView{}, err
	}
	return AppliedView{
		Revision: snapshot.Revision,
		Digest:   snapshot.Digest,
		Config:   NewView(snapshot.Config),
	}, nil
}

type exportConfig struct {
	Version int           `yaml:"version"`
	Server  exportServer  `yaml:"server"`
	IPv4    exportIPv4    `yaml:"ipv4"`
	Logging exportLogging `yaml:"logging"`
}

type exportServer struct {
	Endpoint       string          `yaml:"endpoint"`
	Transport      exportTransport `yaml:"transport"`
	ClientToClient bool            `yaml:"clientToClient"`
}

type exportTransport struct {
	Protocol string `yaml:"protocol"`
	Family   string `yaml:"family"`
	Port     uint16 `yaml:"port"`
}

type exportIPv4 struct {
	Network         string    `yaml:"network"`
	DynamicPoolSize uint64    `yaml:"dynamicPoolSize"`
	NAT             exportNAT `yaml:"nat"`
	RedirectGateway bool      `yaml:"redirectGateway"`
	DNS             []string  `yaml:"dns"`
	Routes          []string  `yaml:"routes"`
}

type exportNAT struct {
	Enabled   bool   `yaml:"enabled"`
	Interface string `yaml:"interface"`
}

type exportLogging struct {
	MaxBytes uint64 `yaml:"maxBytes"`
	Backups  uint32 `yaml:"backups"`
}

// ExportYAML renders a complete declarative YAML document from applied state.
func ExportYAML(snapshot AppliedSnapshot) ([]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	view := NewView(snapshot.Config)
	exported := exportConfig{
		Version: view.Version,
		Server: exportServer{
			Endpoint: view.Server.Endpoint,
			Transport: exportTransport{
				Protocol: view.Server.Protocol,
				Family:   view.Server.Family,
				Port:     view.Server.Port,
			},
			ClientToClient: view.Server.ClientToClient,
		},
		IPv4: exportIPv4{
			Network:         view.IPv4.Network,
			DynamicPoolSize: view.IPv4.DynamicPoolSize,
			NAT: exportNAT{
				Enabled:   view.IPv4.NATEnabled,
				Interface: view.IPv4.NATInterface,
			},
			RedirectGateway: view.IPv4.RedirectGateway,
			DNS:             view.IPv4.DNS,
			Routes:          view.IPv4.Routes,
		},
		Logging: exportLogging{
			MaxBytes: view.Logging.MaxBytes,
			Backups:  view.Logging.Backups,
		},
	}
	data, err := yaml.Marshal(exported)
	if err != nil {
		return nil, fmt.Errorf("encode applied configuration YAML: %w", err)
	}
	return data, nil
}

// Change is one canonical configuration field transition.
type Change struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// Impact describes required work outside the applied configuration row.
type Impact struct {
	RestartRequired       bool     `json:"restart_required"`
	AddressRemap          bool     `json:"address_remap"`
	FirewallReconcile     bool     `json:"firewall_reconcile"`
	ProfileRedistribution bool     `json:"profile_redistribution"`
	DerivedArtifacts      []string `json:"derived_artifacts"`
}

// Plan is the deterministic comparison of desired and applied configuration.
type Plan struct {
	Initial         bool     `json:"initial"`
	CurrentRevision Revision `json:"current_revision"`
	TargetRevision  Revision `json:"target_revision"`
	CurrentDigest   string   `json:"current_digest,omitempty"`
	DesiredDigest   string   `json:"desired_digest"`
	InSync          bool     `json:"in_sync"`
	Changes         []Change `json:"changes"`
	Impact          Impact   `json:"impact"`
}

type fieldValue struct {
	name  string
	value any
}

// BuildPlan compares desired normalized configuration with an optional applied
// snapshot. It performs no I/O and does not allocate client addresses.
func BuildPlan(applied *AppliedSnapshot, desired domain.Config) (Plan, error) {
	desiredDigest, err := Digest(desired)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Initial:        applied == nil,
		TargetRevision: 1,
		DesiredDigest:  desiredDigest,
		Changes:        make([]Change, 0),
		Impact:         Impact{DerivedArtifacts: make([]string, 0)},
	}
	var before []fieldValue
	if applied != nil {
		if err := applied.Validate(); err != nil {
			return Plan{}, err
		}
		plan.CurrentRevision = applied.Revision
		plan.TargetRevision = applied.Revision
		plan.CurrentDigest = applied.Digest
		before = configFields(applied.Config)
	}
	after := configFields(desired)
	for index, field := range after {
		var previous any
		if before != nil {
			previous = before[index].value
		}
		if before == nil || !reflect.DeepEqual(previous, field.value) {
			plan.Changes = append(plan.Changes, Change{Field: field.name, Before: previous, After: field.value})
			applyImpact(&plan.Impact, field.name)
		}
	}
	plan.InSync = len(plan.Changes) == 0
	if !plan.InSync {
		plan.Impact.RestartRequired = true
		if applied != nil {
			if applied.Revision == Revision(math.MaxUint64) {
				return Plan{}, fmt.Errorf("applied revision overflow")
			}
			plan.TargetRevision++
		}
	}
	return plan, nil
}

func configFields(value domain.Config) []fieldValue {
	view := NewView(value)
	return []fieldValue{
		{"server.endpoint", view.Server.Endpoint},
		{"server.transport.protocol", view.Server.Protocol},
		{"server.transport.family", view.Server.Family},
		{"server.transport.port", view.Server.Port},
		{"server.clientToClient", view.Server.ClientToClient},
		{"ipv4.network", view.IPv4.Network},
		{"ipv4.dynamicPoolSize", view.IPv4.DynamicPoolSize},
		{"ipv4.nat.enabled", view.IPv4.NATEnabled},
		{"ipv4.nat.interface", view.IPv4.NATInterface},
		{"ipv4.redirectGateway", view.IPv4.RedirectGateway},
		{"ipv4.dns", view.IPv4.DNS},
		{"ipv4.routes", view.IPv4.Routes},
		{"logging.maxBytes", view.Logging.MaxBytes},
		{"logging.backups", view.Logging.Backups},
	}
}

func applyImpact(impact *Impact, field string) {
	switch field {
	case "server.endpoint":
		impact.ProfileRedistribution = true
		addArtifact(impact, "client_profiles")
	case "server.transport.protocol", "server.transport.family", "server.transport.port":
		impact.ProfileRedistribution = true
		addArtifact(impact, "server_config")
		addArtifact(impact, "client_profiles")
	case "ipv4.network", "ipv4.dynamicPoolSize":
		impact.AddressRemap = true
		impact.FirewallReconcile = true
		addArtifact(impact, "server_config")
		addArtifact(impact, "ccd")
	case "ipv4.nat.enabled", "ipv4.nat.interface":
		impact.FirewallReconcile = true
	case "ipv4.redirectGateway", "ipv4.routes":
		impact.FirewallReconcile = true
		addArtifact(impact, "server_config")
	case "server.clientToClient", "ipv4.dns":
		addArtifact(impact, "server_config")
	}
}

func addArtifact(impact *Impact, artifact string) {
	for _, existing := range impact.DerivedArtifacts {
		if existing == artifact {
			return
		}
	}
	impact.DerivedArtifacts = append(impact.DerivedArtifacts, artifact)
}

// EqualCanonical reports whether two normalized configurations have identical
// canonical representations.
func EqualCanonical(left, right domain.Config) (bool, error) {
	leftJSON, err := CanonicalJSON(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := CanonicalJSON(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}
