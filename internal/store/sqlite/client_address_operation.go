package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

type ClientAddressChange struct {
	ClientID     string
	Name         string
	ClientStatus domain.ClientStatus
	Assignment   *AddressAssignment
}

// CommitClientAddressOperation atomically replaces address intent for one or
// more current clients after associated CCD changes have been installed.
func (store *Store) CommitClientAddressOperation(ctx context.Context, operationID, instanceID, kind string, changes []ClientAddressChange, active []ArtifactMetadata, deleted []ArtifactDeletion, recoveryPayload json.RawMessage, updatedAt time.Time) error {
	if !domain.ValidUUID(operationID) || !domain.ValidUUID(instanceID) || (kind != "client.address.set" && kind != "client.address.edit" && kind != "client.address.release") || len(changes) == 0 || !json.Valid(recoveryPayload) || updatedAt.IsZero() {
		return fmt.Errorf("invalid client address operation")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client address operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, operationID, instanceID); err != nil {
		return err
	}
	allowedClients := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if !domain.ValidUUID(change.ClientID) || !domain.ValidClientName(change.Name) || (change.ClientStatus != domain.ClientActive && change.ClientStatus != domain.ClientRevoked) {
			return fmt.Errorf("invalid client address change")
		}
		if _, duplicate := allowedClients[change.ClientID]; duplicate {
			return fmt.Errorf("duplicate client address change")
		}
		allowedClients[change.ClientID] = struct{}{}
		var count int
		if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM clients WHERE id = ? AND instance_id = ? AND current_name = ? AND status = ?`, change.ClientID, instanceID, change.Name, change.ClientStatus).Scan(&count); err != nil || count != 1 {
			return fmt.Errorf("client address source state changed")
		}
		if kind == "client.address.release" && (change.ClientStatus != domain.ClientRevoked || change.Assignment != nil) {
			return fmt.Errorf("address release requires a revoked client")
		}
		if kind != "client.address.release" && change.Assignment == nil {
			return fmt.Errorf("address set/edit requires an assignment")
		}
		if change.Assignment != nil {
			assignment := change.Assignment
			if !domain.ValidUUID(assignment.ID) || !domain.ValidUUID(assignment.NetworkID) || assignment.CreatedAt.IsZero() || assignment.UpdatedAt.IsZero() || (assignment.Kind != "static" && assignment.Kind != "dynamic") || (assignment.Kind == "static" && assignment.Address == nil) || (assignment.Kind == "dynamic" && assignment.Address != nil) {
				return fmt.Errorf("invalid replacement address assignment")
			}
		}
	}
	// Release every selected current row before inserts so an atomic batch may
	// swap static addresses without transient unique conflicts.
	for _, change := range changes {
		if _, err := transaction.ExecContext(ctx, `UPDATE address_assignments SET status = 'released', updated_at = ?, released_at = ? WHERE client_id = ? AND status IN ('active', 'retained')`, formatTime(updatedAt), formatTime(updatedAt), change.ClientID); err != nil {
			return classifySQLite("release previous client assignment", err)
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM client_leases WHERE client_id = ?`, change.ClientID); err != nil {
			return classifySQLite("clear changed client lease", err)
		}
	}
	for _, change := range changes {
		if change.Assignment == nil {
			continue
		}
		assignment := *change.Assignment
		if change.ClientStatus == domain.ClientActive {
			assignment.Status = AssignmentActive
		} else {
			assignment.Status = AssignmentRetained
		}
		assignment.UpdatedAt = updatedAt
		assignment.ReleasedAt = nil
		if err := ensureNetworkInstance(ctx, transaction, assignment.NetworkID, instanceID); err != nil {
			return err
		}
		if err := insertAssignment(ctx, transaction, change.ClientID, assignment); err != nil {
			return err
		}
	}
	for _, deletion := range deleted {
		if deletion.OwnerKind != "client" {
			return fmt.Errorf("address operation may only delete client artifacts")
		}
		if _, ok := allowedClients[deletion.OwnerID]; !ok {
			return fmt.Errorf("address artifact owner is not selected")
		}
		probe := ArtifactMetadata{ID: operationID, OwnerKind: "client", OwnerID: deletion.OwnerID, Kind: "ccd", Key: deletion.Key, Status: ArtifactActive}
		if err := validateArtifact(probe); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE artifacts SET status = 'deleted' WHERE instance_id = ? AND owner_kind = 'client' AND owner_id = ? AND artifact_key = ?`, instanceID, deletion.OwnerID, deletion.Key); err != nil {
			return classifySQLite("retire client CCD metadata", err)
		}
	}
	for _, metadata := range active {
		if metadata.OwnerKind != "client" || metadata.Kind != "ccd" || metadata.Status != ArtifactActive {
			return fmt.Errorf("address operation may only activate client CCD artifacts")
		}
		if _, ok := allowedClients[metadata.OwnerID]; !ok {
			return fmt.Errorf("address artifact owner is not selected")
		}
		if err := upsertActiveArtifact(ctx, transaction, instanceID, metadata); err != nil {
			return err
		}
	}
	if err := commitOperationRow(ctx, transaction, operationID, recoveryPayload, updatedAt); err != nil {
		return err
	}
	for _, change := range changes {
		mode := "released"
		var address any
		if change.Assignment != nil {
			mode = change.Assignment.Kind
			if change.Assignment.Address != nil {
				address = change.Assignment.Address.String()
			}
		}
		if err := appendAudit(ctx, transaction, instanceID, operationID, "client.address_changed", map[string]any{"client_id": change.ClientID, "name": change.Name, "mode": mode, "address": address, "kick_required": change.ClientStatus == domain.ClientActive}); err != nil {
			return err
		}
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "operation.committed", map[string]any{"kind": kind, "count": len(changes)}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit client address operation")
}
