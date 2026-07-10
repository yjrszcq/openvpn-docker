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
6. Expand toward state detection, safe repair, recovery, compatibility gates,
   and release automation.

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
tests/render-smoke.sh
tests/init-start-smoke.sh
tests/client-lifecycle-smoke.sh
tests/build-info-smoke.sh
tests/source-fetch-smoke.sh
tests/source-build-layout-smoke.sh
tests/runtime-image-smoke.sh
tests/e2e-container-smoke.sh
```

`tests/e2e-container-smoke.sh` sets `OVPN_NETWORK=10.88.0.0/24` internally and skips when Docker or `/dev/net/tun` is unavailable. Set `OVPN_E2E_REQUIRED=1` to make missing E2E prerequisites fail.

Build the current development image with the pinned inputs from `versions.env`:

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

When a builder needs a host-local proxy to fetch pinned source, pass it explicitly:

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

`tests/runtime-image-smoke.sh` builds and inspects the image when Docker is available. Set `OVPN_RUNTIME_REQUIRED=1` to make unavailable Docker prerequisites fail.
