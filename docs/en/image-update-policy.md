# Image Update Policy

This is the long-term policy for project code and runtime delivery.

## Version model

The repository records separate release axes in `versions.env`:

- `IMAGE_VERSION`: project control-plane and container release.
- `DATA_SCHEMA`: persistent structured-state format.
- `OPENVPN_VERSION`: OpenVPN Community Edition runtime built into the image.
- `GO_RUNTIME_VERSION`: version reported by the Go binaries; for a stable release it must equal `IMAGE_VERSION`.

Build inputs also pin the Go builder image and OpenVPN source checksum. `compatibility/contract.json` lists exact OpenVPN runtime versions and features verified for the image. `OPENVPN_CANDIDATE_RANGE` only constrains automation; it is not a compatibility claim.

## Delivery boundary

The image is the only project-code delivery unit. The CLI, hook, supervisor, broker, templates, migration code, and compatibility contract are built into the image. Runtime does not download code, query project releases, or select an online management bundle.

An image update may change project code, OpenVPN, the base system, or build dependencies. It must not silently apply YAML, rewrite credentials, migrate data, or modify structured state merely because a container started.

## Schema boundary

Images sharing a data schema must interpret it identically. A same-schema update is performed by stopping the old service, selecting the new image, running read-only diagnosis, and starting the new service. Do not run migration for a same-schema update.

When the target requires a newer schema, normal runtime refuses old state. The operator must stop OpenVPN and run the target image's explicit maintenance migration according to the [data schema upgrade policy](data-schema-upgrade-policy.md).

## Tag policy

GHCR receives project image tags (`4.0.0`, `4.0`, `4`, and `latest`) plus the verified OpenVPN tag. Docker Hub keeps the public OpenVPN-version tag used by existing deployments. Production users should always pin a concrete tag and verify `ovpn version --json` after update.

Every candidate image is tested before publication. The candidate range may block stable promotion, but it does not suppress the tested candidate image. A prerelease `IMAGE_VERSION` is not eligible for stable promotion. OpenVPN cross-minor updates require the configured approval boundary.

## Rollback boundary

A same-schema image can be rolled back by recreating the service with the prior image. After schema migration, image rollback alone is invalid: restore the matching complete pre-migration snapshot and use the image that supports that restored schema.

## Release requirements

Every stable release must pass:

- Go format, vet, unit, race, build, module, and dependency-license gates;
- workflow/Shell/source-integrity checks;
- strict YAML, SQLite, PKI, lifecycle, repair, recovery, and migration tests;
- real schema handoff and rollback from the supported previous format;
- real UDP and TCP client connections;
- amd64 and arm64 image builds and dynamic dependency audits;
- image-content checks proving the absence of legacy runtime Shell and Python;
- complete English/Chinese command, operations, migration, backup, and rollback documentation.

Image/schema version changes are made in the release-preparation phase. A stable release is blocked while any required gate, review finding, or release metadata inconsistency remains unresolved.
