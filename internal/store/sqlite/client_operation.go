package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

// CommitCreateClientOperation atomically creates a client aggregate and
// commits the journal after its files have been installed.
func (store *Store) CommitCreateClientOperation(ctx context.Context, operationID, instanceID string, state ClientState, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || !domain.ValidUUID(instanceID) || !json.Valid(recoveryPayload) || updatedAt.IsZero() {
		return fmt.Errorf("invalid client creation operation")
	}
	if err := validateClientState(state); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client creation operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	if err := insertClientAggregate(ctx, transaction, instanceID, state); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.created", map[string]any{"client_id": state.Client.ID, "name": state.Client.Name}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": "client.create"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client creation operation")
}

// CommitRenameClientOperation atomically changes the current name and profile
// metadata while preserving the stable UUID and all address state.
func (store *Store) CommitRenameClientOperation(ctx context.Context, operationID, instanceID, clientID, oldName, newName string, profile ArtifactMetadata, oldProfile ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || !domain.ValidUUID(instanceID) || !domain.ValidUUID(clientID) || !domain.ValidClientName(oldName) || !domain.ValidClientName(newName) || oldName == newName || !json.Valid(recoveryPayload) || updatedAt.IsZero() {
		return fmt.Errorf("invalid client rename operation")
	}
	if profile.OwnerKind != "client" || profile.OwnerID != clientID || profile.Kind != "profile" || profile.Status != ArtifactActive {
		return fmt.Errorf("invalid renamed profile metadata")
	}
	if err := validateArtifact(profile); err != nil {
		return err
	}
	if oldProfile.OwnerKind != "client" || oldProfile.OwnerID != clientID || oldProfile.Key == profile.Key {
		return fmt.Errorf("invalid old profile deletion")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client rename operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE clients SET current_name = ?
WHERE id = ? AND instance_id = ? AND current_name = ? AND status IN ('active', 'revoked')`, newName, clientID, instanceID, oldName)
	if err != nil {
		return classifySQLite("rename client", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("client rename source state changed")
	}
	if _, err := transaction.ExecContext(ctx, `
UPDATE artifacts SET status = 'deleted'
WHERE instance_id = ? AND owner_kind = 'client' AND owner_id = ? AND backend = 'local' AND artifact_key = ?`, instanceID, clientID, oldProfile.Key); err != nil {
		return classifySQLite("retire old client profile metadata", err)
	}
	if err := upsertActiveArtifact(ctx, transaction, instanceID, profile); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.renamed", map[string]any{"client_id": clientID, "old_name": oldName, "new_name": newName}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": "client.rename"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client rename operation")
}

func requireFilesInstalledOperation(ctx context.Context, transaction *sql.Tx, operationID, instanceID string) error {
	var storedInstance string
	var state OperationState
	if err := transaction.QueryRowContext(ctx, "SELECT instance_id, state FROM operations WHERE id = ?", operationID).Scan(&storedInstance, &state); err != nil {
		return fmt.Errorf("load client operation: %w", err)
	}
	if storedInstance != instanceID || state != OperationFilesInstalled {
		return fmt.Errorf("client operation is not files-installed for this instance")
	}
	return nil
}

func commitOperationRow(ctx context.Context, transaction *sql.Tx, operationID string, payload json.RawMessage, updatedAt time.Time) error {
	result, err := transaction.ExecContext(ctx, `
UPDATE operations SET state = 'committed', recovery_payload = ?, updated_at = ?, failure = NULL
WHERE id = ? AND state = 'files-installed'`, string(payload), formatTime(updatedAt), operationID)
	if err != nil {
		return classifySQLite("commit client operation journal", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("client operation state changed concurrently")
	}
	return nil
}

func upsertActiveArtifact(ctx context.Context, transaction *sql.Tx, instanceID string, metadata ArtifactMetadata) error {
	if err := validateArtifact(metadata); err != nil {
		return err
	}
	if err := validateArtifactOwnerKind(metadata); err != nil {
		return err
	}
	var fingerprint any
	if len(metadata.CertificateFingerprint) > 0 {
		fingerprint = metadata.CertificateFingerprint
	}
	result, err := transaction.ExecContext(ctx, `
INSERT INTO artifacts(id, instance_id, owner_kind, owner_id, kind, backend, artifact_key, digest, certificate_serial, certificate_fingerprint, status)
VALUES(?, ?, ?, ?, ?, 'local', ?, ?, NULLIF(?, ''), ?, 'active')
ON CONFLICT(backend, artifact_key) WHERE status IN ('active', 'stale') DO UPDATE SET
    digest = excluded.digest,
    certificate_serial = excluded.certificate_serial,
    certificate_fingerprint = excluded.certificate_fingerprint,
    status = 'active'
WHERE artifacts.instance_id = excluded.instance_id
  AND artifacts.owner_kind = excluded.owner_kind
  AND artifacts.owner_id = excluded.owner_id
  AND artifacts.kind = excluded.kind`, metadata.ID, instanceID, metadata.OwnerKind, metadata.OwnerID, metadata.Kind, metadata.Key, metadata.Digest[:], metadata.CertificateSerial, fingerprint)
	if err != nil {
		return classifySQLite("upsert active artifact", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("artifact key %s belongs to another owner or kind", metadata.Key)
	}
	return nil
}
