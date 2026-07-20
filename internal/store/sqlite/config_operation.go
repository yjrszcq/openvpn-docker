package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

type ConfigurationAddressChange struct {
	ClientID     string
	Name         string
	ClientStatus domain.ClientStatus
	Assignment   AddressAssignment
}

type ConfigurationCommit struct {
	OperationID      string
	InstanceID       string
	ExpectedRevision configservice.Revision
	ExpectedDigest   string
	Snapshot         configservice.AppliedSnapshot
	NewNetworkID     string
	AddressChanges   []ConfigurationAddressChange
	ActiveArtifacts  []ArtifactMetadata
	DeletedArtifacts []ArtifactDeletion
	RecoveryPayload  json.RawMessage
	UpdatedAt        time.Time
}

// CommitConfigurationOperation advances applied configuration, address state,
// artifact metadata, audit, and the durable journal in one SQLite transaction
// after all corresponding filesystem changes have been installed.
func (store *Store) CommitConfigurationOperation(ctx context.Context, change ConfigurationCommit) error {
	if !domain.ValidUUID(change.OperationID) || !domain.ValidUUID(change.InstanceID) || change.ExpectedRevision == 0 || change.ExpectedDigest == "" || !json.Valid(change.RecoveryPayload) || change.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid configuration operation")
	}
	if err := validateApplied(change.Snapshot); err != nil {
		return err
	}
	if change.Snapshot.Revision != change.ExpectedRevision+1 {
		return fmt.Errorf("configuration revision must advance by one")
	}
	replaceNetworkState := change.NewNetworkID != ""
	if replaceNetworkState != (change.AddressChanges != nil) || (replaceNetworkState && !domain.ValidUUID(change.NewNetworkID)) {
		return fmt.Errorf("invalid configuration network transition")
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin configuration operation", err)
	}
	defer transaction.Rollback()
	if err := requireFilesInstalledOperation(ctx, transaction, change.OperationID, change.InstanceID); err != nil {
		return err
	}
	var currentRevision uint64
	var currentDigest []byte
	if err := transaction.QueryRowContext(ctx, `
SELECT i.current_applied_revision, c.digest
FROM instances i JOIN applied_config c
  ON c.instance_id = i.id AND c.revision = i.current_applied_revision
WHERE i.id = ?`, change.InstanceID).Scan(&currentRevision, &currentDigest); err != nil {
		return fmt.Errorf("load configuration operation source: %w", err)
	}
	if currentRevision != uint64(change.ExpectedRevision) || hex.EncodeToString(currentDigest) != change.ExpectedDigest {
		return fmt.Errorf("configuration source revision changed")
	}

	if replaceNetworkState {
		if err := validateConfigurationAddresses(ctx, transaction, change); err != nil {
			return err
		}
	}
	if err := insertAppliedConfig(ctx, transaction, change.InstanceID, change.Snapshot); err != nil {
		return err
	}
	if err := replaceRoutesAndDNS(ctx, transaction, change.InstanceID, change.Snapshot.Config); err != nil {
		return err
	}
	if replaceNetworkState {
		if _, err := replaceNetwork(ctx, transaction, change.InstanceID, change.NewNetworkID, change.Snapshot.Config); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE address_assignments
SET status = 'released', updated_at = ?, released_at = ?
WHERE status IN ('active', 'retained')
  AND client_id IN (SELECT id FROM clients WHERE instance_id = ?)`, formatTime(change.UpdatedAt), formatTime(change.UpdatedAt), change.InstanceID); err != nil {
			return classifySQLite("release previous configuration assignments", err)
		}
		if _, err := transaction.ExecContext(ctx, `DELETE FROM client_leases WHERE client_id IN (SELECT id FROM clients WHERE instance_id = ?)`, change.InstanceID); err != nil {
			return classifySQLite("clear configuration leases", err)
		}
		for _, item := range change.AddressChanges {
			assignment := item.Assignment
			assignment.NetworkID = change.NewNetworkID
			assignment.UpdatedAt = change.UpdatedAt
			assignment.ReleasedAt = nil
			if item.ClientStatus == domain.ClientActive {
				assignment.Status = AssignmentActive
			} else {
				assignment.Status = AssignmentRetained
			}
			if err := insertAssignment(ctx, transaction, item.ClientID, assignment); err != nil {
				return err
			}
		}
	}
	if err := commitConfigurationArtifacts(ctx, transaction, change); err != nil {
		return err
	}
	if err := advanceAppliedRevision(ctx, transaction, change.InstanceID, change.Snapshot.Revision); err != nil {
		return err
	}
	if err := commitOperationRow(ctx, transaction, change.OperationID, change.RecoveryPayload, change.UpdatedAt); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, change.InstanceID, change.OperationID, "config.applied", map[string]any{
		"revision": change.Snapshot.Revision,
		"digest":   change.Snapshot.Digest,
		"remapped": len(change.AddressChanges),
	}); err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, change.InstanceID, change.OperationID, "operation.committed", map[string]any{"kind": "config.apply"}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit configuration operation")
}

func validateConfigurationAddresses(ctx context.Context, transaction *sql.Tx, change ConfigurationCommit) error {
	var count int
	if err := transaction.QueryRowContext(ctx, `
SELECT count(*) FROM address_assignments a JOIN clients c ON c.id = a.client_id
WHERE c.instance_id = ? AND a.status IN ('active', 'retained')`, change.InstanceID).Scan(&count); err != nil {
		return fmt.Errorf("count configuration assignments: %w", err)
	}
	if count != len(change.AddressChanges) {
		return fmt.Errorf("configuration assignment source changed")
	}
	seen := make(map[string]struct{}, len(change.AddressChanges))
	for _, item := range change.AddressChanges {
		if !domain.ValidUUID(item.ClientID) || !domain.ValidClientName(item.Name) || (item.ClientStatus != domain.ClientActive && item.ClientStatus != domain.ClientRevoked) || !domain.ValidUUID(item.Assignment.ID) || item.Assignment.CreatedAt.IsZero() || item.Assignment.UpdatedAt.IsZero() || (item.Assignment.Kind != "static" && item.Assignment.Kind != "dynamic") || (item.Assignment.Kind == "static" && item.Assignment.Address == nil) || (item.Assignment.Kind == "dynamic" && item.Assignment.Address != nil) {
			return fmt.Errorf("invalid configuration assignment change")
		}
		if _, duplicate := seen[item.ClientID]; duplicate {
			return fmt.Errorf("duplicate configuration assignment change")
		}
		seen[item.ClientID] = struct{}{}
		var current int
		if err := transaction.QueryRowContext(ctx, `
SELECT count(*) FROM clients c JOIN address_assignments a ON a.client_id = c.id
WHERE c.id = ? AND c.instance_id = ? AND c.current_name = ? AND c.status = ?
  AND a.status IN ('active', 'retained')`, item.ClientID, change.InstanceID, item.Name, item.ClientStatus).Scan(&current); err != nil || current != 1 {
			return fmt.Errorf("configuration assignment source changed")
		}
	}
	return nil
}

func commitConfigurationArtifacts(ctx context.Context, transaction *sql.Tx, change ConfigurationCommit) error {
	seen := make(map[string]struct{}, len(change.ActiveArtifacts)+len(change.DeletedArtifacts))
	for _, metadata := range change.ActiveArtifacts {
		if metadata.Status != ArtifactActive {
			return fmt.Errorf("configuration artifact must be active")
		}
		if err := validateArtifact(metadata); err != nil {
			return err
		}
		if err := validateArtifactOwnerKind(metadata); err != nil {
			return err
		}
		if err := validateArtifactOperationOwner(ctx, transaction, change.InstanceID, metadata.OwnerKind, metadata.OwnerID); err != nil {
			return err
		}
		identity := metadata.OwnerKind + "\x00" + metadata.OwnerID + "\x00" + metadata.Key
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate configuration artifact key %s", metadata.Key)
		}
		seen[identity] = struct{}{}
		if err := upsertActiveArtifact(ctx, transaction, change.InstanceID, metadata); err != nil {
			return err
		}
	}
	for _, deletion := range change.DeletedArtifacts {
		if err := validateArtifactOperationOwner(ctx, transaction, change.InstanceID, deletion.OwnerKind, deletion.OwnerID); err != nil {
			return err
		}
		probe := ArtifactMetadata{ID: change.OperationID, OwnerKind: deletion.OwnerKind, OwnerID: deletion.OwnerID, Kind: "ccd", Key: deletion.Key, Status: ArtifactActive}
		if deletion.OwnerKind == "instance" {
			probe.Kind = "server-config"
		}
		if err := validateArtifact(probe); err != nil {
			return err
		}
		identity := deletion.OwnerKind + "\x00" + deletion.OwnerID + "\x00" + deletion.Key
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate configuration artifact key %s", deletion.Key)
		}
		seen[identity] = struct{}{}
		if _, err := transaction.ExecContext(ctx, `
UPDATE artifacts SET status = 'deleted'
WHERE instance_id = ? AND owner_kind = ? AND owner_id = ? AND backend = 'local' AND artifact_key = ?`, change.InstanceID, deletion.OwnerKind, deletion.OwnerID, deletion.Key); err != nil {
			return classifySQLite("retire configuration artifact", err)
		}
	}
	return nil
}
