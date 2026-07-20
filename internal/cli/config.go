package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runConfigShow(args []string, stdout, stderr io.Writer) int {
	jsonMode, ok := parseJSONOnly(args, stdout, stderr, "ovpn config show")
	if !ok {
		return jsonMode
	}
	store, instance, err := openConfigurationState(context.Background())
	if err != nil {
		return writeConfigurationError(stderr, err, jsonMode == 1)
	}
	defer store.Close()
	view, err := configservice.Show(instance.Applied)
	if err != nil {
		return writeConfigurationError(stderr, err, jsonMode == 1)
	}
	if jsonMode == 1 {
		if err := json.NewEncoder(stdout).Encode(view); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write applied configuration", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	yaml, err := configservice.ExportYAML(instance.Applied)
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	fmt.Fprintf(stdout, "Applied revision: %d\nDigest: %s\n\n", view.Revision, view.Digest)
	if _, err := stdout.Write(yaml); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write applied configuration", err))
	}
	return int(apperror.ExitSuccess)
}

func runConfigExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn config export [--output FILE|-]")
		return int(apperror.ExitSuccess)
	}
	output := ""
	for index := 0; index < len(args); index++ {
		if args[index] != "--output" || index+1 >= len(args) || output != "" {
			return writeError(stderr, usageError("usage: ovpn config export [--output FILE|-]"))
		}
		output = args[index+1]
		index++
	}
	store, instance, err := openConfigurationState(context.Background())
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	defer store.Close()
	content, err := configservice.ExportYAML(instance.Applied)
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	if output == "" || output == "-" {
		if _, err := stdout.Write(content); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write configuration export", err))
		}
		return int(apperror.ExitSuccess)
	}
	if err := writeExportFile(output, content); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write configuration export", err))
	}
	return int(apperror.ExitSuccess)
}

func runConfigPlan(args []string, stdout, stderr io.Writer) int {
	jsonMode, ok := parseJSONOnly(args, stdout, stderr, "ovpn config plan")
	if !ok {
		return jsonMode
	}
	desired, err := configservice.LoadFile(environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath))
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "invalid_config", "configuration is invalid", err), jsonMode == 1)
	}
	store, _, err := openConfigurationState(context.Background())
	if err != nil {
		return writeConfigurationError(stderr, err, jsonMode == 1)
	}
	defer store.Close()
	service, err := configurationservice.NewService(store)
	if err != nil {
		return writeConfigurationError(stderr, err, jsonMode == 1)
	}
	plan, err := service.Plan(context.Background(), desired)
	if err != nil {
		return writeConfigurationError(stderr, err, jsonMode == 1)
	}
	if jsonMode == 1 {
		if err := json.NewEncoder(stdout).Encode(plan); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write configuration plan", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	writeConfigurationPlan(stdout, plan)
	return int(apperror.ExitSuccess)
}

// parseJSONOnly returns 0 for human mode or 1 for JSON mode. On invalid input
// it returns the already-written exit code and false.
func parseJSONOnly(args []string, stdout, stderr io.Writer, command string) (int, bool) {
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" {
			if jsonMode {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true), false
			}
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch arg {
		case "--json":
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, usageError("usage: "+command+" [--json]"), jsonMode), false
			}
			fmt.Fprintf(stdout, "Usage: %s [--json]\n", command)
			return int(apperror.ExitSuccess), false
		default:
			return writeErrorMode(stderr, usageError("usage: "+command+" [--json]"), jsonMode), false
		}
	}
	if jsonMode {
		return 1, true
	}
	return 0, true
}

func openConfigurationState(ctx context.Context) (*storesqlite.Store, storesqlite.InstanceState, error) {
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	store, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return nil, storesqlite.InstanceState{}, err
	}
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		_ = store.Close()
		return nil, storesqlite.InstanceState{}, err
	}
	return store, instance, nil
}

func writeConfigurationPlan(writer io.Writer, plan configurationservice.Plan) {
	if plan.Configuration.InSync {
		fmt.Fprintf(writer, "Configuration is in sync at revision %d (%s).\n", plan.Configuration.CurrentRevision, plan.Configuration.CurrentDigest)
		return
	}
	fmt.Fprintf(writer, "Configuration revision: %d -> %d\n", plan.Configuration.CurrentRevision, plan.Configuration.TargetRevision)
	fmt.Fprintln(writer, "Changes:")
	for _, change := range plan.Configuration.Changes {
		fmt.Fprintf(writer, "  %s: %v -> %v\n", change.Field, change.Before, change.After)
	}
	fmt.Fprintf(writer, "Restart required: %t\nFirewall reconcile on next start: %t\n", plan.Configuration.Impact.RestartRequired, plan.Firewall.Reconcile)
	if len(plan.AddressChanges) > 0 {
		fmt.Fprintln(writer, "IPv4 assignments:")
		for _, change := range plan.AddressChanges {
			fmt.Fprintf(writer, "  %s [%s]: %s -> %s\n", change.Client.Name, change.Client.ID, formatAddressIntent(change.Before), formatAddressIntent(change.After))
		}
	}
	if len(plan.Artifacts) > 0 {
		fmt.Fprintln(writer, "Derived artifacts:")
		for _, artifact := range plan.Artifacts {
			fmt.Fprintf(writer, "  %s %s\n", artifact.Action, artifact.Key)
		}
	}
	if len(plan.ProfileRedistribution) > 0 {
		fmt.Fprintln(writer, "Profiles to redistribute:")
		for _, client := range plan.ProfileRedistribution {
			fmt.Fprintf(writer, "  %s [%s]\n", client.Name, client.ID)
		}
	}
}

func formatAddressIntent(value configurationservice.AddressIntent) string {
	if value.Address != nil {
		return value.Mode + ":" + *value.Address
	}
	return value.Mode
}

func writeConfigurationError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, configurationservice.ErrPlanConflict):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "configuration_conflict", "configuration cannot be applied", err), jsonMode)
	case errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "state_busy", "configuration state is busy", err), jsonMode)
	case errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrUnsupportedSchema):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "configuration_state_refused", "configuration state is invalid", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "configuration_failed", "configuration operation failed", err), jsonMode)
	}
}
