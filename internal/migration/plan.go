package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	SnapshotRelativePath = "repair/migrations/schema3-pre-v4.tar.gz"
	ManifestRelativePath = "repair/migrations/transaction.json"
)

type ImportSummary struct {
	Clients            int `json:"clients"`
	ActiveClients      int `json:"active_clients"`
	RevokedClients     int `json:"revoked_clients"`
	DeletedClients     int `json:"deleted_clients"`
	StaticAssignments  int `json:"static_assignments"`
	DynamicAssignments int `json:"dynamic_assignments"`
	ImportedLeases     int `json:"imported_leases"`
	DiscardedLeases    int `json:"discarded_leases"`
	AuditEvents        int `json:"audit_events"`
	Artifacts          int `json:"artifacts"`
}

type ArtifactMapping struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
}

type Plan struct {
	Version                int               `json:"version"`
	Status                 string            `json:"status"`
	Ready                  bool              `json:"ready"`
	SourceSchema           int               `json:"source_schema"`
	TargetSchema           int               `json:"target_schema"`
	InstanceID             string            `json:"instance_id,omitempty"`
	AppliedRevision        uint64            `json:"applied_revision,omitempty"`
	AppliedDigest          string            `json:"applied_digest,omitempty"`
	NormalizedConfig       *config.View      `json:"normalized_config,omitempty"`
	Imports                ImportSummary     `json:"imports"`
	Repairs                []Issue           `json:"repairs"`
	Conflicts              []Issue           `json:"conflicts"`
	Artifacts              []ArtifactMapping `json:"artifacts"`
	ProfilesToRedistribute []string          `json:"profiles_to_redistribute"`
	LegacyFilesToRemove    []string          `json:"legacy_files_to_remove"`
	SnapshotPath           string            `json:"snapshot_path,omitempty"`
	YAMLExportRequired     bool              `json:"yaml_export_required"`
	YAMLExportCommand      string            `json:"yaml_export_command,omitempty"`
	RollbackInstruction    string            `json:"rollback_instruction,omitempty"`
}

// BuildPlan inspects either a schema 3 source or an already-current schema 4
// instance and returns a deterministic, read-only migration plan.
func BuildPlan(ctx context.Context, root string, now time.Time) (Plan, error) {
	databasePath := filepath.Join(root, "meta", "state.db")
	if _, err := os.Lstat(databasePath); err == nil {
		for _, legacy := range []string{"config/schema-version", "config/project.env", "meta/client-state.csv", "meta/client-ip.csv", "meta/audit.jsonl"} {
			if _, legacyErr := os.Lstat(filepath.Join(root, filepath.FromSlash(legacy))); legacyErr == nil {
				return Plan{}, invalid("", "schema 4 database conflicts with live legacy file %s", legacy)
			}
		}
		store, err := storesqlite.Open(ctx, databasePath)
		if err != nil {
			return Plan{}, fmt.Errorf("open current schema 4 state: %w", err)
		}
		defer store.Close()
		instance, err := store.LoadOnlyInstance(ctx)
		if err != nil {
			return Plan{}, fmt.Errorf("load current schema 4 instance: %w", err)
		}
		return Plan{Version: 1, Status: "current", Ready: false, SourceSchema: 4, TargetSchema: 4, InstanceID: instance.ID, AppliedRevision: uint64(instance.Applied.Revision), AppliedDigest: instance.Applied.Digest, Imports: ImportSummary{}, Repairs: []Issue{}, Conflicts: []Issue{}, Artifacts: []ArtifactMapping{}, ProfilesToRedistribute: []string{}, LegacyFilesToRemove: []string{}}, nil
	} else if !os.IsNotExist(err) {
		return Plan{}, invalid("meta/state.db", "%v", err)
	}
	source, err := ReadSchema3(ctx, root, now)
	if err != nil {
		return Plan{}, err
	}
	digest, err := config.Digest(source.Config)
	if err != nil {
		return Plan{}, err
	}
	view := config.NewView(source.Config)
	plan := Plan{
		Version: 1, Status: "ready", Ready: true, SourceSchema: 3, TargetSchema: 4,
		InstanceID: source.Instance.ID, AppliedRevision: 1, AppliedDigest: digest,
		NormalizedConfig: &view, Repairs: append([]Issue(nil), source.Repairs...), Conflicts: []Issue{},
		Artifacts: make([]ArtifactMapping, 0, len(source.Artifacts)), ProfilesToRedistribute: []string{},
		LegacyFilesToRemove: []string{"config/project.env", "config/schema-version", "meta/instance.json", "meta/instance-id", "meta/client-state.csv", "meta/client-ip.csv", "meta/audit.jsonl"},
		SnapshotPath:        filepath.Join(root, filepath.FromSlash(SnapshotRelativePath)), YAMLExportRequired: true,
		YAMLExportCommand:   "ovpn config export --output /etc/openvpn-config/config.yaml",
		RollbackInstruction: "restore the complete migration snapshot, then run the sh-ver image",
	}
	for _, client := range source.Clients {
		plan.Imports.Clients++
		switch client.Client.Status {
		case domain.ClientActive:
			plan.Imports.ActiveClients++
			plan.ProfilesToRedistribute = append(plan.ProfilesToRedistribute, client.Client.Name)
		case domain.ClientRevoked:
			plan.Imports.RevokedClients++
		case domain.ClientDeleted:
			plan.Imports.DeletedClients++
		}
		if client.Client.Status != domain.ClientDeleted {
			if client.Address == nil {
				plan.Imports.DynamicAssignments++
			} else {
				plan.Imports.StaticAssignments++
			}
		}
	}
	for _, lease := range source.Leases {
		if lease.Import {
			plan.Imports.ImportedLeases++
		} else {
			plan.Imports.DiscardedLeases++
		}
	}
	plan.Imports.AuditEvents = len(source.Audit)
	plan.Imports.Artifacts = len(source.Artifacts)
	for _, value := range source.Artifacts {
		plan.Artifacts = append(plan.Artifacts, ArtifactMapping{OwnerKind: value.OwnerKind, OwnerID: value.OwnerID, Kind: value.Kind, Key: value.Key})
	}
	sort.Slice(plan.Artifacts, func(i, j int) bool {
		if plan.Artifacts[i].Key != plan.Artifacts[j].Key {
			return plan.Artifacts[i].Key < plan.Artifacts[j].Key
		}
		return plan.Artifacts[i].Kind < plan.Artifacts[j].Kind
	})
	sort.Strings(plan.ProfilesToRedistribute)
	return plan, nil
}
