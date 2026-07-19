# OpenVPN Server Docker Image

[中文文档](README_CN.md)

A Docker image for running an OpenVPN Community Edition server with a shell control plane. It targets home labs, small teams, and Linux servers that need certificate-authenticated IPv4 TUN VPN access without a web administration layer.

## Overview

- Builds a checksum-pinned OpenVPN runtime for `linux/amd64` and `linux/arm64`.
- Initializes an empty persistent volume with PKI, server identity, CRL, tls-crypt material, and the rendered server configuration.
- Supports UDP or TCP over public IPv4 or IPv6 transport, IPv4 NAT, route push, full-tunnel routing, DNS push, and client-to-client traffic.
- Uses a persistent client-IP registry with separate static and dynamic pools.
- Detects inconsistent persistent state before startup and fails closed for critical states.

The image supports IPv4 TUN deployments over public IPv4 or IPv6 transport, with mutual certificate authentication, Easy-RSA, tls-crypt, and CRL enforcement. It does not provide a web UI, TAP mode, IPv6 tunnel addressing or a dual-stack data plane, external or offline CA workflows, LDAP/RADIUS/OIDC, or Kubernetes integration.

## Quick Start

### Requirements

- Docker Engine with the Docker Compose plugin.
- A Linux host exposing `/dev/net/tun` and allowing the `NET_ADMIN` capability.
- A publicly reachable hostname or IP address, with `1194/udp` open in the host and cloud firewall. Use the selected port and protocol if you change the default.
- An unused private IPv4 CIDR that does not overlap with the server or client networks.

### Configure and start

Create a minimal `compose.yaml`:

```yaml
services:
  openvpn:
    image: szcq/openvpn:2.7.5
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./openvpn-data:/etc/openvpn
    environment:
      OVPN_ENDPOINT: vpn.example.com
      OVPN_PROTO: udp
      OVPN_TRANSPORT_FAMILY: auto
      OVPN_PORT: "1194"
      OVPN_NETWORK: 10.42.0.0/24
      OVPN_TOPOLOGY: subnet
      OVPN_DYNAMIC_POOL_SIZE: "64"
      OVPN_NAT: "false"
      OVPN_NAT_INTERFACE: auto
      OVPN_REDIRECT_GATEWAY: "false"
      OVPN_CLIENT_TO_CLIENT: "true"
      OVPN_DNS: ""
      OVPN_ROUTES: ""
      OVPN_CRITICAL_MODE: exit
```

Replace `vpn.example.com` with the public hostname or IP address clients use, and choose an unused network for the deployment. The repository `docker-compose.yaml` also includes an optional low-privilege maintenance service.

The example uses host networking, so OpenVPN listens directly on the host and has no Docker `ports:` mapping. `NET_ADMIN` therefore affects the host network namespace. NAT, pushed routes, and full-tunnel routing can also change IPv4 forwarding and iptables rules there; use this layout only on a controlled Linux host and review its resulting firewall rules.

Start the server:

```bash
docker compose up -d
docker compose logs -f openvpn
```

The first start initializes only an empty `./openvpn-data` directory and persists its bootstrap configuration in `config/project.env`. Later changes to bootstrap environment variables do not rewrite an existing instance.

## Configuration

| Variable | Runtime default / Compose fallback | Quick-start value | Purpose |
| --- | --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | `szcq/openvpn:2.7.5` | Image used by Compose. Pin a released OpenVPN-version tag. |
| `OVPN_ENDPOINT` | required | `vpn.example.com` | Public hostname or IP embedded in client profiles during initialization. |
| `OVPN_PROTO` | `udp` | `udp` | Transport protocol: `udp` or `tcp`. |
| `OVPN_TRANSPORT_FAMILY` | `auto` | `auto` | Public transport address family: `auto` detects IP literals and uses dual-stack transport for hostnames; `ipv4` and `ipv6` force a family. |
| `OVPN_PORT` | `1194` | `1194` | OpenVPN listen port. |
| `OVPN_NETWORK` | `10.8.0.0/24` | `10.42.0.0/24` | IPv4 tunnel network. Select a non-overlapping canonical CIDR. |
| `OVPN_TOPOLOGY` | `subnet` | `subnet` | Required IPv4 topology; no other topology is accepted. |
| `OVPN_DYNAMIC_POOL_SIZE` | half of usable client addresses | `64` | Tail of the usable address range reserved for dynamic clients; `0` and full capacity are valid boundaries. |
| `OVPN_NAT` | `false` | `false` | Masquerade client traffic leaving the VPN network namespace. |
| `OVPN_NAT_INTERFACE` | `auto` | `auto` | Egress interface for NAT, or a specific Linux interface name. |
| `OVPN_REDIRECT_GATEWAY` | `false` | `false` | Route client default traffic through the VPN. |
| `OVPN_CLIENT_TO_CLIENT` | `true` | `true` | Allow direct traffic between VPN clients. |
| `OVPN_DNS` | empty | empty | Comma-separated IPv4 DNS servers pushed to clients. |
| `OVPN_ROUTES` | empty | empty | Comma-separated IPv4 CIDRs pushed to clients. |
| `OVPN_LOG_MAX_BYTES` | `10485760` | `10485760` | Maximum size in bytes of each persistent OpenVPN log file before rotation. |
| `OVPN_LOG_BACKUPS` | `5` | `5` | Number of rotated OpenVPN log backups to retain; `0` disables backups. |
| `OVPN_CRITICAL_MODE` | `exit` | `exit` | Use `maintenance` only to hold a critical container for inspection. |
| `OVPN_EDITOR` | `EDITOR`, otherwise `nano` | unset | Editor used by interactive client-IP workflows. |

Runtime defaults apply only when the environment omits a value. The quick-start values are the deliberately opinionated values in `docker-compose.yaml` and `.env.example`; they are not an additional set of runtime defaults.

With `OVPN_TRANSPORT_FAMILY=auto`, an IPv4 literal such as `198.51.100.10` selects IPv4 transport and an IPv6 literal such as `2001:db8::10` selects IPv6 transport. For a hostname, the server opens a dual-stack transport socket and the client resolves and tries A/AAAA records at connection time; `config apply` does not resolve DNS. A server with only public IPv6 can therefore publish an AAAA record and keep `OVPN_TRANSPORT_FAMILY=auto`; use `ipv6` only to reject IPv4 transport. The dual-stack listener is an IPv6 socket without `bind ipv6only`, so it also accepts IPv4-mapped peers such as `::ffff:198.51.100.10`. Explicit IPv6 transport adds `bind ipv6only`; IPv4 transport needs no equivalent option because an IPv4 socket cannot accept IPv6. This changes only the outer OpenVPN connection; tunnel addresses, pushed routes, and DNS configuration remain IPv4. If the server has no IPv4 egress, VPN clients cannot reach the public IPv4 Internet through the existing IPv4 NAT, and this image does not provide NAT64.

For a canonical network with prefix length `p`, usable client capacity is `2^(32 - p) - 3`: the network address, server address (`network + 1`), and broadcast address are reserved. For example, `10.42.0.0/24` provides 253 client addresses (`10.42.0.2` through `10.42.0.254`), so the dynamic pool may be `0` through `253`; its unset default is `floor(253 / 2) = 126`. The dynamic pool is a contiguous tail of this range, and the preceding addresses are the static region. The smallest usable network is `/30`, which provides exactly one client address (`.2`).

Choose the routing model deliberately:

- For routed private-network access, keep `OVPN_NAT=false` and `OVPN_REDIRECT_GATEWAY=false`, and set `OVPN_ROUTES` for reachable private networks. Each target network needs a return route to the VPN CIDR through the VPN host.
- For Internet full-tunnel access, set `OVPN_NAT=true` and `OVPN_REDIRECT_GATEWAY=true`. Leave `OVPN_NAT_INTERFACE=auto` unless the host has more than one possible egress interface.

## Command Documentation

This README intentionally does not duplicate the command manual. Select the reference that matches the image version you operate:

Persistent compatibility follows the version-independent [data schema upgrade policy](docs/en/data-schema-upgrade-policy.md). Its maintenance-only migration requirements remain in force across command-documentation versions.

Project code and runtime changes follow the permanent [image update policy](docs/en/image-update-policy.md). The image is the only code delivery unit; data migrations remain a separate maintenance operation.

- [v1 command reference](docs/en/v1/commands.md) — for `1.0.0`.
  - [v1 operations guide](docs/en/v1/operations.md) — workflow-oriented command combinations.
- [v2 command reference](docs/en/v2/commands.md) — releases `2.0.0` through `2.1.1`.
  - [v2 operations guide](docs/en/v2/operations.md) — workflow-oriented command combinations.
- [v3 command reference](docs/en/v3/commands.md) — the current CLI.
  - [v3 operations guide](docs/en/v3/operations.md) — workflow-oriented command combinations.

## Updates, migrations, and logs

`IMAGE_VERSION`, `OPENVPN_VERSION`, and the integer data schema are independent. Inspect them with `ovpn --version` and `ovpn runtime version`. Pull or build a new image and recreate the container for every project-code update.

- If the target image uses the **same data schema**, stop the old container and recreate it with the target image. Do not run `migrate`.
- If the target image uses a **newer data schema**, stop the old container and run the following maintenance migration before starting the target image.

If a new image finds an older data schema, normal data commands and server startup fail with status `78`. Stop the live service and migrate only through maintenance:

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
```

Migration may replace client credentials; redistribute every active profile listed by `migrate apply`. An image rollback does not roll back migrated data—restore the matching pre-migration snapshot instead. See the operations guide for snapshots and recovery.

Client lists display a copyable 12-character UUID prefix by default; use `client list --no-trunc` for complete UUIDs. Client commands retain positional references and also accept `--id`/`-i` for an ID prefix or `--name`/`-n` for an exact display name. Every public multi-letter option also has a command-local single-letter form; `--ip` uses uppercase `-I` because lowercase `-i` selects a client ID. Persistent OpenVPN logs use the same short identity display, while the event stream provides structured lifecycle records.

These selector and presentation changes do not alter persistent data. The planned 3.2.0 image remains on data schema 3, so a 3.1.0 deployment can recreate the container directly without running `migrate`.

```bash
docker compose exec openvpn ovpn runtime logs -l 100
docker compose exec openvpn ovpn runtime logs -l 100 -t
docker compose exec openvpn ovpn runtime events -l 100 -j
```

The user-facing event stream is stored separately from the strict internal audit at `meta/audit.jsonl`. The audit belongs to the persistent data schema and is used by state validation, repair recovery, and migration; do not edit or delete it manually.

## Security Notes

- The default design keeps the CA inside the persistent data volume for day-to-day convenience. Compromise of the volume can expose the CA.
- Private keys and exported `.ovpn` profiles are sensitive credentials. Store them with strict permissions and deliver them through trusted channels.
- Source checksums, runtime version, configuration loading, and required capabilities are verified before a stable release is published.
- Network-rule scope depends on the selected network mode. The quick-start example uses host networking, which shares the host network namespace; enabling NAT, route push, or full-tunnel routing modifies the host's IPv4 forwarding and iptables rules. In an isolated container network mode those changes stay within the container network namespace. Host firewall, cloud security groups, and port forwarding remain the operator's responsibility.

## Development

Version and release inputs are centralized in `versions.env`. Before changing code, run:

```bash
tests/smoke/shell/check.sh           # shell syntax and style
tests/smoke/shell/cli-smoke.sh       # CLI structure verification
tests/smoke/shell/workflow-smoke.sh  # workflow logic verification
```

CI validates the OpenVPN version, source checksum, support matrix, and project image version. Tests use `OVPN_NETWORK=10.88.0.0/24`. Some checks require Docker and `/dev/net/tun`.

`OPENVPN_CANDIDATE_RANGE` in `versions.env` limits which upstream versions automation may propose; it does not claim runtime compatibility. Exact OpenVPN versions verified for the current image are listed in `compatibility/contract.env`.

## Build & Release

Docker Hub stable images use the OpenVPN runtime version as the sole tag:

```text
szcq/openvpn:<OPENVPN_VERSION>
```

Pin an explicit tag in production; do not rely on a moving tag. GitHub Container Registry also receives image-version tags for project release management.

Build the current source tree locally:

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
OVPN_IMAGE=szcq/openvpn-server:dev docker compose up -d
```

GitHub Actions publishes project code only as images. Changing `IMAGE_VERSION` on the default branch publishes a tested GHCR candidate and then runs the Image Release workflow, which creates stable GHCR tags and the OpenVPN-version tag on Docker Hub. Image-version changes remain separate release commits.

An incompatible persistent-format change must also increment `DATA_SCHEMA` and provide the next continuous migration, representative source fixtures, and policy/tests required by the [data schema upgrade policy](docs/en/data-schema-upgrade-policy.md). OpenVPN, base-system, dependency, and project-code changes are all delivered by the image; there is no management-code release channel.

A weekly (or manually triggered) Upstream Check watches for new official OpenVPN releases within `OPENVPN_CANDIDATE_RANGE`. When one is found it pushes an `automation/openvpn-<version>` branch and opens a PR targeting `dev`. Review and merge that PR into `dev` to run PR checks; Candidate is never published from `dev`. Promote reviewed changes from `dev` to `main` to trigger Candidate and the subsequent Image Release. When triggering Candidate manually, select `main`.

Maintainer note: if `DOCKER_TOKEN` expires, update it in `Settings → Secrets and variables → Actions`, then manually trigger a default-branch Candidate. A successful Candidate queues a new Image Release. After updating the repository secret, do not rely on re-running an old release workflow.

## License

Copyright (C) 2026 yjrszcq.

The original source code and build configuration in this repository are licensed under [GPL-2.0-only](LICENSE). [NOTICE](NOTICE) defines that scope and identifies third-party components. Container images include OpenVPN Community Edition and other third-party components under their own licenses; they are not relicensed by this project. The image provides this project's license file and OpenVPN's `COPYING` file under `/usr/local/share/licenses/`.

Published images are built from this source tree and the checksum-pinned OpenVPN source declared in [versions.env](versions.env); fetch logic is in [scripts/fetch-openvpn-source.sh](scripts/fetch-openvpn-source.sh).
