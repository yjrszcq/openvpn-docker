// Package repair plans and applies safe, derivable state repairs.
package repair

import (
	"context"
	"fmt"
	"sort"

	"github.com/yjrszcq/openvpn-docker/internal/derived"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

type Action struct {
	Type    string `json:"type"`
	OwnerID string `json:"ownerId,omitempty"`
	Target  string `json:"target,omitempty"`
}

type Plan struct {
	Version    int                         `json:"version"`
	State      statecontrol.Classification `json:"state"`
	Applicable bool                        `json:"applicable"`
	Actions    []Action                    `json:"actions"`
	Blockers   []statecontrol.Issue        `json:"blockers"`
	Deferred   []statecontrol.Issue        `json:"deferred"`
}

type Result struct {
	Version      int      `json:"version"`
	OperationIDs []string `json:"operationIds"`
	Actions      []Action `json:"actions"`
}

func BuildPlan(report statecontrol.Report, recoveryReady ...map[string]bool) Plan {
	ready := map[string]bool{}
	if len(recoveryReady) != 0 && recoveryReady[0] != nil {
		ready = recoveryReady[0]
	}
	plan := Plan{Version: 1, State: report.State, Applicable: true, Actions: []Action{}, Blockers: []statecontrol.Issue{}, Deferred: []statecontrol.Issue{}}
	seen := make(map[string]struct{})
	for _, issue := range report.Issues {
		ownerID := canonicalOwner(issue.Action, issue.OwnerID)
		identity := issue.Action + "\x00" + ownerID
		if issue.Severity != statecontrol.SeverityRepairable && !(issue.Severity == statecontrol.SeverityRecoverable && ready[identity]) {
			plan.Blockers = append(plan.Blockers, issue)
			continue
		}
		if issue.Severity == statecontrol.SeverityRepairable && !automatic(issue.Action) {
			plan.Deferred = append(plan.Deferred, issue)
			continue
		}
		action := Action{Type: issue.Action, OwnerID: ownerID, Target: issue.Target}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		plan.Actions = append(plan.Actions, action)
	}
	sort.Slice(plan.Actions, func(i, j int) bool {
		if plan.Actions[i].Type != plan.Actions[j].Type {
			return plan.Actions[i].Type < plan.Actions[j].Type
		}
		return plan.Actions[i].OwnerID < plan.Actions[j].OwnerID
	})
	plan.Applicable = len(plan.Blockers) == 0
	return plan
}

func canonicalOwner(action, ownerID string) string {
	if action == "REFRESH_CLIENT_ARTIFACTS" || action == "RECOVER_CLIENT_IDENTITY" {
		return ownerID
	}
	return ""
}

type Service struct {
	derived   *derived.Service
	pki       *pki.Runner
	recoverer Recoverer
}

type Recoverer interface {
	Recover(context.Context, string, string, string) (string, error)
}

func NewService(derivedService *derived.Service, runner *pki.Runner, recoverer Recoverer) (*Service, error) {
	if derivedService == nil || runner == nil {
		return nil, fmt.Errorf("derived service and PKI runner are required")
	}
	return &Service{derived: derivedService, pki: runner, recoverer: recoverer}, nil
}

func (service *Service) Apply(ctx context.Context, instanceID string, plan Plan) (Result, error) {
	if !plan.Applicable || len(plan.Blockers) != 0 {
		return Result{}, fmt.Errorf("repair plan contains blockers")
	}
	result := Result{Version: 1, OperationIDs: []string{}, Actions: append([]Action(nil), plan.Actions...)}
	for _, action := range plan.Actions {
		var operation derived.RefreshResult
		var err error
		switch action.Type {
		case "REBUILD_CRL":
			operation, err = service.derived.RefreshCRL(ctx, instanceID, service.pki)
		case "REFRESH_SERVER_ARTIFACTS":
			operation, err = service.derived.RefreshServer(ctx, instanceID)
		case "REFRESH_CLIENT_ARTIFACTS":
			if action.OwnerID == "" {
				return Result{}, fmt.Errorf("client artifact repair is missing owner UUID")
			}
			operation, err = service.derived.RefreshClient(ctx, instanceID, action.OwnerID)
		case "RECOVER_CA_CERT", "RECOVER_TLS_CRYPT", "RECOVER_CLIENT_IDENTITY":
			if service.recoverer == nil {
				return Result{}, fmt.Errorf("recovery service is unavailable")
			}
			var operationID string
			operationID, err = service.recoverer.Recover(ctx, instanceID, action.Type, action.OwnerID)
			operation.OperationID = operationID
		default:
			return Result{}, fmt.Errorf("unsupported repair action %s", action.Type)
		}
		if err != nil {
			return Result{}, fmt.Errorf("apply %s: %w", action.Type, err)
		}
		result.OperationIDs = append(result.OperationIDs, operation.OperationID)
	}
	return result, nil
}

func automatic(action string) bool {
	switch action {
	case "REBUILD_CRL", "REFRESH_SERVER_ARTIFACTS", "REFRESH_CLIENT_ARTIFACTS", "RECOVER_CA_CERT", "RECOVER_TLS_CRYPT", "RECOVER_CLIENT_IDENTITY":
		return true
	default:
		return false
	}
}
