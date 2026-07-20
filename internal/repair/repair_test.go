package repair

import (
	"testing"

	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

func TestBuildPlanDeduplicatesSafeActionsAndRetainsBlockers(t *testing.T) {
	report := statecontrol.Report{State: statecontrol.Critical, Issues: []statecontrol.Issue{
		{ID: "PROFILE_MISSING", Severity: statecontrol.SeverityRepairable, Action: "REFRESH_CLIENT_ARTIFACTS", OwnerID: "11111111-1111-4111-8111-111111111111", Target: "clients/a.ovpn"},
		{ID: "PROFILE_DRIFT", Severity: statecontrol.SeverityRepairable, Action: "REFRESH_CLIENT_ARTIFACTS", OwnerID: "11111111-1111-4111-8111-111111111111", Target: "clients/a.ovpn"},
		{ID: "DB_MISSING", Severity: statecontrol.SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db"},
	}}
	plan := BuildPlan(report)
	if plan.Applicable || len(plan.Actions) != 1 || len(plan.Blockers) != 1 || plan.Actions[0].Type != "REFRESH_CLIENT_ARTIFACTS" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestBuildPlanLeavesManualRepairableIssuesBlocked(t *testing.T) {
	plan := BuildPlan(statecontrol.Report{State: statecontrol.DegradedRepairable, Issues: []statecontrol.Issue{{ID: "CONFIG_DRIFT", Severity: statecontrol.SeverityRepairable, Action: "REVIEW_CONFIG_PLAN"}}})
	if !plan.Applicable || len(plan.Actions) != 0 || len(plan.Blockers) != 0 || len(plan.Deferred) != 1 {
		t.Fatalf("plan=%+v", plan)
	}
}
