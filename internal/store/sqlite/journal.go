package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

type OperationState string

const (
	OperationPrepared       OperationState = "prepared"
	OperationFilesInstalled OperationState = "files-installed"
	OperationCommitted      OperationState = "committed"
	OperationRolledBack     OperationState = "rolled-back"
	OperationFailed         OperationState = "failed"
)

type AuditEvent struct {
	Sequence       uint64
	EventID        string
	InstanceID     string
	OperationID    string
	Type           string
	PayloadVersion int
	Payload        json.RawMessage
	CreatedAt      time.Time
}

type Operation struct {
	ID              string
	InstanceID      string
	Kind            string
	State           OperationState
	PayloadVersion  int
	RecoveryPayload json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Failure         string
}

// PrepareOperation records durable recovery intent and its audit event atomically.
func (store *Store) PrepareOperation(ctx context.Context, operation Operation) error {
	if err := validateOperation(operation); err != nil {
		return err
	}
	if operation.State != OperationPrepared || operation.CreatedAt != operation.UpdatedAt {
		return fmt.Errorf("new operation must be prepared with matching timestamps")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin operation prepare", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO operations(id, instance_id, kind, state, payload_version, recovery_payload, created_at, updated_at, failure)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, NULL)`, operation.ID, operation.InstanceID, operation.Kind,
		operation.State, operation.PayloadVersion, string(operation.RecoveryPayload), formatTime(operation.CreatedAt), formatTime(operation.UpdatedAt)); err != nil {
		return classifySQLite("prepare operation", err)
	}
	if err := appendAudit(ctx, transaction, operation.InstanceID, operation.ID, "operation.prepared", map[string]any{"kind": operation.Kind}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit operation prepare")
}

// AdvanceOperation applies one allowed journal transition with an audit event.
func (store *Store) AdvanceOperation(ctx context.Context, operationID string, next OperationState, recoveryPayload json.RawMessage, failure string, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || !json.Valid(recoveryPayload) || updatedAt.IsZero() {
		return fmt.Errorf("invalid operation transition")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin operation transition", err)
	}
	defer transaction.Rollback()
	var instanceID, kind string
	var current OperationState
	if err := transaction.QueryRowContext(ctx, "SELECT instance_id, kind, state FROM operations WHERE id = ?", operationID).Scan(&instanceID, &kind, &current); err != nil {
		return fmt.Errorf("load operation: %w", err)
	}
	if !allowedTransition(current, next) {
		return fmt.Errorf("operation transition %s -> %s is not allowed", current, next)
	}
	if next == OperationFailed && failure == "" {
		return fmt.Errorf("failed operation requires a failure description")
	}
	if next != OperationFailed {
		failure = ""
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE operations SET state = ?, recovery_payload = ?, updated_at = ?, failure = NULLIF(?, '') WHERE id = ?`, next, string(recoveryPayload), formatTime(updatedAt), failure, operationID); err != nil {
		return classifySQLite("advance operation", err)
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation."+string(next), map[string]any{"kind": kind}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit operation transition")
}

// PendingOperations returns recovery work in deterministic creation order.
func (store *Store) PendingOperations(ctx context.Context, instanceID string) ([]Operation, error) {
	if !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("invalid instance UUID")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT id, instance_id, kind, state, payload_version, recovery_payload, created_at, updated_at, COALESCE(failure, '')
FROM operations WHERE instance_id = ? AND state IN ('prepared', 'files-installed') ORDER BY created_at, id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Operation, 0)
	for rows.Next() {
		value, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// AuditEvents returns a stable ascending page after sequence.
func (store *Store) AuditEvents(ctx context.Context, instanceID string, after uint64, limit int) ([]AuditEvent, error) {
	if !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("invalid instance UUID")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("audit event limit must be between 1 and 1000")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT sequence, event_id, instance_id, operation_id, event_type, payload_version, payload, created_at
FROM audit_events WHERE instance_id = ? AND sequence > ? ORDER BY sequence LIMIT ?`, instanceID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]AuditEvent, 0)
	for rows.Next() {
		var value AuditEvent
		var payload, created string
		if err := rows.Scan(&value.Sequence, &value.EventID, &value.InstanceID, &value.OperationID, &value.Type, &value.PayloadVersion, &payload, &created); err != nil {
			return nil, err
		}
		value.Payload = json.RawMessage(payload)
		value.CreatedAt, err = parseTime(created)
		if err != nil || !json.Valid(value.Payload) || !domain.ValidUUID(value.EventID) || !domain.ValidUUID(value.OperationID) {
			return nil, fmt.Errorf("%w: invalid audit event", ErrSchema)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func appendAudit(ctx context.Context, transaction *sql.Tx, instanceID, operationID, eventType string, payload any) error {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(operationID) || eventType == "" {
		return fmt.Errorf("invalid audit identity")
	}
	eventID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode audit payload: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(event_id, instance_id, operation_id, event_type, payload_version, payload, created_at)
VALUES(?, ?, ?, ?, 1, ?, ?)`, eventID, instanceID, operationID, eventType, string(encoded), formatTime(time.Now().UTC())); err != nil {
		return classifySQLite("append audit event", err)
	}
	return nil
}

func validateOperation(operation Operation) error {
	if !domain.ValidUUID(operation.ID) || !domain.ValidUUID(operation.InstanceID) || operation.Kind == "" || strings.ContainsAny(operation.Kind, "\r\n") || operation.PayloadVersion < 1 || !json.Valid(operation.RecoveryPayload) || operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid operation")
	}
	return nil
}

func allowedTransition(current, next OperationState) bool {
	switch current {
	case OperationPrepared:
		return next == OperationFilesInstalled || next == OperationRolledBack || next == OperationFailed
	case OperationFilesInstalled:
		return next == OperationCommitted || next == OperationRolledBack || next == OperationFailed
	default:
		return false
	}
}

type rowScanner interface{ Scan(...any) error }

func scanOperation(row rowScanner) (Operation, error) {
	var value Operation
	var payload, created, updated string
	if err := row.Scan(&value.ID, &value.InstanceID, &value.Kind, &value.State, &value.PayloadVersion, &payload, &created, &updated, &value.Failure); err != nil {
		return Operation{}, err
	}
	value.RecoveryPayload = json.RawMessage(payload)
	var err error
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return Operation{}, err
	}
	value.UpdatedAt, err = parseTime(updated)
	if err != nil || !json.Valid(value.RecoveryPayload) {
		return Operation{}, fmt.Errorf("%w: invalid operation payload", ErrSchema)
	}
	return value, nil
}
