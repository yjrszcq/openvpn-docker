# OpenVPN Server Docker Image

This repository implements a Docker image for OpenVPN Community Edition with a
small shell-based control plane.

The project direction is defined in `goal/`, which is intentionally ignored by
Git in this working copy. Implementation follows a private checkpoint workflow:

- `.checkpoint-karpathy/roadmap.md`
- `.checkpoint-karpathy/progress.md`
- `.checkpoint-karpathy/private/`

Those checkpoint files are local planning state and are not committed.

## Current Phase

The implementation starts with a minimal VPN slice:

1. Create a fixed-runtime container image.
2. Add the `ovpn` CLI and entrypoint.
3. Add explicit initialization and start flows.
4. Add basic client lifecycle commands.
5. Pin the upstream OpenVPN source inputs and build metadata.
6. Enforce the runtime compatibility contract, adapter-selected templates, and capability gate.
7. Expand toward state detection, safe repair, recovery, and release automation.

Local validation must avoid the already-used `10.8.0.0/24` network. Tests and
smoke checks use:

```text
OVPN_NETWORK=10.88.0.0/24
```

## Verification

Run the local checks:

```bash
tests/check.sh
tests/cli-smoke.sh
tests/capabilities-smoke.sh
tests/render-smoke.sh
tests/init-start-smoke.sh
tests/state-scanner-smoke.sh
tests/state-machine-smoke.sh
tests/crypto-state-smoke.sh
tests/doctor-smoke.sh
tests/repair-plan-smoke.sh
tests/recovery-shared-smoke.sh
tests/recovery-container-smoke.sh
tests/maintenance-smoke.sh
tests/maintenance-compose-smoke.sh
tests/maintenance-container-smoke.sh
tests/network-policy-smoke.sh
tests/repair-container-smoke.sh
tests/bootstrap-init-smoke.sh
tests/client-lifecycle-smoke.sh
tests/build-info-smoke.sh
tests/docker-build-wrapper-smoke.sh
tests/source-fetch-smoke.sh
tests/source-build-layout-smoke.sh
tests/runtime-image-smoke.sh
tests/config-load-smoke.sh
tests/e2e-container-smoke.sh
```

`tests/e2e-container-smoke.sh` sets `OVPN_NETWORK=10.88.0.0/24` internally and skips when Docker or `/dev/net/tun` is unavailable. Set `OVPN_E2E_REQUIRED=1` to make missing E2E prerequisites fail.

`tests/recovery-container-smoke.sh` rebuilds the image by default, recovers CA, tls-crypt, and a client identity from active profile material, and verifies hashes and modes on `10.88.0.0/24`. Set `OVPN_RECOVERY_REQUIRED=1` to require its Docker prerequisites.

`ovpn capabilities` emits the runtime version, supported-range result, adapter, and required feature probes. It exits nonzero when the compatibility gate fails.

`ovpn start` automatically initializes a data directory only when it is EMPTY. A valid `OVPN_ENDPOINT` is required for that first run; partial, interrupted, or otherwise non-empty data is never overwritten.

`ovpn doctor` reports the read-only persisted-state diagnosis. Pass `--json` for a stable object containing the state plus every issue ID, severity, and recommended action. It returns `78` for `CRITICAL` and `UNRECOVERABLE` state after printing the diagnosis.

`ovpn repair --plan` is read-only and lists SAFE actions plus validated equivalent `RECOVER` actions. Pass `--json` for an integration-friendly plan; CRITICAL and UNRECOVERABLE states still return `78`.

`ovpn repair` stages, validates, snapshots, and atomically applies SAFE repairs plus strictly validated, byte-equivalent recovery from embedded profile material. It never reissues certificates or generates identity keys; failed transactions restore affected files and record redacted journals under `repair/`.

Use one-shot maintenance commands with `docker compose run --rm openvpn-maintenance doctor` or `docker compose run --rm openvpn-maintenance repair --plan`. Set `OVPN_CRITICAL_MODE=maintenance` only when an operator needs a CRITICAL container to remain running and unhealthy for inspection; the default `exit` behavior fails immediately.

## Network Policy

On first initialization, `OVPN_NAT`, `OVPN_NAT_INTERFACE`,
`OVPN_REDIRECT_GATEWAY`, `OVPN_CLIENT_TO_CLIENT`, `OVPN_DNS`, and
`OVPN_ROUTES` are persisted in `config/project.env`; later Compose environment
changes do not override the instance. DNS and routes are IPv4-only in this
release. When NAT, full-tunnel, or route push is enabled, the main service
enables IPv4 forwarding and installs idempotent `iptables` rules only in its
own network namespace. The Docker host must provide `/dev/net/tun`, permit
`NET_ADMIN`, and allow namespaced IPv4 forwarding; the image does not alter
host firewall or forwarding settings.

`tests/config-load-smoke.sh` validates generated server and client configurations with the actual OpenVPN crypto self-test and uses `OVPN_NETWORK=10.88.0.0/24`. It skips when Docker is unavailable; set `OVPN_CONFIG_LOAD_REQUIRED=1` to require it.

Build the current development image with the pinned inputs from `versions.env`:

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

When a builder needs a host-local proxy to fetch pinned source, pass it explicitly:

`docker-build.sh` inherits standard `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` variables when their `OVPN_BUILD_*` counterparts are unset. It filters loopback HTTP(S) proxies for the default Docker network, because that network cannot reach the host loopback; use `OVPN_BUILD_NETWORK=host` with explicit `OVPN_BUILD_*` values for a host-local proxy.

```bash
OVPN_BUILD_NETWORK=host \
OVPN_BUILD_HTTP_PROXY=http://proxy.example:port \
OVPN_BUILD_HTTPS_PROXY=http://proxy.example:port \
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

`ovpn version` reports both the currently packaged runtime and the pinned source version. The Phase 2 runtime is the verified pinned source build.

For local smoke checks, pass `OVPN_NETWORK=10.88.0.0/24` explicitly. The Compose example keeps the product default configurable.

Start the locally built development image with Compose:

```bash
docker compose up -d
```

`tests/source-fetch-smoke.sh` downloads the pinned upstream archive and therefore requires outbound network access.

Source retrieval prefers `swupdate.openvpn.org` and falls back to the matching official OpenVPN GitHub release asset. Both paths must satisfy the pinned SHA-256 in `versions.env`.

`tests/runtime-image-smoke.sh` builds and inspects the image when Docker is available. Set `OVPN_RUNTIME_REQUIRED=1` to make unavailable Docker prerequisites fail.

## CI and Release

GitHub Actions owns upstream discovery, candidate publication, compatibility verification, and stable promotion.

- `test.yml`: gates G0-G13: source integrity, exact version and configuration checks, PKI/client lifecycle, UDP/TCP E2E, repair/recovery, persisted-state upgrade, and amd64/arm64 build records.
- `upstream-check.yml`: checks the official OpenVPN release feed weekly and opens a checksum-pinned update pull request instead of changing a release branch directly.
- `candidate.yml`: publishes `ghcr.io/<owner>/<repo>:candidate-ovpn<version>` from the default branch after the applicable compatibility gates pass.
- `release.yml`: promotes the tested candidate only when the OpenVPN version is inside `OPENVPN_SUPPORTED_RANGE` and `IMAGE_VERSION` is a final SemVer value. GitHub Container Registry receives project-version tags from `IMAGE_VERSION`; Docker Hub receives only `szcq/openvpn:<OPENVPN_VERSION>` after stable promotion. Same-branch releases promote automatically. Cross-branch releases wait on the `stable-cross-branch` GitHub Environment; configure its required reviewers before relying on that path. Out-of-range and prerelease images remain candidates only. Configure Docker Hub secret `DOCKER_TOKEN` before a stable release.

`versions.env` is the sole version source. Run `tests/update-openvpn-smoke.sh`, `tests/release-policy-smoke.sh`, and `tests/workflow-smoke.sh` locally. To require the persisted-state check against already-built images, set `OVPN_UPGRADE_SKIP_BUILD=1`, `OVPN_UPGRADE_REQUIRED=1`, `OVPN_UPGRADE_SOURCE_IMAGE`, and `OVPN_UPGRADE_TARGET_IMAGE` before running `tests/upgrade-state-smoke.sh`.
