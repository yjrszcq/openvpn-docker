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
