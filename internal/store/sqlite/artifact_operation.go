package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

type ArtifactDeletion struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id"`
	Key       string `json:"key"`
}

// CommitArtifactOperation atomically applies artifact metadata and commits its
// already-files-installed operation journal entry.
func (store *Store) CommitArtifactOperation(ctx context.Context, operationID string, active []ArtifactMetadata, deleted []ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || (!json.Valid(recoveryPayload)) || updatedAt.IsZero() || (len(active) == 0 && len(deleted) == 0) {
		return fmt.Errorf("invalid artifact operation commit")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin artifact operation commit", err)
	}
	defer transaction.Rollback()
	var instanceID, operationKind string
	var state OperationState
	if err := transaction.QueryRowContext(ctx, "SELECT instance_id, kind, state FROM operations WHERE id = ?", operationID).Scan(&instanceID, &operationKind, &state); err != nil {
		return fmt.Errorf("load artifact operation: %w", err)
	}
	if state != OperationFilesInstalled {
		return fmt.Errorf("artifact operation must be files-installed, got %s", state)
	}
	seen := make(map[string]struct{}, len(active)+len(deleted))
	for _, metadata := range active {
		if metadata.Status != ArtifactActive {
			return fmt.Errorf("refreshed artifact must be active")
		}
		if err := validateArtifact(metadata); err != nil {
			return err
		}
		if err := validateArtifactOwnerKind(metadata); err != nil {
			return err
		}
		if err := validateArtifactOperationOwner(ctx, transaction, instanceID, metadata.OwnerKind, metadata.OwnerID); err != nil {
			return err
		}
		identity := metadata.OwnerKind + "\x00" + metadata.OwnerID + "\x00" + metadata.Key
		if _, exists := seen[identity]; exists {
			return fmt.Errorf("artifact operation contains duplicate key %s", metadata.Key)
		}
		seen[identity] = struct{}{}
		var fingerprint any
		if len(metadata.CertificateFingerprint) > 0 {
			fingerprint = metadata.CertificateFingerprint
		}
		result, err := transaction.ExecContext(ctx, `
INSERT INTO artifacts(id, instance_id, owner_kind, owner_id, kind, backend, artifact_key, digest, certificate_serial, certificate_fingerprint, status)
VALUES(?, ?, ?, ?, ?, 'local', ?, ?, NULLIF(?, ''), ?, 'active')
ON CONFLICT(backend, artifact_key) DO UPDATE SET
    digest = excluded.digest,
    certificate_serial = excluded.certificate_serial,
    certificate_fingerprint = excluded.certificate_fingerprint,
    status = 'active'
WHERE artifacts.instance_id = excluded.instance_id
  AND artifacts.owner_kind = excluded.owner_kind
  AND artifacts.owner_id = excluded.owner_id
  AND artifacts.kind = excluded.kind`, metadata.ID, instanceID, metadata.OwnerKind, metadata.OwnerID, metadata.Kind, metadata.Key, metadata.Digest[:], metadata.CertificateSerial, fingerprint)
		if err != nil {
			return classifySQLite("upsert artifact metadata", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return fmt.Errorf("artifact key %s belongs to another owner or kind", metadata.Key)
		}
	}
	for _, deletion := range deleted {
		probeKind := "ccd"
		if deletion.OwnerKind == "instance" {
			probeKind = "server-config"
		}
		probe := ArtifactMetadata{ID: operationID, OwnerKind: deletion.OwnerKind, OwnerID: deletion.OwnerID, Kind: probeKind, Key: deletion.Key, Status: ArtifactActive}
		if err := validateArtifact(probe); err != nil {
			return err
		}
		if err := validateArtifactOwnerKind(probe); err != nil {
			return err
		}
		if err := validateArtifactOperationOwner(ctx, transaction, instanceID, deletion.OwnerKind, deletion.OwnerID); err != nil {
			return err
		}
		identity := deletion.OwnerKind + "\x00" + deletion.OwnerID + "\x00" + deletion.Key
		if _, exists := seen[identity]; exists {
			return fmt.Errorf("artifact operation contains duplicate key %s", deletion.Key)
		}
		seen[identity] = struct{}{}
		if _, err := transaction.ExecContext(ctx, `
UPDATE artifacts SET status = 'deleted'
WHERE instance_id = ? AND owner_kind = ? AND owner_id = ? AND backend = 'local' AND artifact_key = ?`, instanceID, deletion.OwnerKind, deletion.OwnerID, deletion.Key); err != nil {
			return classifySQLite("delete artifact metadata", err)
		}
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE operations
SET state = 'committed', recovery_payload = ?, updated_at = ?, failure = NULL
WHERE id = ? AND state = 'files-installed'`, string(recoveryPayload), formatTime(updatedAt), operationID)
	if err != nil {
		return classifySQLite("commit artifact operation", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("artifact operation state changed concurrently")
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "artifacts.refreshed", map[string]any{"active": len(active), "deleted": len(deleted)}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": operationKind}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit artifact metadata transaction")
}

func validateArtifactOperationOwner(ctx context.Context, transaction *sql.Tx, instanceID, ownerKind, ownerID string) error {
	switch ownerKind {
	case "instance":
		if ownerID != instanceID {
			return fmt.Errorf("instance artifact owner mismatch")
		}
	case "client":
		var count int
		if err := transaction.QueryRowContext(ctx, "SELECT count(*) FROM clients WHERE id = ? AND instance_id = ?", ownerID, instanceID).Scan(&count); err != nil || count != 1 {
			return fmt.Errorf("client artifact owner does not belong to operation instance")
		}
	default:
		return fmt.Errorf("invalid artifact owner kind")
	}
	return nil
}
