package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var ErrOperationConflict = errors.New("operation recovery evidence conflicts")

type OperationRecovery struct {
	OperationID string `json:"operationId"`
	Action      string `json:"action"`
}

type OperationRecoveryResult struct {
	Version int                 `json:"version"`
	Actions []OperationRecovery `json:"actions"`
}

// RecoverOperations reconciles the durable SQLite journal with local artifact
// manifests under the instance-wide exclusive data lock.
func RecoverOperations(ctx context.Context, dataDir string) (OperationRecoveryResult, error) {
	result := OperationRecoveryResult{Version: 1, Actions: []OperationRecovery{}}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(dataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return result, err
	}
	defer lock.Release()
	store, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return result, err
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		return result, err
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return result, err
	}
	artifactIDs, err := local.PendingOperations()
	if err != nil {
		return result, err
	}
	artifactSet := make(map[string]bool, len(artifactIDs))
	for _, id := range artifactIDs {
		artifactSet[id] = true
	}
	pending, err := store.PendingOperations(ctx, instance.ID)
	if err != nil {
		return result, err
	}
	for _, journal := range pending {
		if artifactSet[journal.ID] {
			operation, err := local.OpenOperation(journal.ID)
			if err != nil {
				return result, err
			}
			if operation.State() == artifact.OperationCommitted {
				return result, conflict(journal.ID, journal.State, operation.State())
			}
			if err := operation.Rollback(); err != nil {
				return result, fmt.Errorf("roll back artifact operation %s: %w", journal.ID, err)
			}
			delete(artifactSet, journal.ID)
		}
		if err := store.AdvanceOperation(ctx, journal.ID, storesqlite.OperationRolledBack, journal.RecoveryPayload, "", time.Now().UTC()); err != nil {
			return result, fmt.Errorf("roll back SQLite operation %s: %w", journal.ID, err)
		}
		result.Actions = append(result.Actions, OperationRecovery{OperationID: journal.ID, Action: "rolled-back"})
	}
	remaining := make([]string, 0, len(artifactSet))
	for id := range artifactSet {
		remaining = append(remaining, id)
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		operation, err := local.OpenOperation(id)
		if err != nil {
			return result, err
		}
		journal, journalErr := store.LoadOperation(ctx, id)
		if errors.Is(journalErr, sql.ErrNoRows) {
			if operation.State() == artifact.OperationCommitted {
				return result, fmt.Errorf("%w: committed artifact operation %s has no SQLite journal", ErrOperationConflict, id)
			}
			if err := operation.Rollback(); err != nil {
				return result, err
			}
			result.Actions = append(result.Actions, OperationRecovery{OperationID: id, Action: "orphan-rolled-back"})
			continue
		}
		if journalErr != nil {
			return result, journalErr
		}
		switch journal.State {
		case storesqlite.OperationCommitted:
			if operation.State() != artifact.OperationFilesInstalled && operation.State() != artifact.OperationCommitted {
				return result, conflict(id, journal.State, operation.State())
			}
			if err := operation.Commit(nil); err != nil {
				return result, fmt.Errorf("finish artifact operation %s: %w", id, err)
			}
			result.Actions = append(result.Actions, OperationRecovery{OperationID: id, Action: "commit-finished"})
		case storesqlite.OperationRolledBack, storesqlite.OperationFailed:
			if operation.State() == artifact.OperationCommitted {
				return result, conflict(id, journal.State, operation.State())
			}
			if err := operation.Rollback(); err != nil {
				return result, fmt.Errorf("finish rollback for artifact operation %s: %w", id, err)
			}
			result.Actions = append(result.Actions, OperationRecovery{OperationID: id, Action: "rollback-finished"})
		default:
			return result, conflict(id, journal.State, operation.State())
		}
	}
	return result, nil
}

func conflict(id string, database storesqlite.OperationState, files artifact.OperationState) error {
	return fmt.Errorf("%w: operation %s SQLite=%s artifact=%s", ErrOperationConflict, id, database, files)
}
