package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
)

func runRuntimeStatus(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	jsonMode, fullID := false, false
	for _, arg := range args {
		switch canonicalOption(arg) {
		case "--json":
			if jsonMode {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true)
			}
			jsonMode = true
		case "--full-id":
			if fullID {
				return writeErrorMode(stderr, usageError("--full-id may only be specified once"), jsonMode)
			}
			fullID = true
		default:
			return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn runtime status [--json] [--full-id]"), jsonRequested)
		}
	}
	if jsonMode && fullID {
		return writeErrorMode(stderr, usageError("--full-id only affects human output and cannot be combined with --json"), true)
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	identities, err := runtimecontrol.LoadIdentities(context.Background(), dataDir)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "runtime_state_refused", "runtime state is invalid", err), jsonMode)
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	status, err := runtimecontrol.QueryStatus(context.Background(), runtimecontrol.SocketPath(runtimeDir), identities)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUnavailable, "runtime_unavailable", "OpenVPN runtime is unavailable", err), jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(status); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write runtime status", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "daemon: %s\nmanagement: %s\nclients: %d\n", status.Daemon, status.Management, status.ClientCount)
	for _, client := range status.Clients {
		identity := displayClientID(client.ClientID, fullID)
		if client.ClientName != "" {
			identity = fmt.Sprintf("%s [%s]", client.ClientName, displayClientID(client.ClientID, fullID))
		}
		fmt.Fprintf(stdout, "- %s virtual=%s remote=%s\n", identity, client.VirtualAddress, client.RemoteAddress)
	}
	return int(apperror.ExitSuccess)
}

func runRuntimeDisconnect(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	selector, positionals, selectorErr := parseMutationSelector(args)
	if optionErr != nil || selectorErr != nil || len(positionals) != 0 {
		return writeErrorMode(stderr, usageError("usage: ovpn runtime disconnect (NAME|--name NAME|--id ID) [--json] [--full-id]"), jsonRequested)
	}
	if options.JSON && options.FullID {
		return writeErrorMode(stderr, usageError("--full-id only affects human output and cannot be combined with --json"), true)
	}
	service, state, err := openClientService(context.Background())
	if err != nil {
		return writeClientError(stderr, err, options.JSON)
	}
	identity, err := service.ResolveIdentity(context.Background(), selector)
	closeErr := state.Close()
	if err != nil {
		return writeClientError(stderr, err, options.JSON)
	}
	if closeErr != nil {
		return writeClientError(stderr, closeErr, options.JSON)
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	result, err := runtimecontrol.Disconnect(context.Background(), runtimecontrol.SocketPath(runtimeDir), identity.ID, identity.Name)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUnavailable, "runtime_unavailable", "OpenVPN runtime is unavailable", err), options.JSON)
	}
	if options.JSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write runtime disconnect result", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	identityText := fmt.Sprintf("%s [%s]", identity.Name, displayClientID(identity.ID, options.FullID))
	if !result.WasConnected {
		fmt.Fprintf(stdout, "client %s is not connected\n", identityText)
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "disconnected client %s (%d connection(s))\n", identityText, result.Connections)
	return int(apperror.ExitSuccess)
}

func runRuntimeHealth(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn runtime health"))
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	if err := runtimecontrol.Health(context.Background(), runtimecontrol.SocketPath(runtimeDir)); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "runtime_unhealthy", "OpenVPN runtime is unhealthy", err))
	}
	fmt.Fprintln(stdout, "healthy")
	return int(apperror.ExitSuccess)
}

type streamCLIOptions struct {
	lines  int
	follow bool
	raw    bool
	json   bool
	fullID bool
}

func parseStreamOptions(args []string, events bool) (streamCLIOptions, error) {
	options := streamCLIOptions{lines: 100}
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		name := canonicalOption(args[index])
		if seen[name] {
			return options, fmt.Errorf("option %s is repeated", name)
		}
		seen[name] = true
		switch name {
		case "--follow":
			options.follow = true
		case "--raw":
			if events {
				return options, fmt.Errorf("unknown option %s", name)
			}
			options.raw = true
		case "--json":
			if !events {
				return options, fmt.Errorf("unknown option %s", name)
			}
			options.json = true
		case "--full-id":
			options.fullID = true
		case "--lines":
			if index+1 >= len(args) {
				return options, fmt.Errorf("--lines requires a value")
			}
			index++
			value, err := strconv.Atoi(args[index])
			if err != nil || value < 0 || value > runtimecontrol.MaxStreamLines {
				return options, fmt.Errorf("--lines must be between 0 and %d", runtimecontrol.MaxStreamLines)
			}
			options.lines = value
		default:
			return options, fmt.Errorf("unknown option %s", name)
		}
	}
	return options, nil
}

func runRuntimeLogs(args []string, stdout, stderr io.Writer) int {
	options, err := parseStreamOptions(args, false)
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitUsage, "usage", "invalid runtime logs options: "+err.Error(), err))
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	var resolver *runtimecontrol.IdentityResolver
	if !options.raw {
		resolver, err = runtimecontrol.NewIdentityResolver(context.Background(), dataDir)
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "runtime_state_refused", "runtime state is invalid", err))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err = runtimecontrol.StreamLines(ctx, runtimecontrol.StreamOptions{Path: filepath.Join(dataDir, "logs", "openvpn.log"), Rotated: true, Lines: options.lines, Follow: options.follow}, func(line string) error {
		if !options.raw {
			translated, translateErr := resolver.Translate(ctx, line, options.fullID)
			if translateErr != nil {
				return translateErr
			}
			line = translated
		}
		_, err := fmt.Fprintln(stdout, line)
		return err
	})
	if err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "runtime_logs_failed", "read OpenVPN runtime logs", err))
	}
	return int(apperror.ExitSuccess)
}

func runRuntimeEvents(args []string, stdout, stderr io.Writer) int {
	options, err := parseStreamOptions(args, true)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUsage, "usage", "invalid runtime events options: "+err.Error(), err), containsArgument(args, "--json"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	err = runtimecontrol.StreamLines(ctx, runtimecontrol.StreamOptions{Path: filepath.Join(dataDir, "logs", "events.jsonl"), Lines: options.lines, Follow: options.follow}, func(line string) error {
		event, parseErr := runtimecontrol.ParseEvent(line)
		if parseErr != nil {
			if options.follow {
				fmt.Fprintf(stderr, "ovpn: warning: skipped event while following: %v\n", parseErr)
				return nil
			}
			return parseErr
		}
		if options.json {
			encoded, err := event.JSON()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, string(encoded))
			return err
		}
		_, err := fmt.Fprintln(stdout, event.Text(options.fullID))
		return err
	})
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "runtime_events_failed", "read runtime events", err), options.json)
	}
	return int(apperror.ExitSuccess)
}

func containsArgument(args []string, wanted string) bool {
	for _, value := range args {
		if canonicalOption(value) == wanted {
			return true
		}
	}
	return false
}
