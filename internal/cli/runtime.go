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
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn runtime status [--json|-j]")
		return int(apperror.ExitSuccess)
	}
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && canonicalOption(args[0]) == "--json"
	if len(args) != 0 && !jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn runtime status [--json]"), jsonRequested)
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
		identity := client.ClientID
		if client.ClientName != "" {
			identity = fmt.Sprintf("%s [%s]", client.ClientName, client.ClientID)
		}
		fmt.Fprintf(stdout, "- %s virtual=%s remote=%s\n", identity, client.VirtualAddress, client.RemoteAddress)
	}
	return int(apperror.ExitSuccess)
}

func runRuntimeHealth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn runtime health")
		return int(apperror.ExitSuccess)
	}
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
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn runtime logs [--lines|-l N] [--follow|-f] [--raw|-r] [--full-id|-u]")
		return int(apperror.ExitSuccess)
	}
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
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn runtime events [--lines|-l N] [--follow|-f] [--json|-j] [--full-id|-u]")
		return int(apperror.ExitSuccess)
	}
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
