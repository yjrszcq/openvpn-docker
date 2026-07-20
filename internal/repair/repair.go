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

func BuildPlan(report statecontrol.Report) Plan {
	plan := Plan{Version: 1, State: report.State, Applicable: true, Actions: []Action{}, Blockers: []statecontrol.Issue{}, Deferred: []statecontrol.Issue{}}
	seen := make(map[string]struct{})
	for _, issue := range report.Issues {
		if issue.Severity != statecontrol.SeverityRepairable {
			plan.Blockers = append(plan.Blockers, issue)
			continue
		}
		if !automatic(issue.Action) {
			plan.Deferred = append(plan.Deferred, issue)
			continue
		}
		action := Action{Type: issue.Action, OwnerID: issue.OwnerID, Target: issue.Target}
		identity := action.Type + "\x00" + action.OwnerID
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

type Service struct {
	derived *derived.Service
	pki     *pki.Runner
}

func NewService(derivedService *derived.Service, runner *pki.Runner) (*Service, error) {
	if derivedService == nil || runner == nil {
		return nil, fmt.Errorf("derived service and PKI runner are required")
	}
	return &Service{derived: derivedService, pki: runner}, nil
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
	case "REBUILD_CRL", "REFRESH_SERVER_ARTIFACTS", "REFRESH_CLIENT_ARTIFACTS":
		return true
	default:
		return false
	}
}
