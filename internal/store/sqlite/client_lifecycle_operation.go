package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

// CommitRevokeClientOperation atomically revokes an active client, transitions
// its current address intent, updates artifact metadata, and commits the
// already-installed file operation.
func (store *Store) CommitRevokeClientOperation(ctx context.Context, operationID, instanceID, clientID, name string, releaseAddress bool, active []ArtifactMetadata, deleted []ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if err := validateLifecycleInput(operationID, instanceID, clientID, name, recoveryPayload, updatedAt); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client revoke operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE clients SET status = 'revoked', revoked_at = ?, deleted_at = NULL WHERE id = ? AND instance_id = ? AND current_name = ? AND status = 'active'`, formatTime(updatedAt), clientID, instanceID, name)
	if err != nil {
		return classifySQLite("revoke client", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("client revoke source state changed")
	}
	if releaseAddress {
		if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'released', updated_at = ?, released_at = ? WHERE client_id = ? AND status IN ('active', 'retained')`, formatTime(updatedAt), formatTime(updatedAt), clientID); err != nil {
			return classifySQLite("release revoked client address", err)
		}
	} else if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'retained', updated_at = ?, released_at = NULL WHERE client_id = ? AND status IN ('active', 'retained')`, formatTime(updatedAt), clientID); err != nil {
		return classifySQLite("retain revoked client address", err)
	}
	if err := clearLeaseAndApplyArtifacts(ctx, transaction, instanceID, clientID, active, deleted); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.revoked", map[string]any{"client_id": clientID, "name": name, "address_released": releaseAddress, "kick_required": true}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": "client.revoke"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client revoke operation")
}

// CommitReissueClientOperation atomically activates replacement credentials
// and the selected current address assignment.
func (store *Store) CommitReissueClientOperation(ctx context.Context, operationID, instanceID, clientID, name string, sourceStatus domain.ClientStatus, assignment AddressAssignment, active []ArtifactMetadata, deleted []ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if err := validateLifecycleInput(operationID, instanceID, clientID, name, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if sourceStatus != domain.ClientActive && sourceStatus != domain.ClientRevoked {
		return fmt.Errorf("invalid reissue source state")
	}
	if !domain.ValidUUID(assignment.ID) || assignment.Status != AssignmentActive {
		return fmt.Errorf("invalid reissue address assignment")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client reissue operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE clients SET status = 'active', revoked_at = NULL, deleted_at = NULL WHERE id = ? AND instance_id = ? AND current_name = ? AND status = ?`, clientID, instanceID, name, sourceStatus)
	if err != nil {
		return classifySQLite("activate reissued client", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("client reissue source state changed")
	}
	var currentID string
	queryErr := transaction.QueryRowContext(ctx, `SELECT id FROM address_assignments WHERE client_id = ? AND status IN ('active', 'retained')`, clientID).Scan(&currentID)
	switch {
	case queryErr == nil && currentID == assignment.ID:
		if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'active', updated_at = ?, released_at = NULL WHERE id = ?`, formatTime(updatedAt), assignment.ID); err != nil {
			return classifySQLite("reactivate retained assignment", err)
		}
	case queryErr == nil || queryErr == sql.ErrNoRows:
		if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'released', updated_at = ?, released_at = ? WHERE client_id = ? AND status IN ('active', 'retained')`, formatTime(updatedAt), formatTime(updatedAt), clientID); err != nil {
			return classifySQLite("release previous reissue assignment", err)
		}
		if err := ensureNetworkInstance(ctx, transaction, assignment.NetworkID, instanceID); err != nil {
			return err
		}
		if err := insertAssignment(ctx, transaction, clientID, assignment); err != nil {
			return err
		}
	default:
		return classifySQLite("load current reissue assignment", queryErr)
	}
	if err := clearLeaseAndApplyArtifacts(ctx, transaction, instanceID, clientID, active, deleted); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.reissued", map[string]any{"client_id": clientID, "name": name, "source_status": sourceStatus, "assignment_id": assignment.ID, "kick_required": true}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": "client.reissue"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client reissue operation")
}

// CommitDeleteClientOperation preserves the client row as a reusable-name
// tombstone while releasing current address state and retiring credentials.
func (store *Store) CommitDeleteClientOperation(ctx context.Context, operationID, instanceID, clientID, name string, sourceStatus domain.ClientStatus, active []ArtifactMetadata, deleted []ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if err := validateLifecycleInput(operationID, instanceID, clientID, name, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if sourceStatus != domain.ClientActive && sourceStatus != domain.ClientRevoked {
		return fmt.Errorf("invalid delete source state")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client delete operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE clients SET status = 'deleted', revoked_at = COALESCE(revoked_at, ?), deleted_at = ? WHERE id = ? AND instance_id = ? AND current_name = ? AND status = ?`, formatTime(updatedAt), formatTime(updatedAt), clientID, instanceID, name, sourceStatus)
	if err != nil {
		return classifySQLite("delete client", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("client delete source state changed")
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'released', updated_at = ?, released_at = ? WHERE client_id = ? AND status IN ('active', 'retained')`, formatTime(updatedAt), formatTime(updatedAt), clientID); err != nil {
		return classifySQLite("release deleted client address", err)
	}
	if err := clearLeaseAndApplyArtifacts(ctx, transaction, instanceID, clientID, active, deleted); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.deleted", map[string]any{"client_id": clientID, "name": name, "source_status": sourceStatus, "kick_required": sourceStatus == domain.ClientActive}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": "client.delete"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client delete operation")
}

func validateLifecycleInput(operationID, instanceID, clientID, name string, payload json.RawMessage, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || !domain.ValidUUID(instanceID) || !domain.ValidUUID(clientID) || !domain.ValidClientName(name) || !json.Valid(payload) || updatedAt.IsZero() {
		return fmt.Errorf("invalid client lifecycle operation")
	}
	return nil
}

func clearLeaseAndApplyArtifacts(ctx context.Context, transaction *sql.Tx, instanceID, clientID string, active []ArtifactMetadata, deleted []ArtifactDeletion) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM client_leases WHERE client_id = ?`, clientID); err != nil {
		return classifySQLite("clear client lease", err)
	}
	for _, value := range deleted {
		if (value.OwnerKind == "client" && value.OwnerID != clientID) || (value.OwnerKind == "instance" && value.OwnerID != instanceID) || (value.OwnerKind != "client" && value.OwnerKind != "instance") {
			return fmt.Errorf("invalid lifecycle artifact deletion")
		}
		probeKind := "ccd"
		if value.OwnerKind == "instance" {
			probeKind = "server-config"
		}
		probe := ArtifactMetadata{ID: clientID, OwnerKind: value.OwnerKind, OwnerID: value.OwnerID, Kind: probeKind, Key: value.Key, Status: ArtifactActive}
		if err := validateArtifact(probe); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE artifacts SET status = 'deleted' WHERE instance_id = ? AND owner_kind = ? AND owner_id = ? AND backend = 'local' AND artifact_key = ?`, instanceID, value.OwnerKind, value.OwnerID, value.Key); err != nil {
			return classifySQLite("retire lifecycle artifact", err)
		}
	}
	for _, value := range active {
		if value.Status != ArtifactActive || (value.OwnerKind == "client" && value.OwnerID != clientID) || (value.OwnerKind == "instance" && value.OwnerID != instanceID) || (value.OwnerKind != "client" && value.OwnerKind != "instance") {
			return fmt.Errorf("invalid active lifecycle artifact")
		}
		if err := upsertActiveArtifact(ctx, transaction, instanceID, value); err != nil {
			return err
		}
	}
	return nil
}
