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
5. Expand toward state detection, safe repair, recovery, compatibility gates,
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
tests/e2e-container-smoke.sh
```

`tests/e2e-container-smoke.sh` sets `OVPN_NETWORK=10.88.0.0/24` internally and skips when Docker or `/dev/net/tun` is unavailable. Set `OVPN_E2E_REQUIRED=1` to make missing E2E prerequisites fail.

Build the current development image:

```bash
docker build -t szcq/openvpn-server:dev .
```

For local smoke checks, pass `OVPN_NETWORK=10.88.0.0/24` explicitly. The
Compose example keeps the product default configurable.
