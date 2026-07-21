package migration

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBuildPlanReportsCompleteSchema3Impact(t *testing.T) {
	fixture := makeLegacyFixture(t)
	plan, err := BuildPlan(context.Background(), fixture.root, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready || plan.Status != "ready" || plan.SourceSchema != 3 || plan.TargetSchema != 4 || plan.AppliedRevision != 1 || len(plan.AppliedDigest) != 64 {
		t.Fatalf("unexpected plan identity: %+v", plan)
	}
	if plan.Imports.Clients != 1 || plan.Imports.ActiveClients != 1 || plan.Imports.DynamicAssignments != 1 || plan.Imports.ImportedLeases != 1 || plan.Imports.Artifacts != 9 {
		t.Fatalf("unexpected imports: %+v", plan.Imports)
	}
	if len(plan.Artifacts) != 9 || len(plan.ProfilesToRedistribute) != 1 || plan.ProfilesToRedistribute[0] != "laptop" || len(plan.LegacyFilesToRemove) != 7 {
		t.Fatalf("unexpected impacts: %+v", plan)
	}
	if plan.SnapshotPath != filepath.Join(fixture.root, filepath.FromSlash(SnapshotRelativePath)) || !plan.YAMLExportRequired || plan.YAMLExportCommand == "" || plan.RollbackInstruction == "" {
		t.Fatalf("handoff is incomplete: %+v", plan)
	}
}

func TestBuildPlanRejectsMixedSchemaAuthority(t *testing.T) {
	fixture := makeLegacyFixture(t)
	writeLegacy(t, fixture.root, "meta/state.db", "not sqlite", 0o600)
	if _, err := BuildPlan(context.Background(), fixture.root, fixture.now); err == nil {
		t.Fatal("mixed schema authority was accepted")
	}
}
