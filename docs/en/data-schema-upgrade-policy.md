# Data Schema Upgrade Policy

This policy defines persistent-data compatibility independently from command documentation and image releases.

## Independent version axes

The project image version, OpenVPN runtime version, and integer data schema are independent. The data schema changes only when persistent interpretation is incompatible. Multiple project/OpenVPN releases may share one schema and must interpret that schema identically.

Migration dispatch uses only evidence inside the data directory. It must not select a format by image tag, source revision, release date, or OpenVPN version.

## Current schema 4 authority

Schema 4 stores all structured authoritative state in `/etc/openvpn/meta/state.db`. PKI, private keys, CRL, tls-crypt, profiles, CCD, derived server configuration, and logs remain files. The database and those files are one backup/restore unit.

Runtime paths must not maintain CSV/SQLite dual authority. Runtime lease data is explicitly disposable cache; business state and audit updates are committed transactionally in SQLite.

## Strict runtime gate

Normal runtime code accepts only the current schema. Historic formats are read only by the explicit migration package. Config, client, state, repair, recovery, hook, and server startup paths must not parse legacy registries.

Old, newer, unknown, conflicting, or corrupt schema evidence is rejected with exit code `78`. Help, version, capabilities, and migration planning may remain available when they do not interpret current business state.

There is no startup migration, repair-triggered migration, or automatic format rewrite.

## Supported migration ingress

The v4 image directly supports schema 3 to schema 4 only. Schema 1 and 2 instances must first use the stable `sh-ver` image to reach schema 3. This deliberately bounds the Go runtime's legacy parser and keeps older migration logic on the maintenance branch that created those formats.

Future schema changes must add an explicit supported ingress policy. A direct `N` to `N+1` step is preferred, but the project may require an intermediate stable image when retaining every historical parser would expand the trusted runtime or weaken validation. Unsupported paths must fail with actionable instructions; they must never be guessed.

## Maintenance transaction

Destructive migration requires OpenVPN to be stopped and runs through the target image's `openvpn-maintenance` service. `migrate plan` is read-only. `migrate apply` requires confirmation, `OVPN_MAINTENANCE=true`, the exclusive runtime lock, a persistent full-data snapshot and digest, staging, target validation, and atomic installation.

Database audit and business state changes are committed together. Cross-file and SQLite changes use the operation journal and staging so an interruption can be completed or rolled back deterministically.

The final report must identify the snapshot, digest, imported counts, state doctor result, YAML export step, and profile redistribution impact.

## Rollback

A migrated directory must never be passed to an image that does not support its schema. Rolling back a successful schema 3 to 4 migration requires verifying and restoring the complete migration snapshot, then running the `sh-ver` image. Changing only the image is unsupported.

For ordinary schema 4 backups, stop all writers and archive the complete data and configuration directories. Never restore only `state.db` or merge a backup into live artifacts.

## Definition of done

A schema change is incomplete without:

- a schema increment and authoritative model definition;
- explicit supported and rejected source schemas;
- representative healthy, damaged, conflicting, large, and interrupted fixtures;
- staging, snapshot, digest, rollback, and crash recovery tests;
- target `state doctor` and real runtime handoff verification;
- updated command, operations, backup, migration, and rollback documentation.
