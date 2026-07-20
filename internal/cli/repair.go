package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/derived"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	recoveryservice "github.com/yjrszcq/openvpn-docker/internal/recovery"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	repairservice "github.com/yjrszcq/openvpn-docker/internal/repair"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runRepairPlan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn repair plan [--json]")
		return int(apperror.ExitSuccess)
	}
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && args[0] == "--json"
	if len(args) != 0 && !jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn repair plan [--json]"), jsonRequested)
	}
	options, err := repairScanOptions()
	if err != nil {
		return writeErrorMode(stderr, err, jsonMode)
	}
	plan, err := buildRepairPlan(context.Background(), options)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	return writeRepairPlan(plan, stdout, stderr, jsonMode)
}

func runRepairApply(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn repair apply [--yes] [--json]")
		return int(apperror.ExitSuccess)
	}
	yes, jsonMode, err := parseRepairApplyOptions(args)
	if err != nil {
		return writeErrorMode(stderr, err, containsArgument(args, "--json"))
	}
	if !yes {
		confirmed, confirmErr := confirmAction(stderr, "Type yes to apply the repair plan: ")
		if confirmErr != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "repair apply requires an interactive confirmation or --yes", confirmErr), jsonMode)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "repair apply was not confirmed"), jsonMode)
		}
	}
	options, err := repairScanOptions()
	if err != nil {
		return writeErrorMode(stderr, err, jsonMode)
	}
	ctx := context.Background()
	if _, err := recoveryservice.RecoverOperations(ctx, options.DataDir); err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	plan, err := buildRepairPlan(ctx, options)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	if !plan.Applicable {
		return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "repair_refused", "repair plan contains authority, recovery, or reissue blockers"), jsonMode)
	}
	store, err := storesqlite.Open(ctx, filepath.Join(options.DataDir, "meta", "state.db"))
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	local, err := artifact.NewLocal(options.DataDir)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	derivedService, err := derived.NewService(store, local, options.Renderer, options.Paths)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	runner, err := runtimePKIRunner()
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	recoverer, err := recoveryservice.NewService(store, local, options.DataDir)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	service, err := repairservice.NewService(derivedService, runner, recoverer)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	result, err := service.Apply(ctx, instance.ID, plan)
	if err != nil {
		return writeRepairError(stderr, err, jsonMode)
	}
	final := statecontrol.Scan(ctx, options)
	output := struct {
		repairservice.Result
		FinalState      statecontrol.Classification `json:"finalState"`
		RemainingIssues int                         `json:"remainingIssues"`
	}{Result: result, FinalState: final.State, RemainingIssues: final.IssueCount}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(output); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write repair result", err), true)
		}
	} else {
		fmt.Fprintf(stdout, "applied repairs: %d\nfinal state: %s\nremaining issues: %d\n", len(result.Actions), final.State, final.IssueCount)
		for index, action := range result.Actions {
			fmt.Fprintf(stdout, "- %s", action.Type)
			if action.OwnerID != "" {
				fmt.Fprintf(stdout, " %s", action.OwnerID)
			}
			if index < len(result.OperationIDs) {
				fmt.Fprintf(stdout, " operation=%s", result.OperationIDs[index])
			}
			fmt.Fprintln(stdout)
		}
	}
	if final.State == statecontrol.Critical || final.State == statecontrol.Unrecoverable {
		return int(apperror.ExitPolicy)
	}
	return int(apperror.ExitSuccess)
}

func buildRepairPlan(ctx context.Context, options statecontrol.Options) (repairservice.Plan, error) {
	report := statecontrol.Scan(ctx, options)
	ready := map[string]bool{}
	if report.InstanceID != "" {
		store, err := storesqlite.Open(ctx, filepath.Join(options.DataDir, "meta", "state.db"))
		if err != nil {
			return repairservice.Plan{}, err
		}
		defer store.Close()
		local, err := artifact.NewLocal(options.DataDir)
		if err != nil {
			return repairservice.Plan{}, err
		}
		service, err := recoveryservice.NewService(store, local, options.DataDir)
		if err != nil {
			return repairservice.Plan{}, err
		}
		assessment, err := service.Assess(ctx, report)
		if err != nil {
			return repairservice.Plan{}, err
		}
		for _, candidate := range assessment.Ready {
			ready[candidate.Action+"\x00"+candidate.OwnerID] = true
		}
	}
	return repairservice.BuildPlan(report, ready), nil
}

func repairScanOptions() (statecontrol.Options, error) {
	contract, err := compatibility.Load(environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath))
	if err != nil {
		return statecontrol.Options{}, apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err)
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return statecontrol.Options{}, apperror.Wrap(apperror.ExitPolicy, "invalid_templates", "OpenVPN templates are invalid", err)
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	return statecontrol.Options{DataDir: dataDir, ConfigFile: environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath), ServerName: initialize.DefaultServerName, Renderer: renderer, Paths: render.Paths{DataDir: dataDir, RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)}}, nil
}

func writeRepairPlan(plan repairservice.Plan, stdout, stderr io.Writer, jsonMode bool) int {
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(plan); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write repair plan", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "state: %s\napplicable: %t\nactions: %d\nblockers: %d\ndeferred: %d\n", plan.State, plan.Applicable, len(plan.Actions), len(plan.Blockers), len(plan.Deferred))
	for _, action := range plan.Actions {
		fmt.Fprintf(stdout, "- %s", action.Type)
		if action.OwnerID != "" {
			fmt.Fprintf(stdout, " %s", action.OwnerID)
		}
		fmt.Fprintln(stdout)
	}
	return int(apperror.ExitSuccess)
}

func parseRepairApplyOptions(args []string) (bool, bool, error) {
	var yes, jsonMode bool
	for _, arg := range args {
		switch arg {
		case "--yes":
			if yes {
				return false, jsonMode, apperror.New(apperror.ExitUsage, "usage", "--yes may only be specified once")
			}
			yes = true
		case "--json":
			if jsonMode {
				return false, true, apperror.New(apperror.ExitUsage, "usage", "--json may only be specified once")
			}
			jsonMode = true
		default:
			return false, jsonMode, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn repair apply [--yes] [--json]")
		}
	}
	return yes, jsonMode, nil
}

func writeRepairError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "repair_busy", "repair lock or state is busy", err), jsonMode)
	case errors.Is(err, pki.ErrUnavailable):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "repair dependency is unavailable", err), jsonMode)
	case errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrMissing), errors.Is(err, pki.ErrInvalidMaterial), errors.Is(err, recoveryservice.ErrOperationConflict):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "repair_refused", "repair was refused by state or security policy", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "repair_failed", "apply repair plan", err), jsonMode)
	}
}
