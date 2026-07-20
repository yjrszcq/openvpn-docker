// Package cli implements dependency-free command dispatch for both binaries.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

// Run dispatches the ovpn multicall CLI and returns a stable exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeHelp(stdout, nil)
		return int(apperror.ExitSuccess)
	}
	if isHelp(args[0]) {
		if len(args) != 1 {
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn [command]"))
		}
		writeHelp(stdout, nil)
		return int(apperror.ExitSuccess)
	}
	if args[0] == "help" {
		path := args[1:]
		if _, found := findCommand(path); !found {
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "unknown help topic "+strings.Join(path, " ")))
		}
		writeHelp(stdout, path)
		return int(apperror.ExitSuccess)
	}
	if args[0] == "version" {
		return runVersion(args[1:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "config" && args[1] == "validate" {
		return runConfigValidate(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "capabilities" {
		return runRuntimeCapabilities(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "server" && args[1] == "init" {
		return runServerInit(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "list" {
		return runClientList(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "export" {
		return runClientExport(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "create" {
		return runClientCreate(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "rename" {
		return runClientRename(args[2:], stdout, stderr)
	}

	path := commandPath(args)
	if len(path) == 0 {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", fmt.Sprintf("unknown command %q", args[0])))
	}
	if len(path) < len(args) {
		if isHelp(args[len(path)]) {
			if len(args) != len(path)+1 {
				return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "help does not accept additional arguments"))
			}
			writeHelp(stdout, path)
			return int(apperror.ExitSuccess)
		}
		if !strings.HasPrefix(args[len(path)], "-") {
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "unknown command "+strings.Join(args[:len(path)+1], " ")))
		}
	}
	return writeError(stderr, apperror.New(apperror.ExitFailure, "not_implemented", strings.Join(path, " ")+" is not implemented in the foundation build"))
}

func runServerInit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn server init")
		return int(apperror.ExitSuccess)
	}
	if len(args) != 0 {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn server init"))
	}
	contractPath := environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath)
	contract, err := compatibility.Load(contractPath)
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err))
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "invalid_templates", "OpenVPN templates are invalid", err))
	}
	pkiRunner, err := runtimePKIRunner()
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitData, "invalid_runtime_path", "PKI runtime configuration is invalid", err))
	}
	service, err := initialize.NewService(pkiRunner, renderer)
	if err != nil {
		return writeError(stderr, err)
	}
	result, err := service.Initialize(context.Background(), initialize.Options{
		DataDir: environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir), RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir),
		ConfigFile: environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath), ServerName: initialize.DefaultServerName, Version: buildinfo.Current().Version,
	}, nil)
	if err != nil {
		switch {
		case errors.Is(err, initialize.ErrInvalidConfig):
			return writeError(stderr, apperror.Wrap(apperror.ExitData, "invalid_config", "initialization configuration is invalid", err))
		case errors.Is(err, pki.ErrUnavailable):
			return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "initialization dependency is unavailable", err))
		case errors.Is(err, artifact.ErrLocked):
			return writeError(stderr, apperror.Wrap(apperror.ExitTemporary, "lock_conflict", "initialization lock is unavailable", err))
		case errors.Is(err, initialize.ErrNotEmpty), errors.Is(err, initialize.ErrRecoveryNeeded), errors.Is(err, pki.ErrInvalidMaterial), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt):
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "initialization_refused", "initialization was refused by state or security policy", err))
		default:
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "initialization_failed", "initialize schema 4 instance", err))
		}
	}
	fmt.Fprintf(stdout, "initialized schema 4 instance %s at %s\n", result.InstanceID, environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir))
	return int(apperror.ExitSuccess)
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func runRuntimeCapabilities(args []string, stdout, stderr io.Writer) int {
	binary := os.Getenv("OVPN_OPENVPN_BIN")
	if binary == "" {
		binary = "openvpn"
	}
	return runRuntimeCapabilitiesWith(args, stdout, stderr, compatibility.DefaultContractPath, binary, compatibility.ExecRunner{})
}

func runRuntimeCapabilitiesWith(args []string, stdout, stderr io.Writer, contractPath, binary string, runner compatibility.CommandRunner) int {
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch arg {
		case "--json":
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "help does not accept additional arguments"), jsonMode)
			}
			fmt.Fprintln(stdout, "Usage: ovpn runtime capabilities [--json]")
			return int(apperror.ExitSuccess)
		default:
			return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn runtime capabilities [--json]"), jsonMode)
		}
	}
	contract, err := compatibility.Load(contractPath)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err), jsonMode)
	}
	capabilities, err := compatibility.Inspect(context.Background(), contract, binary, runner)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUnavailable, "openvpn_unavailable", "OpenVPN runtime is unavailable", err), jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(capabilities); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write capabilities output", err))
		}
	} else {
		status := "unsupported"
		if capabilities.SupportedVersion {
			status = "verified"
		}
		fmt.Fprintf(stdout, "OpenVPN: %s (%s)\n", capabilities.OpenVPNVersion, status)
		if capabilities.Adapter == nil {
			fmt.Fprintln(stdout, "adapter: unavailable")
		} else {
			fmt.Fprintf(stdout, "adapter: %s\ntemplate family: %s\n", *capabilities.Adapter, *capabilities.TemplateFamily)
		}
		for _, feature := range contract.RequiredFeatures {
			key := strings.ReplaceAll(feature.Name, "-", "_")
			fmt.Fprintf(stdout, "feature %s: %t\n", feature.Name, capabilities.Features[key])
		}
	}
	if !capabilities.Supported() {
		return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "unsupported_openvpn", "OpenVPN runtime does not satisfy the verified compatibility contract"), jsonMode)
	}
	return int(apperror.ExitSuccess)
}

func runConfigValidate(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch arg {
		case "--json":
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "help does not accept additional arguments"), jsonMode)
			}
			fmt.Fprintln(stdout, "Usage: ovpn config validate [--json]")
			return int(apperror.ExitSuccess)
		default:
			return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn config validate [--json]"), jsonMode)
		}
	}
	path := os.Getenv("OVPN_CONFIG_FILE")
	if path == "" {
		path = configservice.DefaultPath
	}
	value, err := configservice.LoadFile(path)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "invalid_config", "configuration is invalid: "+err.Error(), err), jsonMode)
	}
	if jsonMode {
		payload := struct {
			Valid  bool               `json:"valid"`
			Config configservice.View `json:"config"`
		}{Valid: true, Config: configservice.NewView(value)}
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write validation output", err))
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "configuration is valid: %s\n", path)
	return int(apperror.ExitSuccess)
}

func commandPath(args []string) []string {
	current := rootCommand
	path := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}
		next, found := current.child(arg)
		if !found {
			break
		}
		path = append(path, arg)
		current = next
	}
	return path
}

func isHelp(value string) bool { return value == "-h" || value == "--help" }

func runVersion(args []string, stdout, stderr io.Writer) int {
	short, jsonMode := false, false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch arg {
		case "--short":
			short = true
		case "--json":
			jsonMode = true
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "help does not accept additional arguments"), jsonMode)
			}
			fmt.Fprintln(stdout, "Usage: ovpn version [--short|--json]")
			return int(apperror.ExitSuccess)
		default:
			return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn version [--short|--json]"), jsonMode)
		}
	}
	if short && jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "--short and --json are mutually exclusive"), jsonMode)
	}
	info := buildinfo.Current()
	if short {
		fmt.Fprintln(stdout, info.Version)
		return int(apperror.ExitSuccess)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(info); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write version output", err))
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "ovpn %s\ndata schema: %d\ncommit: %s\nbuilt: %s\ngo: %s\nsqlite: %s\nyaml: %s\n", info.Version, info.DataSchema, info.Commit, info.BuildDate, info.GoVersion, info.Dependencies.SQLite, info.Dependencies.YAML)
	return int(apperror.ExitSuccess)
}

func writeError(stderr io.Writer, err error) int {
	return int(apperror.Write(stderr, err, false))
}

func writeErrorMode(stderr io.Writer, err error, jsonMode bool) int {
	return int(apperror.Write(stderr, err, jsonMode))
}

// RunBroker dispatches the independent broker process skeleton.
func RunBroker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && isHelp(args[0])) {
		fmt.Fprintln(stdout, "Usage: ovpn-broker [--help|--version]")
		return int(apperror.ExitSuccess)
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, buildinfo.Current().Version)
		return int(apperror.ExitSuccess)
	}
	return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn-broker [--help|--version]"))
}
