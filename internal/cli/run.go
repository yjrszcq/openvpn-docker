// Package cli implements dependency-free command dispatch for both binaries.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
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
		switch arg {
		case "--short":
			short = true
		case "--json":
			jsonMode = true
		case "-h", "--help":
			if len(args) != 1 {
				return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "help does not accept additional arguments"))
			}
			fmt.Fprintln(stdout, "Usage: ovpn version [--short|--json]")
			return int(apperror.ExitSuccess)
		default:
			return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn version [--short|--json]"))
		}
	}
	if short && jsonMode {
		return writeError(stderr, apperror.New(apperror.ExitUsage, "usage", "--short and --json are mutually exclusive"))
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
	fmt.Fprintf(stdout, "ovpn %s\ndata schema: %d\ncommit: %s\nbuilt: %s\ngo: %s\n", info.Version, info.DataSchema, info.Commit, info.BuildDate, info.GoVersion)
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
