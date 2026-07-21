package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

// ImportedAuditEvent is immutable legacy evidence copied into schema 4.
type ImportedAuditEvent struct {
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// ImportAuditEvents appends legacy evidence and one completion record in a
// single transaction. Business aggregates are already isolated in a staging DB.
func (store *Store) ImportAuditEvents(ctx context.Context, instanceID string, events []ImportedAuditEvent, importedAt time.Time) error {
	if !domain.ValidUUID(instanceID) || importedAt.IsZero() {
		return fmt.Errorf("invalid legacy audit import")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin legacy audit import", err)
	}
	defer transaction.Rollback()
	for _, event := range events {
		if event.Type == "" || !json.Valid(event.Payload) || event.CreatedAt.IsZero() {
			return fmt.Errorf("invalid imported audit event")
		}
		operationID, err := domain.GenerateUUID()
		if err != nil {
			return err
		}
		eventID, err := domain.GenerateUUID()
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(event_id, instance_id, operation_id, event_type, payload_version, payload, created_at)
VALUES(?, ?, ?, ?, 1, ?, ?)`, eventID, instanceID, operationID, event.Type, string(event.Payload), formatTime(event.CreatedAt)); err != nil {
			return classifySQLite("import legacy audit event", err)
		}
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	if err := appendAuditAt(ctx, transaction, instanceID, operationID, "migration.schema3_imported", map[string]any{"events": len(events)}, importedAt); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit legacy audit import")
}

func appendAuditAt(ctx context.Context, transaction *sql.Tx, instanceID, operationID, eventType string, payload any, createdAt time.Time) error {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(operationID) || eventType == "" || createdAt.IsZero() {
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
	if _, err := transaction.ExecContext(ctx, `INSERT INTO audit_events(event_id, instance_id, operation_id, event_type, payload_version, payload, created_at) VALUES(?, ?, ?, ?, 1, ?, ?)`, eventID, instanceID, operationID, eventType, string(encoded), formatTime(createdAt)); err != nil {
		return classifySQLite("append audit event", err)
	}
	return nil
}
