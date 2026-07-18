# Data Schema Upgrade Policy

This version-independent policy governs persistent data compatibility for every
image release and remains in force when command manuals are archived.

## Independent versions

The image version, OpenVPN runtime version, and persistent data schema are
independent. The data schema is a monotonically increasing integer and changes
only for incompatible persisted-state changes. Multiple image releases may use
the same schema.

Published image versions, exact source commits, and schemas are recorded in
`compatibility/data-schema-releases.tsv`. Every release must be registered.

## Runtime boundary

An image runs only its current schema. Historical schemas may be read only by
migration modules loaded by `ovpn upgrade`; normal configuration, client,
network, state, repair, render, and runtime-data commands must not contain
historical compatibility branches.

An older schema prevents the server from starting. Automatic startup migration,
migration through repair, and old-format completion by `config apply` are
prohibited. Data-independent help, version, and runtime-capability inspection
may remain available. Supporting an older release means supporting its upgrade,
not running its data without migration.

## Migration requirements

Every schema change provides a dedicated `N-to-N+1` migration. Multi-schema
upgrades run registered steps in order. Historical migration code is loaded
only by the upgrade command and is not sourced by the normal runtime.

The newest image should retain a complete chain from every published schema for
which sufficient evidence exists. Upgrade must refuse to guess when a schema is
unknown, newer than the image, inconsistent, or missing required evidence.

Destructive migration is offline and runs through `openvpn-maintenance`.
Upgrade must provide a read-only plan, explicit apply confirmation, a durable
snapshot, staging, full target validation, interrupted-commit recovery, and a
report of credential replacement, profile invalidation, and lost history.

An upgraded data directory cannot be used by an older image. Rolling back the
image requires restoring its matching pre-upgrade snapshot.

## Release gates

A schema-changing release is incomplete without migration, fixtures,
documentation, and tests. CI must exercise every published release baseline in
the manifest to the current schema. A new image release must register its
version, exact source commit, and schema even when the schema is unchanged.
