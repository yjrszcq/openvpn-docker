// Package compatibility validates the image's OpenVPN runtime contract.
package compatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const DefaultContractPath = "/usr/local/share/openvpn-container/compatibility/contract.json"

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	featurePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Contract is strict, non-executable compatibility metadata shipped in the image.
type Contract struct {
	Version                  int       `json:"version"`
	SupportedOpenVPNVersions []string  `json:"supported_openvpn_versions"`
	Adapter                  Adapter   `json:"adapter"`
	RequiredFeatures         []Feature `json:"required_features"`
}

// Adapter describes the verified renderer and config-validation behavior.
type Adapter struct {
	Name             string `json:"name"`
	TemplateFamily   string `json:"template_family"`
	ConfigTestCipher string `json:"config_test_cipher"`
}

// Feature describes deterministic evidence expected in OpenVPN --help.
type Feature struct {
	Name         string   `json:"name"`
	HelpContains []string `json:"help_contains"`
}

// Load reads and validates a compatibility contract.
func Load(path string) (Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Contract{}, fmt.Errorf("read compatibility contract %s: %w", path, err)
	}
	contract, err := Parse(data)
	if err != nil {
		return Contract{}, fmt.Errorf("parse compatibility contract %s: %w", path, err)
	}
	return contract, nil
}

// Parse strictly decodes one compatibility contract document.
func Parse(data []byte) (Contract, error) {
	if err := rejectDuplicateKeys(data); err != nil {
		return Contract{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return Contract{}, fmt.Errorf("decode JSON: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Contract{}, err
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// Validate enforces ordering, identifier, and feature-probe invariants.
func (contract Contract) Validate() error {
	if contract.Version != 1 {
		return fmt.Errorf("contract version must be 1")
	}
	if len(contract.SupportedOpenVPNVersions) == 0 {
		return fmt.Errorf("supported_openvpn_versions must not be empty")
	}
	previous := [3]uint64{}
	for index, version := range contract.SupportedOpenVPNVersions {
		current, err := parseVersion(version)
		if err != nil {
			return fmt.Errorf("supported_openvpn_versions[%d]: %w", index, err)
		}
		if index > 0 && compareVersion(previous, current) >= 0 {
			return fmt.Errorf("supported_openvpn_versions must be unique and ascending")
		}
		previous = current
	}
	if !namePattern.MatchString(contract.Adapter.Name) {
		return fmt.Errorf("adapter.name is invalid")
	}
	if !namePattern.MatchString(contract.Adapter.TemplateFamily) {
		return fmt.Errorf("adapter.template_family is invalid")
	}
	if contract.Adapter.ConfigTestCipher == "" || strings.ContainsAny(contract.Adapter.ConfigTestCipher, "\r\n") {
		return fmt.Errorf("adapter.config_test_cipher is invalid")
	}
	if len(contract.RequiredFeatures) == 0 {
		return fmt.Errorf("required_features must not be empty")
	}
	seen := make(map[string]struct{}, len(contract.RequiredFeatures))
	for index, feature := range contract.RequiredFeatures {
		if !featurePattern.MatchString(feature.Name) {
			return fmt.Errorf("required_features[%d].name is invalid", index)
		}
		if _, exists := seen[feature.Name]; exists {
			return fmt.Errorf("required feature %q is duplicated", feature.Name)
		}
		seen[feature.Name] = struct{}{}
		if len(feature.HelpContains) == 0 {
			return fmt.Errorf("required feature %q has no probes", feature.Name)
		}
		for probeIndex, probe := range feature.HelpContains {
			if probe == "" || strings.ContainsAny(probe, "\r\n") {
				return fmt.Errorf("required feature %q probe %d is invalid", feature.Name, probeIndex)
			}
		}
	}
	return nil
}

// SupportsVersion reports whether version belongs to the exact verified set.
func (contract Contract) SupportsVersion(version string) bool {
	for _, supported := range contract.SupportedOpenVPNVersions {
		if version == supported {
			return true
		}
	}
	return false
}

func parseVersion(value string) ([3]uint64, error) {
	matches := versionPattern.FindStringSubmatch(value)
	if matches == nil {
		return [3]uint64{}, fmt.Errorf("version %q must be canonical major.minor.patch", value)
	}
	var parsed [3]uint64
	for index := range parsed {
		part, err := strconv.ParseUint(matches[index+1], 10, 64)
		if err != nil {
			return [3]uint64{}, fmt.Errorf("version %q: %w", value, err)
		}
		parsed[index] = part
	}
	return parsed, nil
}

func compareVersion(left, right [3]uint64) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return fmt.Errorf("compatibility contract must contain one JSON value")
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode JSON: %w", err)
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return fmt.Errorf("decode JSON object key: %w", keyErr)
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("JSON object key %q is duplicated", key)
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode JSON closing delimiter: %w", err)
		}
		return nil
	}
	if err := visit(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("compatibility contract must contain one JSON value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}
