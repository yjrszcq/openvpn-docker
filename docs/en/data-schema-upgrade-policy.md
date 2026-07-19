# Data Schema Upgrade Policy

This version-independent policy defines the persistent-data compatibility
contract. It remains in force when command manuals and image releases change.

## Version independence

The image version, OpenVPN runtime version, and persistent data schema are
independent. The schema is a monotonically increasing positive integer and
changes only when the persistent format becomes incompatible. Multiple image
and OpenVPN versions may use the same schema, and every image using that schema
must interpret it identically.

Migration dispatch is based only on schema evidence stored in the data
directory. It must not select persistent formats by image version, release tag,
source commit, or OpenVPN version.

## Strict runtime gate

Normal runtime code accepts only its current schema. Historical schemas may be
read only by migration modules lazy-loaded by `ovpn migrate`; normal config,
client, IP, state, repair, recovery, and server-start paths must not source or
parse historical formats.

An older schema prevents server startup and data-dependent commands with exit
status `78`. Help, version, capabilities, and migration planning may remain
available where they do not parse current-format state. Conflicting, malformed,
unknown, or newer schema evidence is rejected rather than guessed.

Startup migration, repair-driven migration, and config-driven backfilling are
forbidden. `state doctor` diagnoses only the current schema; use `migrate plan`
for historical data.

## Continuous migrations

Every schema change provides one dedicated `N-to-N+1` migration. A multi-schema
upgrade executes those steps in order. Historical migration code is loaded
only by the migrate dispatcher and is never sourced by normal runtime code.

The current image should retain a complete migration chain from every schema
for which sufficient released-format evidence exists. A missing step or
insufficient/conflicting evidence blocks migration.

## Maintenance transaction

All destructive migrations run while OpenVPN is stopped, through the current
image's `openvpn-maintenance` service. `migrate plan` is read-only. `migrate
apply` requires explicit confirmation and must acquire the exclusive runtime
lock, create a durable snapshot and transaction marker, migrate a staging copy,
validate the target schema and state, and commit atomically. Failure or
interruption restores the original data.

Migration uses only code embedded in the maintenance image and must not query
or download project releases. The final report identifies credentials or
profiles that were replaced and must be redistributed.

## Rollback

Migrated data cannot be given to an older image that does not support its
schema. An image rollback after migration requires restoring the matching
pre-migration snapshot. Recreating an old container without restoring its data
is not a supported rollback.

## Definition of done

A schema change is incomplete without the schema increment, one continuous
migration step, representative source fixtures, target validation, failure and
recovery tests, and updates to this policy and current operations documents.
CI must verify every retained source schema to the current schema, current
schema idempotence, and rejection of invalid or newer schemas.
