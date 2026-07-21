package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

func runState(args []string, stdout, stderr io.Writer, doctor bool) int {
	command := "show"
	if doctor {
		command = "doctor"
	}
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintf(stdout, "Usage: ovpn state %s [--json|-j]\n", command)
		return int(apperror.ExitSuccess)
	}
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && canonicalOption(args[0]) == "--json"
	if len(args) != 0 && !jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", fmt.Sprintf("usage: ovpn state %s [--json]", command)), jsonRequested)
	}
	options, err := stateScanOptions()
	if err != nil {
		return writeErrorMode(stderr, err, jsonMode)
	}
	report := statecontrol.Scan(context.Background(), options)
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
