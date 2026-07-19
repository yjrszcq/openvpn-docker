# Image Update Policy

This permanent policy defines how project code and runtime changes are delivered. It remains in force when versioned command manuals are archived.

## Version model

The project has three independent version axes:

- `IMAGE_VERSION` identifies the project code and container image release.
- `OPENVPN_VERSION` identifies the OpenVPN runtime built into that image.
- The integer data schema identifies the persistent `/etc/openvpn` format.

Management scripts, hooks, templates, broker code, and Web/API code are part of the image. There is no in-container code downloader, management bundle, active code selector, or online rollback mechanism.

## Delivery boundary

The image is the only code delivery unit. Ordinary updates pull or build a new image and recreate the container. An image may change project code, OpenVPN, system dependencies, or the base operating system. Runtime commands never install code or packages and never query project releases.

An image update must not silently rewrite configuration, credentials, or other persistent state. New templates are applied only through the normal explicit configuration and lifecycle commands.

## Data-schema boundary

When old and new images use the same data schema, the persistent format must be identical; code must not branch on image version to interpret that format. The operator may stop the old container and recreate it with the new image.

When the new image requires a newer schema, its normal runtime rejects the old data with exit status `78`. The operator must stop OpenVPN and run `ovpn migrate` through the new image's `openvpn-maintenance` service. Migration uses only code built into that maintenance image and never accesses the network.

Changing an incompatible persistent format requires incrementing the schema and adding the next continuous migration step. Details are governed by the [data schema upgrade policy](data-schema-upgrade-policy.md).

## Rollback boundary

Replacing a same-schema image can be rolled back by recreating the container with the previous image. After a data migration, an image rollback alone is not a data rollback: restore the migration's matching pre-migration snapshot before running an older image that expects the old schema.

## Release requirements

Every image release must pass static checks, schema gates, representative migrations, persistent-state handoff, runtime lifecycle tests, and the relevant network E2E matrix. `compatibility/contract.env` records the exact OpenVPN versions verified with the image; `OPENVPN_CANDIDATE_RANGE` only constrains which upstream versions automation may propose.

Image versions are changed in a dedicated release commit. A persistent format change is incomplete without a schema increment, migration, fixtures, policy updates, and migration tests.
