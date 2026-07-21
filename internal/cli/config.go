package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
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
		fmt.Fprintln(stdout, "Usage: ovpn config export [--output|-o FILE|-]")
		return int(apperror.ExitSuccess)
	}
	output := ""
	for index := 0; index < len(args); index++ {
		if canonicalOption(args[index]) != "--output" || index+1 >= len(args) || output != "" {
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

func runConfigApply(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn config apply [--yes|-y] [--json|-j]")
		return int(apperror.ExitSuccess)
	}
	yes, jsonMode := false, false
	for _, arg := range args {
		switch canonicalOption(arg) {
		case "--yes":
			if yes {
				return writeErrorMode(stderr, usageError("--yes may only be specified once"), jsonMode)
			}
			yes = true
		case "--json":
			if jsonMode {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true)
			}
			jsonMode = true
		default:
			return writeErrorMode(stderr, usageError("usage: ovpn config apply [--yes] [--json]"), jsonMode)
		}
	}
	desired, err := configservice.LoadFile(environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath))
	if err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "invalid_config", "configuration is invalid", err), jsonMode)
	}
	if !yes {
		confirmed, err := confirmAction(stderr, "Type yes to apply the configuration while OpenVPN is stopped: ")
		if err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "config apply requires an interactive confirmation or --yes", err), jsonMode)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "config apply was not confirmed"), jsonMode)
		}
	}
	renderer, err := configurationRenderer()
	if err != nil {
		return writeConfigurationApplyError(stderr, err, jsonMode)
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	result, err := configurationservice.ApplyPersistent(context.Background(), desired, renderer, render.Paths{DataDir: dataDir, RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)})
	if err != nil {
		return writeConfigurationApplyError(stderr, err, jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write configuration apply result", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	if !result.Applied {
		fmt.Fprintf(stdout, "Configuration is already in sync at revision %d.\n", result.Plan.Configuration.CurrentRevision)
		return int(apperror.ExitSuccess)
	}
	writeConfigurationApplyResult(stdout, result)
	return int(apperror.ExitSuccess)
}

func runServerRender(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn server render [--output|-o FILE|-]")
		return int(apperror.ExitSuccess)
	}
	output := ""
	for index := 0; index < len(args); index++ {
		if canonicalOption(args[index]) != "--output" || index+1 >= len(args) || output != "" {
			return writeError(stderr, usageError("usage: ovpn server render [--output FILE|-]"))
		}
		output = args[index+1]
		index++
	}
	store, instance, err := openConfigurationState(context.Background())
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	defer store.Close()
	renderer, err := configurationRenderer()
	if err != nil {
		return writeConfigurationApplyError(stderr, err, false)
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	content, err := renderer.Server(instance.Applied.Config, render.Paths{DataDir: dataDir, RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)})
	if err != nil {
		return writeConfigurationApplyError(stderr, err, false)
	}
	if output == "" || output == "-" {
		if _, err := stdout.Write(content); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write rendered server configuration", err))
		}
		return int(apperror.ExitSuccess)
	}
	if err := writeExportFile(output, content); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write rendered server configuration", err))
	}
	return int(apperror.ExitSuccess)
}

// parseJSONOnly returns 0 for human mode or 1 for JSON mode. On invalid input
// it returns the already-written exit code and false.
func parseJSONOnly(args []string, stdout, stderr io.Writer, command string) (int, bool) {
	jsonMode := false
	for _, arg := range args {
		if canonicalOption(arg) == "--json" {
			if jsonMode {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true), false
			}
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch canonicalOption(arg) {
		case "--json":
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, usageError("usage: "+command+" [--json]"), jsonMode), false
			}
			fmt.Fprintf(stdout, "Usage: %s [--json|-j]\n", command)
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

func configurationRenderer() (render.Renderer, error) {
	contract, err := compatibility.Load(environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath))
	if err != nil {
		return render.Renderer{}, apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err)
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return render.Renderer{}, apperror.Wrap(apperror.ExitPolicy, "invalid_templates", "OpenVPN templates are invalid", err)
	}
	return renderer, nil
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

func writeConfigurationApplyResult(writer io.Writer, result configurationservice.ApplyResult) {
	fmt.Fprintf(writer, "Applied configuration revision %d (operation %s).\n", result.Plan.Configuration.TargetRevision, result.OperationID)
	if result.Activation.RestartRequired {
		fmt.Fprintln(writer, "Restart required: yes (OpenVPN was not reloaded by config apply).")
	} else {
		fmt.Fprintln(writer, "Restart required: no.")
	}
	if len(result.Activation.ProfileRedistribution) == 0 {
		fmt.Fprintln(writer, "Profiles to redistribute: none.")
		return
	}
	fmt.Fprintf(writer, "Profiles to redistribute: %d\n", len(result.Activation.ProfileRedistribution))
	for _, client := range result.Activation.ProfileRedistribution {
		fmt.Fprintf(writer, "  %s [%s]\n", client.Name, client.ID)
	}
}

func writeConfigurationError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, configurationservice.ErrPlanConflict):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "configuration_conflict", "configuration cannot be applied", err), jsonMode)
	case errors.Is(err, configurationservice.ErrRecoveryRequired):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "operation_recovery_required", "interrupted operation recovery is required before config apply", err), jsonMode)
	case errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "state_busy", "configuration state is busy", err), jsonMode)
	case errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrUnsupportedSchema):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "configuration_state_refused", "configuration state is invalid", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "configuration_failed", "configuration operation failed", err), jsonMode)
	}
}

func writeConfigurationApplyError(stderr io.Writer, err error, jsonMode bool) int {
	var applicationError *apperror.Error
	if errors.As(err, &applicationError) {
		return writeErrorMode(stderr, err, jsonMode)
	}
	switch {
	case errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "configuration_busy", "OpenVPN must be stopped and configuration state unlocked", err), jsonMode)
	case errors.Is(err, pki.ErrInvalidMaterial), errors.Is(err, artifact.ErrUnsafePath), errors.Is(err, storesqlite.ErrConstraint), errors.Is(err, os.ErrNotExist):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "configuration_apply_refused", "configuration apply was refused by state or security policy", err), jsonMode)
	default:
		return writeConfigurationError(stderr, err, jsonMode)
	}
}
