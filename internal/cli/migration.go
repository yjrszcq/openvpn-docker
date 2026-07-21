package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	migrationservice "github.com/yjrszcq/openvpn-docker/internal/migration"
)

func runMigrationPlan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn migrate plan [--json]")
		return int(apperror.ExitSuccess)
	}
	jsonRequested := containsArgument(args, "--json")
	jsonMode := len(args) == 1 && args[0] == "--json"
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

func writeMigrationError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, migrationservice.ErrNeedsShellUpgrade):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_upgrade_required", "schema must be upgraded to 3 with sh-ver before migration", err), jsonMode)
	case errors.Is(err, migrationservice.ErrUnsupportedSource):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_schema_unsupported", "migration source schema is unsupported", err), jsonMode)
	case errors.Is(err, migrationservice.ErrInvalidSource):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_source_invalid", "migration source is invalid", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "migration_refused", "migration was refused", err), jsonMode)
	}
}
