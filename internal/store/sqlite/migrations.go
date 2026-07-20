package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	revision int
	apply    func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{revision: 2, apply: migrateConfigurationState},
	{revision: 3, apply: migrateClientState},
	{revision: 4, apply: migrateAuditState},
	{revision: 5, apply: migrateLeaseUniqueness},
	{revision: 6, apply: migrateCAKeyArtifact},
	{revision: 7, apply: migrateReusableArtifactKeys},
	{revision: 8, apply: migrateHistoricalNetworks},
}

func migrateHistoricalNetworks(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
ALTER TABLE address_pools RENAME TO address_pools_v7;
ALTER TABLE address_assignments RENAME TO address_assignments_v7;
ALTER TABLE client_leases RENAME TO client_leases_v7;
ALTER TABLE networks RENAME TO networks_v7;
DROP INDEX IF EXISTS networks_enabled_purpose;
DROP INDEX assignments_current_client_network;
DROP INDEX assignments_current_network_address;
DROP INDEX client_leases_network_address;

CREATE TABLE networks (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    family INTEGER NOT NULL CHECK (family = 4),
    network BLOB NOT NULL CHECK (length(network) = 4),
    prefix INTEGER NOT NULL CHECK (prefix BETWEEN 0 AND 30),
    purpose TEXT NOT NULL CHECK (purpose = 'tunnel'),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
) STRICT;
CREATE UNIQUE INDEX networks_enabled_purpose
ON networks(instance_id, family, purpose) WHERE enabled = 1;

CREATE TABLE address_pools (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('static', 'dynamic')),
    first_address BLOB NOT NULL CHECK (length(first_address) = 4),
    last_address BLOB NOT NULL CHECK (length(last_address) = 4),
    policy TEXT NOT NULL CHECK (policy IN ('lowest-free', 'openvpn-dynamic')),
    CHECK (first_address <= last_address),
    UNIQUE (network_id, kind)
) STRICT;

CREATE TABLE address_assignments (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('static', 'dynamic')),
    address BLOB CHECK (address IS NULL OR length(address) = 4),
    status TEXT NOT NULL CHECK (status IN ('active', 'retained', 'released')),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    updated_at TEXT NOT NULL CHECK (length(updated_at) > 0),
    released_at TEXT,
    CHECK ((kind = 'static' AND address IS NOT NULL) OR (kind = 'dynamic' AND address IS NULL)),
    CHECK ((status = 'released' AND released_at IS NOT NULL) OR (status != 'released' AND released_at IS NULL))
) STRICT;
CREATE UNIQUE INDEX assignments_current_client_network
ON address_assignments(client_id, network_id) WHERE status IN ('active', 'retained');
CREATE UNIQUE INDEX assignments_current_network_address
ON address_assignments(network_id, address) WHERE address IS NOT NULL AND status IN ('active', 'retained');

CREATE TABLE client_leases (
    client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    family INTEGER NOT NULL CHECK (family = 4),
    address BLOB NOT NULL CHECK (length(address) = 4),
    updated_at TEXT NOT NULL CHECK (length(updated_at) > 0),
    PRIMARY KEY (client_id, network_id)
) STRICT;
CREATE UNIQUE INDEX client_leases_network_address
ON client_leases(network_id, address);

INSERT INTO networks SELECT * FROM networks_v7;
INSERT INTO address_pools SELECT * FROM address_pools_v7;
INSERT INTO address_assignments SELECT * FROM address_assignments_v7;
INSERT INTO client_leases SELECT * FROM client_leases_v7;

DROP TABLE client_leases_v7;
DROP TABLE address_assignments_v7;
DROP TABLE address_pools_v7;
DROP TABLE networks_v7;`)
	if err != nil {
		return classifySQLite("migrate historical network state", err)
	}
	return nil
}

func migrateReusableArtifactKeys(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
CREATE TABLE artifacts_v7 (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('instance', 'client')),
    owner_id TEXT NOT NULL CHECK (length(owner_id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('ca-cert', 'ca-key', 'server-cert', 'server-key', 'client-cert', 'client-key', 'crl', 'tls-crypt', 'profile', 'ccd', 'server-config')),
    backend TEXT NOT NULL CHECK (backend = 'local'),
    artifact_key TEXT NOT NULL CHECK (length(artifact_key) > 0),
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    certificate_serial TEXT,
    certificate_fingerprint BLOB CHECK (certificate_fingerprint IS NULL OR length(certificate_fingerprint) = 32),
    status TEXT NOT NULL CHECK (status IN ('active', 'stale', 'deleted'))
) STRICT;
INSERT INTO artifacts_v7 SELECT * FROM artifacts;
DROP TABLE artifacts;
ALTER TABLE artifacts_v7 RENAME TO artifacts;
CREATE UNIQUE INDEX artifacts_current_key
ON artifacts(backend, artifact_key) WHERE status IN ('active', 'stale')`)
	if err != nil {
		return classifySQLite("migrate reusable artifact keys", err)
	}
	return nil
}

func migrateCAKeyArtifact(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
CREATE TABLE artifacts_v6 (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('instance', 'client')),
    owner_id TEXT NOT NULL CHECK (length(owner_id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('ca-cert', 'ca-key', 'server-cert', 'server-key', 'client-cert', 'client-key', 'crl', 'tls-crypt', 'profile', 'ccd', 'server-config')),
    backend TEXT NOT NULL CHECK (backend = 'local'),
    artifact_key TEXT NOT NULL CHECK (length(artifact_key) > 0),
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    certificate_serial TEXT,
    certificate_fingerprint BLOB CHECK (certificate_fingerprint IS NULL OR length(certificate_fingerprint) = 32),
    status TEXT NOT NULL CHECK (status IN ('active', 'stale', 'deleted')),
    UNIQUE (backend, artifact_key)
) STRICT;
INSERT INTO artifacts_v6 SELECT * FROM artifacts;
DROP TABLE artifacts;
ALTER TABLE artifacts_v6 RENAME TO artifacts`)
	if err != nil {
		return classifySQLite("migrate CA key artifact metadata", err)
	}
	return nil
}

func migrateLeaseUniqueness(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
DELETE FROM client_leases
WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               row_number() OVER (
                   PARTITION BY network_id, address
                   ORDER BY updated_at DESC, client_id DESC
               ) AS duplicate_rank
        FROM client_leases
    )
    WHERE duplicate_rank > 1
);
CREATE UNIQUE INDEX client_leases_network_address
ON client_leases(network_id, address)`)
	if err != nil {
		return classifySQLite("migrate client lease uniqueness", err)
	}
	return nil
}

func migrateAuditState(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
CREATE TABLE audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK (length(event_id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE RESTRICT,
    operation_id TEXT NOT NULL CHECK (length(operation_id) = 36),
    event_type TEXT NOT NULL CHECK (length(event_type) > 0),
    payload_version INTEGER NOT NULL CHECK (payload_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0)
) STRICT;

CREATE TABLE operations (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (length(kind) > 0),
    state TEXT NOT NULL CHECK (state IN ('prepared', 'files-installed', 'committed', 'rolled-back', 'failed')),
    payload_version INTEGER NOT NULL CHECK (payload_version > 0),
    recovery_payload TEXT NOT NULL CHECK (json_valid(recovery_payload)),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    updated_at TEXT NOT NULL CHECK (length(updated_at) > 0),
    failure TEXT
) STRICT`)
	if err != nil {
		return classifySQLite("migrate audit state", err)
	}
	return nil
}

func migrateClientState(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
CREATE TABLE clients (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    current_name TEXT NOT NULL CHECK (length(current_name) BETWEEN 1 AND 64),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'deleted')),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    revoked_at TEXT,
    deleted_at TEXT,
    CHECK ((status = 'active' AND revoked_at IS NULL AND deleted_at IS NULL)
        OR (status = 'revoked' AND revoked_at IS NOT NULL AND deleted_at IS NULL)
        OR (status = 'deleted' AND deleted_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX clients_current_name
ON clients(instance_id, current_name) WHERE status IN ('active', 'revoked');

CREATE TABLE address_assignments (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('static', 'dynamic')),
    address BLOB CHECK (address IS NULL OR length(address) = 4),
    status TEXT NOT NULL CHECK (status IN ('active', 'retained', 'released')),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    updated_at TEXT NOT NULL CHECK (length(updated_at) > 0),
    released_at TEXT,
    CHECK ((kind = 'static' AND address IS NOT NULL) OR (kind = 'dynamic' AND address IS NULL)),
    CHECK ((status = 'released' AND released_at IS NOT NULL) OR (status != 'released' AND released_at IS NULL))
) STRICT;
CREATE UNIQUE INDEX assignments_current_client_network
ON address_assignments(client_id, network_id) WHERE status IN ('active', 'retained');
CREATE UNIQUE INDEX assignments_current_network_address
ON address_assignments(network_id, address) WHERE address IS NOT NULL AND status IN ('active', 'retained');

CREATE TABLE client_leases (
    client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    family INTEGER NOT NULL CHECK (family = 4),
    address BLOB NOT NULL CHECK (length(address) = 4),
    updated_at TEXT NOT NULL CHECK (length(updated_at) > 0),
    PRIMARY KEY (client_id, network_id)
) STRICT;

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('instance', 'client')),
    owner_id TEXT NOT NULL CHECK (length(owner_id) = 36),
    kind TEXT NOT NULL CHECK (kind IN ('ca-cert', 'server-cert', 'server-key', 'client-cert', 'client-key', 'crl', 'tls-crypt', 'profile', 'ccd', 'server-config')),
    backend TEXT NOT NULL CHECK (backend = 'local'),
    artifact_key TEXT NOT NULL CHECK (length(artifact_key) > 0),
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    certificate_serial TEXT,
    certificate_fingerprint BLOB CHECK (certificate_fingerprint IS NULL OR length(certificate_fingerprint) = 32),
    status TEXT NOT NULL CHECK (status IN ('active', 'stale', 'deleted')),
    UNIQUE (backend, artifact_key)
) STRICT`)
	if err != nil {
		return classifySQLite("migrate client state", err)
	}
	return nil
}

func migrate(ctx context.Context, database *sql.DB, metadata Metadata) (Metadata, error) {
	for _, step := range migrations {
		if step.revision <= metadata.DatabaseRevision {
			continue
		}
		if step.revision != metadata.DatabaseRevision+1 {
			return Metadata{}, fmt.Errorf("%w: no migration from revision %d", ErrUnsupportedRevision, metadata.DatabaseRevision)
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return Metadata{}, classifySQLite("begin database migration", err)
		}
		if err := step.apply(ctx, transaction); err != nil {
			_ = transaction.Rollback()
			return Metadata{}, err
		}
		if _, err := transaction.ExecContext(ctx, "UPDATE schema_metadata SET database_revision = ? WHERE singleton = 1", step.revision); err != nil {
			_ = transaction.Rollback()
			return Metadata{}, classifySQLite("update database migration revision", err)
		}
		if err := transaction.Commit(); err != nil {
			return Metadata{}, classifySQLite("commit database migration", err)
		}
		metadata.DatabaseRevision = step.revision
	}
	if metadata.DatabaseRevision != CurrentRevision {
		return Metadata{}, fmt.Errorf("%w: database stopped at revision %d, require %d", ErrUnsupportedRevision, metadata.DatabaseRevision, CurrentRevision)
	}
	return metadata, nil
}

func migrateConfigurationState(ctx context.Context, transaction *sql.Tx) error {
	_, err := transaction.ExecContext(ctx, `
CREATE TABLE instances (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    ca_fingerprint BLOB NOT NULL CHECK (length(ca_fingerprint) = 32),
    current_applied_revision INTEGER CHECK (current_applied_revision > 0)
) STRICT;

CREATE TABLE applied_config (
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    digest BLOB NOT NULL CHECK (length(digest) = 32),
    endpoint TEXT NOT NULL CHECK (length(endpoint) > 0),
    protocol TEXT NOT NULL CHECK (protocol IN ('udp', 'tcp')),
    transport_family TEXT NOT NULL CHECK (transport_family IN ('auto', 'ipv4', 'ipv6')),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    client_to_client INTEGER NOT NULL CHECK (client_to_client IN (0, 1)),
    nat_enabled INTEGER NOT NULL CHECK (nat_enabled IN (0, 1)),
    nat_interface TEXT NOT NULL CHECK (length(nat_interface) > 0),
    redirect_gateway INTEGER NOT NULL CHECK (redirect_gateway IN (0, 1)),
    log_max_bytes INTEGER NOT NULL CHECK (log_max_bytes > 0),
    log_backups INTEGER NOT NULL CHECK (log_backups >= 0),
    PRIMARY KEY (instance_id, revision)
) STRICT;

CREATE TABLE networks (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    family INTEGER NOT NULL CHECK (family = 4),
    network BLOB NOT NULL CHECK (length(network) = 4),
    prefix INTEGER NOT NULL CHECK (prefix BETWEEN 0 AND 30),
    purpose TEXT NOT NULL CHECK (purpose = 'tunnel'),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    UNIQUE (instance_id, family, purpose)
) STRICT;

CREATE TABLE address_pools (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('static', 'dynamic')),
    first_address BLOB NOT NULL CHECK (length(first_address) = 4),
    last_address BLOB NOT NULL CHECK (length(last_address) = 4),
    policy TEXT NOT NULL CHECK (policy IN ('lowest-free', 'openvpn-dynamic')),
    CHECK (first_address <= last_address),
    UNIQUE (network_id, kind)
) STRICT;

CREATE TABLE pushed_routes (
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    family INTEGER NOT NULL CHECK (family = 4),
    network BLOB NOT NULL CHECK (length(network) = 4),
    prefix INTEGER NOT NULL CHECK (prefix BETWEEN 0 AND 32),
    PRIMARY KEY (instance_id, position)
) STRICT;

CREATE TABLE dns_servers (
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    family INTEGER NOT NULL CHECK (family = 4),
    address BLOB NOT NULL CHECK (length(address) = 4),
    PRIMARY KEY (instance_id, position)
) STRICT`)
	if err != nil {
		return classifySQLite("migrate configuration state", err)
	}
	return nil
}
