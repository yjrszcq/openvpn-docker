# Data Schema Upgrade Policy

This version-independent policy governs persistent data compatibility for every
management release and remains in force when command manuals are archived.

## Independent versions

The management-code version, image version, OpenVPN runtime version, and
persistent data schema are independent. The data schema is a monotonically
increasing integer and changes only for incompatible persisted-state changes.
Multiple management and image releases may use the same schema.

Published management versions, exact source commits, schemas, distribution
type, platform API range, and OpenVPN range are recorded in
`compatibility/data-schema-releases.jsonl`. Every release must be registered.
Historical `legacy-image` rows explicitly have no online platform range;
online-capable releases use `signed-bundle`.
The registry uses one strict JSON object per line. Unknown fields and invalid
types are rejected so registry-format changes require an explicit validator
update.

```json
{"management_version":"3.0.0","commit":"<40-character commit>","data_schema":3,"distribution":"signed-bundle","platform_api":{"min":2,"max":2},"openvpn":{"min":"2.7.0","max_exclusive":"2.8.0"}}
```

`legacy-image` entries use `null` for `platform_api`.

## Runtime boundary

A management release runs only its current schema. Historical schemas may be
read only by migration modules loaded by `ovpn migrate`; normal configuration, client,
network, state, repair, render, and runtime-data commands must not contain
historical compatibility branches.

An older schema prevents the server from starting. Automatic startup migration,
migration through repair, and old-format completion by `config apply` are
prohibited. Data-independent help, version, and runtime-capability inspection
may remain available. Supporting an older release means supporting its migration,
not running its data without migration.

## Migration requirements

Every schema change provides a dedicated `N-to-N+1` migration. Multi-schema
upgrades run registered steps in order. Historical migration code is loaded
only by the migrate command and is not sourced by the normal runtime.

The newest management release should retain a complete chain from every
published schema for which sufficient evidence exists. Migrate must refuse to
guess when a schema is
unknown, newer than the image, inconsistent, or missing required evidence.

Destructive migration is offline and runs through `openvpn-maintenance`.
Migrate must provide a read-only plan, explicit apply confirmation, a durable
snapshot, staging, full target validation, interrupted-commit recovery, and a
report of credential replacement, profile invalidation, and lost history.

A migrated data directory cannot be used by older management code that lacks
support for its schema. Rolling back code or an image requires restoring the
matching pre-migration snapshot when their embedded code does not support it.

## Release gates

A schema-changing release is incomplete without migration, fixtures,
documentation, and tests. CI must exercise every published release baseline in
the manifest to the current schema. Every management release must register its
version, exact source commit, schema, distribution type, platform range, and
OpenVPN range even when the schema is unchanged.
