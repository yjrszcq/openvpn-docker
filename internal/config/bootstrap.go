package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const BootstrapFromEnvironmentVariable = "OVPN_BOOTSTRAP_FROM_ENV"

var (
	ErrBootstrapInput    = errors.New("bootstrap environment is invalid")
	ErrBootstrapConflict = errors.New("bootstrap environment conflicts with declarative configuration")
	ErrBootstrapWrite    = errors.New("write bootstrap configuration")
)

type EnvironmentLookup func(string) (string, bool)

// BootstrapRequested reports whether one-time environment bootstrap is enabled.
func BootstrapRequested(lookup EnvironmentLookup) (bool, error) {
	value, found := lookup(BootstrapFromEnvironmentVariable)
	if !found || strings.TrimSpace(value) == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%w: %s must be a boolean", ErrBootstrapInput, BootstrapFromEnvironmentVariable)
	}
	return enabled, nil
}

// BootstrapConfig builds the normal domain configuration from bootstrap-only
// environment variables. It shares all defaults and validation with YAML v1.
func BootstrapConfig(lookup EnvironmentLookup) (domain.Config, error) {
	var config yamlConfig
	var err error
	config.Version = 1
	if config.Server.Endpoint, err = requiredBootstrapValue(lookup, "OVPN_BOOTSTRAP_ENDPOINT"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.Network, err = requiredBootstrapValue(lookup, "OVPN_BOOTSTRAP_IPV4_NETWORK"); err != nil {
		return domain.Config{}, err
	}
	if config.Server.Transport.Protocol, err = optionalString(lookup, "OVPN_BOOTSTRAP_PROTOCOL"); err != nil {
		return domain.Config{}, err
	}
	if config.Server.Transport.Family, err = optionalString(lookup, "OVPN_BOOTSTRAP_FAMILY"); err != nil {
		return domain.Config{}, err
	}
	if config.Server.Transport.Port, err = optionalUint16(lookup, "OVPN_BOOTSTRAP_PORT"); err != nil {
		return domain.Config{}, err
	}
	if config.Server.ClientToClient, err = optionalBool(lookup, "OVPN_BOOTSTRAP_CLIENT_TO_CLIENT"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.DynamicPoolSize, err = optionalUint64(lookup, "OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.NAT.Enabled, err = boolValue(lookup, "OVPN_BOOTSTRAP_NAT_ENABLED"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.NAT.Interface, err = optionalString(lookup, "OVPN_BOOTSTRAP_NAT_INTERFACE"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.RedirectGateway, err = boolValue(lookup, "OVPN_BOOTSTRAP_REDIRECT_GATEWAY"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.DNS, err = listValue(lookup, "OVPN_BOOTSTRAP_DNS"); err != nil {
		return domain.Config{}, err
	}
	if config.IPv4.Routes, err = listValue(lookup, "OVPN_BOOTSTRAP_ROUTES"); err != nil {
		return domain.Config{}, err
	}
	if config.Logging.MaxBytes, err = optionalUint64(lookup, "OVPN_BOOTSTRAP_LOG_MAX_BYTES"); err != nil {
		return domain.Config{}, err
	}
	if config.Logging.Backups, err = optionalUint32(lookup, "OVPN_BOOTSTRAP_LOG_BACKUPS"); err != nil {
		return domain.Config{}, err
	}
	value, err := normalize(config)
	if err != nil {
		return domain.Config{}, fmt.Errorf("%w: %v", ErrBootstrapInput, err)
	}
	return value, nil
}

// EnsureBootstrapFile creates a canonical YAML file or accepts an existing
// file only when it normalizes to the exact same configuration.
func EnsureBootstrapFile(path string, lookup EnvironmentLookup) (bool, error) {
	value, err := BootstrapConfig(lookup)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return false, compareBootstrapFile(path, value)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w: inspect %s: %v", ErrBootstrapWrite, path, err)
	}
	snapshot, err := NewAppliedSnapshot(1, value)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrBootstrapInput, err)
	}
	data, err := ExportYAML(snapshot)
	if err != nil {
		return false, fmt.Errorf("%w: encode YAML: %v", ErrBootstrapWrite, err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return false, fmt.Errorf("%w: create configuration directory: %v", ErrBootstrapWrite, err)
	}
	temporary, err := os.CreateTemp(directory, ".config.yaml.bootstrap-*")
	if err != nil {
		return false, fmt.Errorf("%w: create temporary file: %v", ErrBootstrapWrite, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return false, fmt.Errorf("%w: set temporary file mode: %v", ErrBootstrapWrite, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return false, fmt.Errorf("%w: write temporary file: %v", ErrBootstrapWrite, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, fmt.Errorf("%w: sync temporary file: %v", ErrBootstrapWrite, err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("%w: close temporary file: %v", ErrBootstrapWrite, err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, compareBootstrapFile(path, value)
		}
		return false, fmt.Errorf("%w: install %s: %v", ErrBootstrapWrite, path, err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return false, fmt.Errorf("%w: remove temporary file: %v", ErrBootstrapWrite, err)
	}
	if err := syncBootstrapDirectory(directory); err != nil {
		return false, fmt.Errorf("%w: %v", ErrBootstrapWrite, err)
	}
	if err := compareBootstrapFile(path, value); err != nil {
		return false, err
	}
	return true, nil
}

func requiredBootstrapValue(lookup EnvironmentLookup, name string) (string, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrBootstrapInput, name)
	}
	return value, nil
}

func optionalString(lookup EnvironmentLookup, name string) (*string, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return nil, nil
	}
	return &value, nil
}

func optionalBool(lookup EnvironmentLookup, name string) (*bool, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be a boolean", ErrBootstrapInput, name)
	}
	return &parsed, nil
}

func boolValue(lookup EnvironmentLookup, name string) (bool, error) {
	value, err := optionalBool(lookup, name)
	if err != nil || value == nil {
		return false, err
	}
	return *value, nil
}

func optionalUint16(lookup EnvironmentLookup, name string) (*uint16, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an unsigned 16-bit integer", ErrBootstrapInput, name)
	}
	result := uint16(parsed)
	return &result, nil
}

func optionalUint32(lookup EnvironmentLookup, name string) (*uint32, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an unsigned 32-bit integer", ErrBootstrapInput, name)
	}
	result := uint32(parsed)
	return &result, nil
}

func optionalUint64(lookup EnvironmentLookup, name string) (*uint64, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an unsigned integer", ErrBootstrapInput, name)
	}
	return &parsed, nil
}

func listValue(lookup EnvironmentLookup, name string) ([]string, error) {
	value, found := lookup(name)
	value = strings.TrimSpace(value)
	if !found || value == "" {
		return []string{}, nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%w: %s contains an empty list item", ErrBootstrapInput, name)
		}
		result = append(result, part)
	}
	return result, nil
}

func compareBootstrapFile(path string, expected domain.Config) error {
	current, err := LoadFile(path)
	if err != nil {
		return fmt.Errorf("%w: existing configuration is invalid: %v", ErrBootstrapConflict, err)
	}
	currentDigest, err := Digest(current)
	if err != nil {
		return fmt.Errorf("%w: digest existing configuration: %v", ErrBootstrapConflict, err)
	}
	expectedDigest, err := Digest(expected)
	if err != nil {
		return fmt.Errorf("%w: digest bootstrap configuration: %v", ErrBootstrapInput, err)
	}
	if currentDigest != expectedDigest {
		return ErrBootstrapConflict
	}
	return nil
}

func syncBootstrapDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open configuration directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync configuration directory: %w", err)
	}
	return nil
}
