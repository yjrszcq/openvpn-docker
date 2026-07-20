package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
)

type AssignmentStatus string
type ArtifactStatus string

const (
	AssignmentActive   AssignmentStatus = "active"
	AssignmentRetained AssignmentStatus = "retained"
	AssignmentReleased AssignmentStatus = "released"
	ArtifactActive     ArtifactStatus   = "active"
	ArtifactStale      ArtifactStatus   = "stale"
	ArtifactDeleted    ArtifactStatus   = "deleted"
)

var artifactKinds = map[string]struct{}{
	"ca-cert": {}, "ca-key": {}, "server-cert": {}, "server-key": {},
	"client-cert": {}, "client-key": {}, "crl": {}, "tls-crypt": {},
	"profile": {}, "ccd": {}, "server-config": {},
}

var instanceArtifactKinds = map[string]struct{}{
	"ca-cert": {}, "ca-key": {}, "server-cert": {}, "server-key": {},
	"crl": {}, "tls-crypt": {}, "server-config": {},
}

var clientArtifactKinds = map[string]struct{}{
	"client-cert": {}, "client-key": {}, "profile": {}, "ccd": {},
}

// AddressAssignment is the authoritative address intent, not a runtime lease.
type AddressAssignment struct {
	ID         string
	NetworkID  string
	Kind       string
	Address    *domain.Address
	Status     AssignmentStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ReleasedAt *time.Time
}

// ClientLease is disposable runtime cache for a dynamic assignment.
type ClientLease struct {
	NetworkID string
	Address   domain.Address
	UpdatedAt time.Time
}

// ArtifactMetadata references local file content without storing it in SQLite.
type ArtifactMetadata struct {
	ID                     string
	OwnerKind              string
	OwnerID                string
	Kind                   string
	Key                    string
	Digest                 [32]byte
	CertificateSerial      string
	CertificateFingerprint []byte
	Status                 ArtifactStatus
}

// ClientState is the persisted client aggregate.
type ClientState struct {
	Client     domain.Client
	CreatedAt  time.Time
	RevokedAt  *time.Time
	DeletedAt  *time.Time
	Assignment *AddressAssignment
	Lease      *ClientLease
	Artifacts  []ArtifactMetadata
}

// ListClients returns current client aggregates in stable name/UUID order.
// Deleted tombstones remain queryable by UUID internally but are not current
// clients and therefore do not appear in the public list or selector surface.
func (store *Store) ListClients(ctx context.Context, instanceID string) ([]ClientState, error) {
	if !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("invalid instance UUID")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT id FROM clients
WHERE instance_id = ? AND status IN ('active', 'revoked')
ORDER BY current_name, id`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil || closeErr != nil {
		return nil, errors.Join(rowErr, closeErr)
	}
	values := make([]ClientState, 0, len(ids))
	for _, id := range ids {
		value, err := store.LoadClient(ctx, instanceID, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// CreateClient stores one complete client aggregate atomically.
func (store *Store) CreateClient(ctx context.Context, instanceID string, state ClientState) error {
	if !domain.ValidUUID(instanceID) {
		return fmt.Errorf("invalid instance UUID")
	}
	if err := validateClientState(state); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin client creation", err)
	}
	defer transaction.Rollback()
	if err := insertClientAggregate(ctx, transaction, instanceID, state); err != nil {
		return err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "client.created", map[string]any{"client_id": state.Client.ID, "name": state.Client.Name}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return classifySQLite("commit client creation", err)
	}
	return nil
}

func insertClientAggregate(ctx context.Context, transaction *sql.Tx, instanceID string, state ClientState) error {
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO clients(id, instance_id, current_name, status, created_at, revoked_at, deleted_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`, state.Client.ID, instanceID, state.Client.Name, state.Client.Status,
		formatTime(state.CreatedAt), nullableTime(state.RevokedAt), nullableTime(state.DeletedAt)); err != nil {
		return classifySQLite("insert client", err)
	}
	if state.Assignment != nil {
		if err := ensureNetworkInstance(ctx, transaction, state.Assignment.NetworkID, instanceID); err != nil {
			return err
		}
		if err := insertAssignment(ctx, transaction, state.Client.ID, *state.Assignment); err != nil {
			return err
		}
	}
	if state.Lease != nil {
		if err := validateLeasePool(ctx, transaction, *state.Lease); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO client_leases(client_id, network_id, family, address, updated_at) VALUES(?, ?, 4, ?, ?)`, state.Client.ID, state.Lease.NetworkID, packAddress(state.Lease.Address.Netip()), formatTime(state.Lease.UpdatedAt)); err != nil {
			return classifySQLite("insert client lease", err)
		}
	}
	for _, artifact := range state.Artifacts {
		if err := insertArtifact(ctx, transaction, instanceID, state.Client.ID, artifact); err != nil {
			return err
		}
	}
	return nil
}

// LoadClient loads and validates one client aggregate.
func (store *Store) LoadClient(ctx context.Context, instanceID, clientID string) (ClientState, error) {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(clientID) {
		return ClientState{}, fmt.Errorf("invalid instance or client UUID")
	}
	var state ClientState
	var name, status, createdAt string
	var revokedAt, deletedAt sql.NullString
	err := store.db.QueryRowContext(ctx, `SELECT current_name, status, created_at, revoked_at, deleted_at FROM clients WHERE instance_id = ? AND id = ?`, instanceID, clientID).Scan(&name, &status, &createdAt, &revokedAt, &deletedAt)
	if err != nil {
		return ClientState{}, fmt.Errorf("load client: %w", err)
	}
	state.Client, err = domain.NewClient(clientID, name, domain.ClientStatus(status))
	if err != nil {
		return ClientState{}, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	state.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return ClientState{}, err
	}
	state.RevokedAt, err = parseNullableTime(revokedAt)
	if err != nil {
		return ClientState{}, err
	}
	state.DeletedAt, err = parseNullableTime(deletedAt)
	if err != nil {
		return ClientState{}, err
	}
	state.Assignment, err = loadCurrentAssignment(ctx, store.db, clientID)
	if err != nil {
		return ClientState{}, err
	}
	state.Lease, err = loadLease(ctx, store.db, clientID)
	if err != nil {
		return ClientState{}, err
	}
	state.Artifacts, err = loadArtifacts(ctx, store.db, instanceID, clientID)
	if err != nil {
		return ClientState{}, err
	}
	if err := validateClientState(state); err != nil {
		return ClientState{}, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	if state.Assignment != nil {
		if err := ensureNetworkInstance(ctx, store.db, state.Assignment.NetworkID, instanceID); err != nil {
			return ClientState{}, fmt.Errorf("%w: assignment network belongs to another instance", ErrSchema)
		}
	}
	if state.Assignment != nil && state.Assignment.Address != nil {
		layout, err := loadNetworkLayout(ctx, store.db, state.Assignment.NetworkID)
		if err != nil || layout.ValidateStatic(*state.Assignment.Address) != nil {
			return ClientState{}, fmt.Errorf("%w: static assignment is outside its pool", ErrSchema)
		}
	}
	if state.Lease != nil {
		if err := validateLeasePool(ctx, store.db, *state.Lease); err != nil {
			return ClientState{}, fmt.Errorf("%w: lease is outside its pool", ErrSchema)
		}
	}
	return state, nil
}

// RecordLease replaces disposable lease cache only for a dynamic assignment.
func (store *Store) RecordLease(ctx context.Context, clientID string, lease ClientLease) error {
	if !domain.ValidUUID(clientID) || lease.Address.Family() != domain.FamilyIPv4 || lease.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid client lease")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin lease update", err)
	}
	defer transaction.Rollback()
	var count int
	if err := transaction.QueryRowContext(ctx, `
SELECT count(*)
FROM address_assignments a
JOIN clients c ON c.id = a.client_id
JOIN networks n ON n.id = a.network_id AND n.instance_id = c.instance_id
WHERE a.client_id = ? AND a.network_id = ? AND a.kind = 'dynamic'
  AND a.status IN ('active', 'retained')`, clientID, lease.NetworkID).Scan(&count); err != nil || count != 1 {
		return fmt.Errorf("dynamic assignment is missing")
	}
	if err := validateLeasePool(ctx, transaction, lease); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
DELETE FROM client_leases
WHERE network_id = ? AND family = 4 AND address = ? AND client_id <> ?`, lease.NetworkID, packAddress(lease.Address.Netip()), clientID); err != nil {
		return classifySQLite("remove stale client lease", err)
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO client_leases(client_id, network_id, family, address, updated_at)
VALUES(?, ?, 4, ?, ?)
ON CONFLICT(client_id, network_id) DO UPDATE SET address = excluded.address, updated_at = excluded.updated_at`, clientID, lease.NetworkID, packAddress(lease.Address.Netip()), formatTime(lease.UpdatedAt)); err != nil {
		return classifySQLite("record client lease", err)
	}
	return classifyCommit(transaction.Commit(), "commit client lease")
}

// RegisterInstanceArtifacts records verified instance-owned file references.
func (store *Store) RegisterInstanceArtifacts(ctx context.Context, instanceID string, artifacts []ArtifactMetadata) error {
	if !domain.ValidUUID(instanceID) || len(artifacts) == 0 {
		return fmt.Errorf("invalid instance artifact registration")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin instance artifact registration", err)
	}
	defer transaction.Rollback()
	for _, artifact := range artifacts {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		if artifact.OwnerKind != "instance" || artifact.OwnerID != instanceID {
			return fmt.Errorf("instance artifact owner mismatch")
		}
		if err := validateArtifactOwnerKind(artifact); err != nil {
			return err
		}
		if err := insertArtifactMetadata(ctx, transaction, instanceID, artifact); err != nil {
			return err
		}
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "artifacts.registered", map[string]any{"count": len(artifacts)}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit instance artifact registration")
}

// LoadInstanceArtifacts returns validated instance-owned artifact references.
func (store *Store) LoadInstanceArtifacts(ctx context.Context, instanceID string) ([]ArtifactMetadata, error) {
	if !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("invalid instance UUID")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT id, owner_kind, owner_id, kind, artifact_key, digest,
       COALESCE(certificate_serial, ''), certificate_fingerprint, status
FROM artifacts
WHERE instance_id = ? AND owner_kind = 'instance' AND owner_id = ?
ORDER BY artifact_key`, instanceID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]ArtifactMetadata, 0)
	for rows.Next() {
		var value ArtifactMetadata
		var digest []byte
		if err := rows.Scan(&value.ID, &value.OwnerKind, &value.OwnerID, &value.Kind, &value.Key, &digest, &value.CertificateSerial, &value.CertificateFingerprint, &value.Status); err != nil {
			return nil, err
		}
		if len(digest) != 32 {
			return nil, fmt.Errorf("%w: invalid artifact digest", ErrSchema)
		}
		copy(value.Digest[:], digest)
		if err := validateArtifact(value); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchema, err)
		}
		if err := validateArtifactOwnerKind(value); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchema, err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func validateClientState(state ClientState) error {
	if _, err := domain.NewClient(state.Client.ID, state.Client.Name, state.Client.Status); err != nil {
		return err
	}
	if state.CreatedAt.IsZero() {
		return fmt.Errorf("client creation time is required")
	}
	switch state.Client.Status {
	case domain.ClientActive:
		if state.RevokedAt != nil || state.DeletedAt != nil {
			return fmt.Errorf("active client has terminal timestamps")
		}
	case domain.ClientRevoked:
		if state.RevokedAt == nil || state.DeletedAt != nil {
			return fmt.Errorf("revoked client timestamps are invalid")
		}
	case domain.ClientDeleted:
		if state.DeletedAt == nil {
			return fmt.Errorf("deleted client timestamp is required")
		}
	}
	if state.Assignment != nil {
		if !domain.ValidUUID(state.Assignment.ID) || !domain.ValidUUID(state.Assignment.NetworkID) || state.Assignment.CreatedAt.IsZero() || state.Assignment.UpdatedAt.IsZero() {
			return fmt.Errorf("invalid address assignment identity")
		}
		switch state.Assignment.Status {
		case AssignmentActive, AssignmentRetained:
			if state.Assignment.ReleasedAt != nil {
				return fmt.Errorf("current assignment has a release time")
			}
		case AssignmentReleased:
			if state.Assignment.ReleasedAt == nil {
				return fmt.Errorf("released assignment has no release time")
			}
		default:
			return fmt.Errorf("invalid assignment status")
		}
		if state.Assignment.Kind == "static" {
			if state.Assignment.Address == nil || state.Assignment.Address.Family() != domain.FamilyIPv4 {
				return fmt.Errorf("static assignment requires IPv4")
			}
		} else if state.Assignment.Kind != "dynamic" || state.Assignment.Address != nil {
			return fmt.Errorf("invalid dynamic assignment")
		}
	}
	if state.Lease != nil {
		if state.Lease.Address.Family() != domain.FamilyIPv4 || state.Lease.UpdatedAt.IsZero() || !domain.ValidUUID(state.Lease.NetworkID) {
			return fmt.Errorf("invalid client lease")
		}
		if state.Assignment == nil || state.Assignment.Kind != "dynamic" || state.Assignment.NetworkID != state.Lease.NetworkID || state.Assignment.Status == AssignmentReleased {
			return fmt.Errorf("lease requires a matching current dynamic assignment")
		}
	}
	for _, artifact := range state.Artifacts {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		if err := validateArtifactOwnerKind(artifact); err != nil {
			return err
		}
	}
	return nil
}

func insertAssignment(ctx context.Context, transaction *sql.Tx, clientID string, assignment AddressAssignment) error {
	var address any
	if assignment.Address != nil {
		address = packAddress(assignment.Address.Netip())
		layout, err := loadNetworkLayout(ctx, transaction, assignment.NetworkID)
		if err != nil {
			return err
		}
		if err := layout.ValidateStatic(*assignment.Address); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO address_assignments(id, client_id, network_id, kind, address, status, created_at, updated_at, released_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, assignment.ID, clientID, assignment.NetworkID, assignment.Kind, address, assignment.Status, formatTime(assignment.CreatedAt), formatTime(assignment.UpdatedAt), nullableTime(assignment.ReleasedAt)); err != nil {
		return classifySQLite("insert address assignment", err)
	}
	return nil
}

func validateLeasePool(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, lease ClientLease) error {
	layout, err := loadNetworkLayout(ctx, query, lease.NetworkID)
	if err != nil {
		return err
	}
	return layout.ValidateDynamicLease(lease.Address)
}

func ensureNetworkInstance(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, networkID, instanceID string) error {
	var count int
	if err := query.QueryRowContext(ctx, "SELECT count(*) FROM networks WHERE id = ? AND instance_id = ?", networkID, instanceID).Scan(&count); err != nil {
		return fmt.Errorf("validate assignment network owner: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("assignment network does not belong to the client instance")
	}
	return nil
}

func loadNetworkLayout(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, networkID string) (ipam.IPv4Layout, error) {
	var packed []byte
	var prefix int
	err := query.QueryRowContext(ctx, "SELECT network, prefix FROM networks WHERE id = ?", networkID).Scan(&packed, &prefix)
	if err != nil {
		return ipam.IPv4Layout{}, fmt.Errorf("load assignment network: %w", err)
	}
	network, err := unpackNetwork(packed, prefix)
	if err != nil {
		return ipam.IPv4Layout{}, err
	}
	// Derive dynamic capacity from canonical pool rows instead of SQL hex math.
	var first, last []byte
	err = query.QueryRowContext(ctx, "SELECT first_address, last_address FROM address_pools WHERE network_id = ? AND kind = 'dynamic'", networkID).Scan(&first, &last)
	if err == sql.ErrNoRows {
		return ipam.NewIPv4Layout(network, 0)
	} else if err != nil {
		return ipam.IPv4Layout{}, err
	} else {
		firstAddress, firstErr := unpackAddress(first)
		lastAddress, lastErr := unpackAddress(last)
		if firstErr != nil || lastErr != nil || addressUint32(lastAddress) < addressUint32(firstAddress) {
			return ipam.IPv4Layout{}, fmt.Errorf("%w: invalid dynamic pool", ErrSchema)
		}
		dynamic := uint64(addressUint32(lastAddress)-addressUint32(firstAddress)) + 1
		return ipam.NewIPv4Layout(network, dynamic)
	}
}

func insertArtifact(ctx context.Context, transaction *sql.Tx, instanceID, clientID string, artifact ArtifactMetadata) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	if artifact.OwnerKind != "client" || artifact.OwnerID != clientID {
		return fmt.Errorf("client artifact owner mismatch")
	}
	if err := validateArtifactOwnerKind(artifact); err != nil {
		return err
	}
	return insertArtifactMetadata(ctx, transaction, instanceID, artifact)
}

func insertArtifactMetadata(ctx context.Context, transaction *sql.Tx, instanceID string, artifact ArtifactMetadata) error {
	var fingerprint any
	if len(artifact.CertificateFingerprint) > 0 {
		fingerprint = artifact.CertificateFingerprint
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO artifacts(id, instance_id, owner_kind, owner_id, kind, backend, artifact_key, digest, certificate_serial, certificate_fingerprint, status)
VALUES(?, ?, ?, ?, ?, 'local', ?, ?, NULLIF(?, ''), ?, ?)`, artifact.ID, instanceID, artifact.OwnerKind, artifact.OwnerID, artifact.Kind, artifact.Key, artifact.Digest[:], artifact.CertificateSerial, fingerprint, artifact.Status); err != nil {
		return classifySQLite("insert artifact metadata", err)
	}
	return nil
}

func validateArtifact(artifact ArtifactMetadata) error {
	if !domain.ValidUUID(artifact.ID) || !domain.ValidUUID(artifact.OwnerID) || (artifact.OwnerKind != "client" && artifact.OwnerKind != "instance") {
		return fmt.Errorf("invalid artifact identity")
	}
	if artifact.Key == "" || strings.HasPrefix(artifact.Key, "/") || path.Clean(artifact.Key) != artifact.Key || artifact.Key == "." || artifact.Key == ".." || strings.HasPrefix(artifact.Key, "../") || strings.Contains(artifact.Key, "\\") {
		return fmt.Errorf("artifact key must be a canonical relative path")
	}
	if len(artifact.CertificateFingerprint) != 0 && len(artifact.CertificateFingerprint) != 32 {
		return fmt.Errorf("artifact certificate fingerprint must contain 32 bytes")
	}
	if _, exists := artifactKinds[artifact.Kind]; !exists {
		return fmt.Errorf("invalid artifact kind")
	}
	switch artifact.Status {
	case ArtifactActive, ArtifactStale, ArtifactDeleted:
	default:
		return fmt.Errorf("invalid artifact status")
	}
	return nil
}

func validateArtifactOwnerKind(artifact ArtifactMetadata) error {
	var allowed map[string]struct{}
	switch artifact.OwnerKind {
	case "instance":
		allowed = instanceArtifactKinds
	case "client":
		allowed = clientArtifactKinds
	default:
		return fmt.Errorf("invalid artifact owner kind")
	}
	if _, exists := allowed[artifact.Kind]; !exists {
		return fmt.Errorf("artifact kind %s is invalid for %s owner", artifact.Kind, artifact.OwnerKind)
	}
	return nil
}

func loadCurrentAssignment(ctx context.Context, database *sql.DB, clientID string) (*AddressAssignment, error) {
	var value AddressAssignment
	var packed []byte
	var created, updated string
	var released sql.NullString
	err := database.QueryRowContext(ctx, `
SELECT id, network_id, kind, address, status, created_at, updated_at, released_at
FROM address_assignments WHERE client_id = ? AND status IN ('active', 'retained')`, clientID).Scan(&value.ID, &value.NetworkID, &value.Kind, &packed, &value.Status, &created, &updated, &released)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return nil, err
	}
	value.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return nil, err
	}
	value.ReleasedAt, err = parseNullableTime(released)
	if err != nil {
		return nil, err
	}
	if packed != nil {
		address, err := unpackAddress(packed)
		if err != nil {
			return nil, err
		}
		value.Address = &address
	}
	return &value, nil
}

func loadLease(ctx context.Context, database *sql.DB, clientID string) (*ClientLease, error) {
	var value ClientLease
	var packed []byte
	var updated string
	err := database.QueryRowContext(ctx, "SELECT network_id, address, updated_at FROM client_leases WHERE client_id = ?", clientID).Scan(&value.NetworkID, &packed, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value.Address, err = unpackAddress(packed)
	if err != nil {
		return nil, err
	}
	value.UpdatedAt, err = parseTime(updated)
	return &value, err
}

func loadArtifacts(ctx context.Context, database *sql.DB, instanceID, clientID string) ([]ArtifactMetadata, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, owner_kind, owner_id, kind, artifact_key, digest, COALESCE(certificate_serial, ''), certificate_fingerprint, status FROM artifacts WHERE instance_id = ? AND owner_kind = 'client' AND owner_id = ? ORDER BY artifact_key`, instanceID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]ArtifactMetadata, 0)
	for rows.Next() {
		var value ArtifactMetadata
		var digest []byte
		if err := rows.Scan(&value.ID, &value.OwnerKind, &value.OwnerID, &value.Kind, &value.Key, &digest, &value.CertificateSerial, &value.CertificateFingerprint, &value.Status); err != nil {
			return nil, err
		}
		if len(digest) != 32 {
			return nil, fmt.Errorf("%w: invalid artifact digest", ErrSchema)
		}
		copy(value.Digest[:], digest)
		values = append(values, value)
	}
	return values, rows.Err()
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid timestamp", ErrSchema)
	}
	return parsed, nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func classifyCommit(err error, operation string) error {
	if err == nil {
		return nil
	}
	return classifySQLite(operation, err)
}
