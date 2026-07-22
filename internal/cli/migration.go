package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	migrationservice "github.com/yjrszcq/openvpn-docker/internal/migration"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runMigrationPlan(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && canonicalOption(args[0]) == "--json"
	if len(args) != 0 && !jsonMode {
		return writeErrorMode(stderr, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn migrate plan [--json]"), jsonRequested)
	}
	plan, err := migrationservice.BuildPlan(context.Background(), environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir), time.Now().UTC())
	if err != nil {
		return writeMigrationError(stderr, err, jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(plan); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write migration plan", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	if plan.Status == "current" {
		fmt.Fprintf(stdout, "migration: current\nschema: 4\ninstance: %s\napplied revision: %d\n", plan.InstanceID, plan.AppliedRevision)
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "migration: ready\nschema: 3 -> 4\ninstance: %s\nclients: %d (%d active, %d revoked, %d deleted)\nassignments: %d static, %d dynamic\nleases: %d import, %d discard\naudit events: %d\nartifacts: %d\nrepairs: %d\nsnapshot: %s\n", plan.InstanceID, plan.Imports.Clients, plan.Imports.ActiveClients, plan.Imports.RevokedClients, plan.Imports.DeletedClients, plan.Imports.StaticAssignments, plan.Imports.DynamicAssignments, plan.Imports.ImportedLeases, plan.Imports.DiscardedLeases, plan.Imports.AuditEvents, plan.Imports.Artifacts, len(plan.Repairs), plan.SnapshotPath)
	for _, repair := range plan.Repairs {
		fmt.Fprintf(stdout, "- repair %s (%s): %s\n", repair.Code, repair.Path, repair.Detail)
	}
	fmt.Fprintf(stdout, "profiles to redistribute: %d\nafter migration: %s\nrollback: %s\n", len(plan.ProfilesToRedistribute), plan.YAMLExportCommand, plan.RollbackInstruction)
	return int(apperror.ExitSuccess)
}

func runMigrationApply(args []string, stdout, stderr io.Writer) int {
	yes, jsonMode, err := parseMigrationApplyOptions(args)
	if err != nil {
		return writeErrorMode(stderr, err, containsArgument(args, "--json"))
	}
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	now := time.Now().UTC()
	markerPresent, err := migrationMarkerPresent(dataDir)
	if err != nil {
		return writeMigrationError(stderr, err, jsonMode)
	}
	var plan migrationservice.Plan
	if !markerPresent {
		plan, err = migrationservice.BuildPlan(context.Background(), dataDir, now)
		if err != nil {
			return writeMigrationError(stderr, err, jsonMode)
		}
	}
	if plan.Status == "current" && !markerPresent {
		return writeMigrationApplyResult(stdout, stderr, migrationservice.ApplyResult{Version: 1, Applied: false, InstanceID: plan.InstanceID, SourceSchema: 4, TargetSchema: 4, FinalState: "HEALTHY"}, jsonMode)
	}
	if os.Getenv("OVPN_MAINTENANCE") != "true" {
		return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "maintenance_required", "migrate apply requires OVPN_MAINTENANCE=true"), jsonMode)
	}
	renderer, err := configurationRenderer()
	if err != nil {
		return writeMigrationError(stderr, err, jsonMode)
	}
	if !yes {
		prompt := fmt.Sprintf("Type yes to migrate schema 3 to SQLite (%d clients, snapshot %s): ", plan.Imports.Clients, plan.SnapshotPath)
		if markerPresent {
			prompt = "Type yes to recover the interrupted schema migration: "
		}
		confirmed, confirmErr := confirmAction(stderr, prompt)
		if confirmErr != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "migrate apply requires an interactive confirmation or --yes", confirmErr), jsonMode)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "migrate apply was not confirmed"), jsonMode)
		}
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	result, err := migrationservice.Apply(context.Background(), migrationservice.ApplyOptions{DataDir: dataDir, RuntimeDir: runtimeDir, Maintenance: true, Version: buildinfo.Current().Version, Renderer: renderer, Paths: render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir}, Now: now})
	if err != nil {
		return writeMigrationError(stderr, err, jsonMode)
	}
	return writeMigrationApplyResult(stdout, stderr, result, jsonMode)
}

func migrationMarkerPresent(dataDir string) (bool, error) {
	_, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(migrationservice.ManifestRelativePath)))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func writeMigrationApplyResult(stdout, stderr io.Writer, result migrationservice.ApplyResult, jsonMode bool) int {
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write migration result", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	if !result.Applied {
		fmt.Fprintf(stdout, "migration: current\nschema: 4\ninstance: %s\n", result.InstanceID)
		if result.Recovered {
			fmt.Fprintln(stdout, "interrupted transaction recovery: complete")
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "migration: applied\nschema: 3 -> 4\ninstance: %s\noperation: %s\nclients: %d\naudit events imported: %d\nstate doctor: %s\nsnapshot: %s\nsnapshot digest: %s\nnext: ovpn config export --output /etc/ovpn-conf/config.yaml\nrollback: verify and restore the complete snapshot, then run the sh-ver image\n", result.InstanceID, result.OperationID, result.Clients, result.AuditEvents, result.FinalState, result.SnapshotPath, result.SnapshotDigestPath)
	return int(apperror.ExitSuccess)
}

func parseMigrationApplyOptions(args []string) (bool, bool, error) {
	var yes, jsonMode bool
	for _, arg := range args {
		switch canonicalOption(arg) {
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
			return false, jsonMode, apperror.New(apperror.ExitUsage, "usage", "usage: ovpn migrate apply [--yes] [--json]")
		}
	}
	return yes, jsonMode, nil
}

func writeMigrationError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, migrationservice.ErrNeedsShellUpgrade):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_upgrade_required", "schema must be upgraded to 3 with sh-ver before migration", err), jsonMode)
	case errors.Is(err, migrationservice.ErrUnsupportedSource):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_schema_unsupported", "migration source schema is unsupported", err), jsonMode)
	case errors.Is(err, migrationservice.ErrInvalidSource):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_source_invalid", "migration source is invalid", err), jsonMode)
	case errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "migration_busy", "migration lock or state is busy", err), jsonMode)
	case errors.Is(err, migrationservice.ErrMaintenanceRequired):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "maintenance_required", "migration requires maintenance mode", err), jsonMode)
	case errors.Is(err, migrationservice.ErrSnapshotExists), errors.Is(err, migrationservice.ErrRecoveryRequired):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_recovery_refused", "migration snapshot or recovery state requires operator review", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_refused", "migration was refused", err), jsonMode)
	}
}
