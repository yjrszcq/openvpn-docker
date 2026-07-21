// Package cli implements dependency-free command dispatch for both binaries.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/broker"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/hook"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	networkcontrol "github.com/yjrszcq/openvpn-docker/internal/network"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	recoveryservice "github.com/yjrszcq/openvpn-docker/internal/recovery"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
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
	if len(args) >= 2 && args[0] == "config" && args[1] == "show" {
		return runConfigShow(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "config" && args[1] == "export" {
		return runConfigExport(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "config" && args[1] == "plan" {
		return runConfigPlan(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "config" && args[1] == "apply" {
		return runConfigApply(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "capabilities" {
		return runRuntimeCapabilities(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "status" {
		return runRuntimeStatus(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "health" {
		return runRuntimeHealth(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "logs" {
		return runRuntimeLogs(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "runtime" && args[1] == "events" {
		return runRuntimeEvents(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "state" && args[1] == "show" {
		return runState(args[2:], stdout, stderr, false)
	}
	if len(args) >= 2 && args[0] == "state" && args[1] == "doctor" {
		return runState(args[2:], stdout, stderr, true)
	}
	if len(args) >= 2 && args[0] == "repair" && args[1] == "plan" {
		return runRepairPlan(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "repair" && args[1] == "apply" {
		return runRepairApply(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "migrate" && args[1] == "plan" {
		return runMigrationPlan(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "migrate" && args[1] == "apply" {
		return runMigrationApply(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "server" && args[1] == "init" {
		return runServerInit(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "server" && args[1] == "run" {
		return runServerRun(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "server" && args[1] == "render" {
		return runServerRender(args[2:], stdout, stderr)
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
	if len(args) >= 2 && args[0] == "client" && args[1] == "revoke" {
		return runClientRevoke(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "reissue" {
		return runClientReissue(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "client" && args[1] == "delete" {
		return runClientDelete(args[2:], stdout, stderr)
	}
	if len(args) >= 3 && args[0] == "client" && args[1] == "address" && args[2] == "set" {
		return runClientAddressSet(args[3:], stdout, stderr)
	}
	if len(args) >= 3 && args[0] == "client" && args[1] == "address" && args[2] == "edit" {
		return runClientAddressEdit(args[3:], stdout, stderr)
	}
	if len(args) >= 3 && args[0] == "client" && args[1] == "address" && args[2] == "release" {
		return runClientAddressRelease(args[3:], stdout, stderr)
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

func runServerRun(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn server run")
		return int(apperror.ExitSuccess)
	}
	if len(args) != 0 {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn server run"))
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	if _, err := recoveryservice.RecoverOperations(context.Background(), dataDir); err != nil {
		if errors.Is(err, artifact.ErrLocked) || errors.Is(err, storesqlite.ErrBusy) {
			return writeError(stderr, apperror.Wrap(apperror.ExitTemporary, "operation_recovery_busy", "interrupted operation recovery is busy", err))
		}
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "operation_recovery_refused", "interrupted operation recovery was refused", err))
	}
	instance, err := runtimecontrol.LoadInstance(context.Background(), dataDir)
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "runtime_state_refused", "runtime state is invalid", err))
	}
	configPath := environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath)
	desired, configErr := configservice.LoadFile(configPath)
	if configErr != nil {
		fmt.Fprintf(stderr, "ovpn: warning: declarative configuration is unavailable or invalid; using applied revision %d\n", instance.Applied.Revision)
	} else if digest, digestErr := configservice.Digest(desired); digestErr != nil || digest != instance.Applied.Digest {
		fmt.Fprintf(stderr, "ovpn: warning: declarative configuration differs from applied revision %d; using the applied snapshot\n", instance.Applied.Revision)
	}
	openvpnBinary := environmentOr("OVPN_OPENVPN_BIN", "openvpn")
	brokerBinary := environmentOr("OVPN_BROKER_BIN", "ovpn-broker")
	for _, dependency := range []string{openvpnBinary, brokerBinary} {
		if _, err := exec.LookPath(dependency); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "runtime dependency is unavailable", err))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	supervisor := runtimecontrol.Supervisor{DataDir: dataDir, RuntimeDir: runtimeDir, OpenVPNBinary: openvpnBinary, BrokerBinary: brokerBinary}
	if err := supervisor.Run(ctx, hup, instance); err != nil {
		if errors.Is(err, artifact.ErrLocked) {
			return writeError(stderr, apperror.Wrap(apperror.ExitTemporary, "lock_conflict", "runtime lock is unavailable", err))
		}
		if errors.Is(err, networkcontrol.ErrUnavailable) {
			return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "network_unavailable", "IPv4 network dependency is unavailable", err))
		}
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "runtime_failed", "OpenVPN runtime failed", err))
	}
	return int(apperror.ExitSuccess)
}

// RunEntrypoint preserves direct OpenVPN/shell execution while making an empty
// container command start the Go runtime.
func RunEntrypoint(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
		empty, err := dataDirectoryEmpty(dataDir)
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "runtime_state_refused", "inspect runtime data directory", err))
		}
		if empty {
			if code := runServerInit(nil, stdout, stderr); code != int(apperror.ExitSuccess) {
				return code
			}
		}
		return Run([]string{"server", "run"}, stdout, stderr)
	}
	base := filepath.Base(args[0])
	if base == "ovpn" {
		return Run(args[1:], stdout, stderr)
	}
	if base == "openvpn" || base == "bash" || base == "sh" {
		binary, err := exec.LookPath(args[0])
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "entrypoint command is unavailable", err))
		}
		if err := syscall.Exec(binary, args, os.Environ()); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "entrypoint_failed", "execute entrypoint command", err))
		}
		return int(apperror.ExitSuccess)
	}
	return Run(args, stdout, stderr)
}

func dataDirectoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// RunHook dispatches the ovpn-hook multicall entrypoint.
func RunHook(args []string, stderr io.Writer) int {
	if len(args) < 1 || len(args) > 2 || args[0] != "pool-persist" {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn-hook pool-persist"))
	}
	input := hook.Input{
		ScriptType: os.Getenv("script_type"), ClientID: os.Getenv("common_name"), VirtualAddress: os.Getenv("ifconfig_pool_remote_ip"),
		RemoteAddress: os.Getenv("trusted_ip"), RemotePort: os.Getenv("trusted_port"), BytesReceived: os.Getenv("bytes_received"),
		BytesSent: os.Getenv("bytes_sent"), DurationSeconds: os.Getenv("time_duration"),
	}
	result, err := hook.Execute(context.Background(), environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir), input, time.Now().UTC())
	if err != nil {
		if errors.Is(err, hook.ErrInput) {
			return writeError(stderr, apperror.Wrap(apperror.ExitData, "invalid_hook_input", "OpenVPN hook input is invalid", err))
		}
		if errors.Is(err, storesqlite.ErrBusy) {
			return writeError(stderr, apperror.Wrap(apperror.ExitTemporary, "state_busy", "OpenVPN hook state is busy", err))
		}
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "hook_state_refused", "OpenVPN hook state is invalid", err))
	}
	if result.EventError != nil {
		fmt.Fprintf(stderr, "ovpn: warning: unable to append runtime event: %v\n", result.EventError)
	}
	return int(apperror.ExitSuccess)
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

// RunBroker dispatches the independent broker process.
func RunBroker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && isHelp(args[0])) {
		fmt.Fprintln(stdout, "Usage: ovpn-broker --listen PATH --backend PATH --raw-log PATH --max-bytes N --backups N [--timeout DURATION]")
		return int(apperror.ExitSuccess)
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, buildinfo.Current().Version)
		return int(apperror.ExitSuccess)
	}
	config := broker.Config{Timeout: 5 * time.Second}
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		name := args[index]
		if index+1 >= len(args) || seen[name] {
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "invalid or repeated broker option"))
		}
		seen[name] = true
		value := args[index+1]
		index++
		var err error
		switch name {
		case "--listen":
			config.Listen = value
		case "--backend":
			config.Backend = value
		case "--raw-log":
			config.RawLog = value
		case "--max-bytes":
			config.MaxBytes, err = strconv.ParseInt(value, 10, 64)
		case "--backups":
			config.Backups, err = strconv.Atoi(value)
		case "--timeout":
			config.Timeout, err = time.ParseDuration(value)
		default:
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "unknown broker option "+name))
		}
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitUsage, "usage", "invalid broker option "+name, err))
		}
	}
	service, err := broker.New(config)
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitData, "invalid_broker_config", "broker configuration is invalid", err))
	}
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	defer service.Close()
	if err := service.Serve(ctx); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "broker_failed", "management broker failed", err))
	}
	return int(apperror.ExitSuccess)
}
