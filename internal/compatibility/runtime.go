package compatibility

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var runtimeVersionPattern = regexp.MustCompile(`^OpenVPN[[:space:]]+([0-9]+\.[0-9]+\.[0-9]+)(?:[[:space:]]|$)`)

// Capabilities is the stable runtime compatibility query object.
type Capabilities struct {
	OpenVPNVersion    string          `json:"openvpn_version"`
	SupportedVersion  bool            `json:"supported_version"`
	SupportedVersions []string        `json:"supported_versions"`
	Adapter           *string         `json:"adapter"`
	TemplateFamily    *string         `json:"template_family"`
	Features          map[string]bool `json:"features"`
}

// Supported reports whether every version and feature requirement passed.
func (capabilities Capabilities) Supported() bool {
	if !capabilities.SupportedVersion ||
		capabilities.Adapter == nil ||
		capabilities.TemplateFamily == nil ||
		len(capabilities.Features) == 0 {
		return false
	}
	for _, supported := range capabilities.Features {
		if !supported {
			return false
		}
	}
	return true
}

// CommandRunner provides the narrow process boundary required by runtime probes.
type CommandRunner interface {
	CombinedOutput(ctx context.Context, binary string, args ...string) ([]byte, error)
}

// ExecRunner probes a real external executable.
type ExecRunner struct{}

// CombinedOutput runs one command without a shell.
func (ExecRunner) CombinedOutput(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, binary, args...).CombinedOutput()
}

// Inspect probes one OpenVPN executable against a validated contract.
func Inspect(ctx context.Context, contract Contract, binary string, runner CommandRunner) (Capabilities, error) {
	if err := contract.Validate(); err != nil {
		return Capabilities{}, fmt.Errorf("invalid compatibility contract: %w", err)
	}
	if binary == "" {
		return Capabilities{}, fmt.Errorf("OpenVPN executable path is empty")
	}
	versionOutput, err := runner.CombinedOutput(ctx, binary, "--version")
	if err != nil {
		return Capabilities{}, fmt.Errorf("run %s --version: %w", binary, err)
	}
	version, err := parseRuntimeVersion(versionOutput)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities := Capabilities{
		OpenVPNVersion:    version,
		SupportedVersion:  contract.SupportsVersion(version),
		SupportedVersions: append([]string(nil), contract.SupportedOpenVPNVersions...),
		Features:          make(map[string]bool, len(contract.RequiredFeatures)),
	}
	for _, feature := range contract.RequiredFeatures {
		capabilities.Features[featureKey(feature.Name)] = false
	}
	if !capabilities.SupportedVersion {
		return capabilities, nil
	}
	adapter := contract.Adapter.Name
	templateFamily := contract.Adapter.TemplateFamily
	capabilities.Adapter = &adapter
	capabilities.TemplateFamily = &templateFamily
	helpOutput, helpErr := runner.CombinedOutput(ctx, binary, "--help")
	if helpErr != nil && len(helpOutput) == 0 {
		return Capabilities{}, fmt.Errorf("run %s --help: %w", binary, helpErr)
	}
	help := string(helpOutput)
	for _, feature := range contract.RequiredFeatures {
		supported := true
		for _, evidence := range feature.HelpContains {
			if !strings.Contains(help, evidence) {
				supported = false
				break
			}
		}
		capabilities.Features[featureKey(feature.Name)] = supported
	}
	return capabilities, nil
}

func featureKey(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func parseRuntimeVersion(output []byte) (string, error) {
	firstLine := string(output)
	if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	matches := runtimeVersionPattern.FindStringSubmatch(strings.TrimSuffix(firstLine, "\r"))
	if matches == nil {
		return "", fmt.Errorf("unable to parse OpenVPN runtime version")
	}
	if _, err := parseVersion(matches[1]); err != nil {
		return "", fmt.Errorf("unable to parse OpenVPN runtime version: %w", err)
	}
	return matches[1], nil
}
