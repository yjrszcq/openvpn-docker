package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

func runState(args []string, stdout, stderr io.Writer, doctor bool) int {
	command := "show"
	if doctor {
		command = "doctor"
	}
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintf(stdout, "Usage: ovpn state %s [--json]\n", command)
		return int(apperror.ExitSuccess)
	}
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && args[0] == "--json"
	if len(args) != 0 && !jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", fmt.Sprintf("usage: ovpn state %s [--json]", command)), jsonRequested)
	}
	contract, err := compatibility.Load(environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath))
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err), jsonMode)
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "invalid_templates", "OpenVPN templates are invalid", err), jsonMode)
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	report := statecontrol.Scan(context.Background(), statecontrol.Options{
		DataDir: dataDir, ConfigFile: environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath),
		ServerName: initialize.DefaultServerName, Renderer: renderer,
		Paths: render.Paths{DataDir: dataDir, RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)},
	})
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write state report", err), true)
		}
	} else {
		fmt.Fprintf(stdout, "state: %s\nschema: %d\nissues: %d\n", report.State, report.DataSchema, report.IssueCount)
		if report.InstanceID != "" {
			fmt.Fprintf(stdout, "instance: %s\napplied revision: %d\n", report.InstanceID, report.Revision)
		}
		if doctor {
			for _, issue := range report.Issues {
				identity := issue.Target
				if issue.OwnerID != "" {
					identity = issue.OwnerID
					if issue.Target != "" {
						identity += " " + issue.Target
					}
				}
				fmt.Fprintf(stdout, "- [%s] %s: %s", issue.Severity, issue.ID, issue.Detail)
				if identity != "" {
					fmt.Fprintf(stdout, " (%s)", identity)
				}
				fmt.Fprintf(stdout, " -> %s\n", issue.Action)
			}
		}
	}
	if doctor && (report.State == statecontrol.Critical || report.State == statecontrol.Unrecoverable) {
		return int(apperror.ExitPolicy)
	}
	return int(apperror.ExitSuccess)
}
