# Management Code Update Policy

This version-independent policy separates management-code updates from runtime
images, the OpenVPN kernel, and persistent data migrations.

## Version boundaries

`MANAGEMENT_VERSION`, `IMAGE_VERSION`, `OPENVPN_VERSION`, and the integer data
schema are independent. Stable GitHub `vX.Y.Z` releases identify management
code. Images provide the operating-system environment, OpenVPN kernel, and a
platform API consumed by management bundles.

Every management release declares its supported platform API, OpenVPN version
range, and exact data schema. Online update must reject an incompatible target
and direct the operator to update the image when the platform or OpenVPN kernel
is outside that declaration.

## Online update boundary

`ovpn upgrade` may replace only signed management code whose data schema equals
the running instance schema. It must not replace OpenVPN, install system
packages, migrate data, rewrite configuration or credentials, reload OpenVPN
configuration, or disconnect clients.

Downloaded release assets are verified by an image-trusted public key and
SHA-256 before extraction. Verified assets and active/previous selectors are
persisted under `/etc/openvpn/repair/.scripts`; executable runtime copies are
hydrated separately. Drafts, prereleases, downgrades, unsigned assets, and
incompatible releases are rejected.

Image-owned CLI and hook launchers resolve the active bundle at each invocation.
Switching the active selector therefore affects new management commands and hook
events without signaling, reloading, or replacing the OpenVPN process. Standard
HTTP proxy variables and an optional read-only GitHub token are the only network
configuration consumed by the updater.

## Migration and image updates

A schema-changing target is installed only by `ovpn migrate` in the stopped
`openvpn-maintenance` service. Migration stages target code and data together,
validates the result, and commits or restores both.

OpenVPN, operating-system dependencies, the immutable update bootstrap, and
platform API changes require an image update. A newer image does not implicitly
migrate persistent data.

## Release gates

Stable management releases publish a deterministic bundle, strict manifest,
SHA-256, and Ed25519 signature without requiring a stable image publication.
CI verifies the declared compatibility matrix, signature, schema registration,
online-update safety, and migrations from every registered release baseline.
