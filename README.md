# OpenVPN Server Docker Image

[中文文档](README_CN.md)

A Docker image for running an OpenVPN Community Edition server with an
operator-focused shell control plane. It targets home labs, small teams, and
Linux servers that need certificate-authenticated IPv4 TUN VPN access without a
web administration layer.

## Highlights

- Builds a checksum-pinned OpenVPN runtime from source for `linux/amd64` and
  `linux/arm64`.
- Starts an empty persistent volume by creating the PKI, server identity, CRL,
  tls-crypt key, and OpenVPN configuration automatically.
- Supports UDP or TCP, IPv4 NAT, route push, full-tunnel routing, DNS push, and
  client-to-client traffic.
- Manages client certificates and profiles with `add-client`, `export-client`,
  `list-clients`, and `revoke-client`.
- Detects inconsistent persistent state before startup, performs only safe or
  byte-equivalent repairs, and fails closed for critical states.
- Includes a low-privilege maintenance service for diagnosis and repair.

## Scope

This image supports IPv4 TUN deployments with mutual certificate
authentication, Easy-RSA, tls-crypt, and CRL enforcement. It does not provide a
web UI, TAP mode, IPv6, external/offline CA workflows, LDAP/RADIUS/OIDC, or
Kubernetes integration.

## Quick Start

### Requirements

- Docker Engine with the Docker Compose plugin.
- A Linux host exposing `/dev/net/tun` and allowing the `NET_ADMIN` capability.
- A publicly reachable hostname or IP address and an open UDP or TCP port.
- An unused private IPv4 CIDR that does not overlap with the server or client
  networks.

### Configure and start

From a checkout of this repository, create a `.env` file beside `compose.yaml`:

```dotenv
OVPN_IMAGE=szcq/openvpn:2.7.5
OVPN_ENDPOINT=vpn.example.com
OVPN_PROTO=udp
OVPN_PORT=1194
OVPN_NETWORK=10.42.0.0/24
OVPN_NAT=true
OVPN_NAT_INTERFACE=auto
OVPN_REDIRECT_GATEWAY=false
OVPN_CLIENT_TO_CLIENT=false
OVPN_DNS=
OVPN_ROUTES=
```

Replace `vpn.example.com` with the public hostname or IP address clients use.
Choose a network that is unused in the deployment; the example deliberately
does not assume that `10.8.0.0/24` is available.

Start the server:

```bash
docker compose up -d
docker compose logs -f openvpn
```

The first start initializes only an empty `./openvpn-data` directory. It then
persists bootstrap configuration in `config/project.env`. Later changes to
bootstrap environment variables do not rewrite an existing instance.

The Compose port mapping follows `OVPN_PORT` and `OVPN_PROTO`. When either
value changes, open the same port and protocol in the host and cloud firewall.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | Image used by Compose. Pin a released OpenVPN-version tag. |
| `OVPN_ENDPOINT` | required | Public hostname or IP embedded in client profiles on initialization. |
| `OVPN_PROTO` | `udp` | Transport protocol: `udp` or `tcp`. |
| `OVPN_PORT` | `1194` | OpenVPN listen port. |
| `OVPN_NETWORK` | `10.8.0.0/24` | IPv4 tunnel network. Select a non-overlapping CIDR. |
| `OVPN_NAT` | `true` | Masquerade client traffic leaving the VPN container namespace. |
| `OVPN_NAT_INTERFACE` | `auto` | Egress interface for NAT, or a specific Linux interface name. |
| `OVPN_REDIRECT_GATEWAY` | `false` | Route client default traffic through the VPN. |
| `OVPN_CLIENT_TO_CLIENT` | `false` | Allow direct traffic between VPN clients. |
| `OVPN_DNS` | empty | Comma-separated IPv4 DNS servers pushed to clients. |
| `OVPN_ROUTES` | empty | Comma-separated IPv4 CIDRs pushed to clients. |
| `OVPN_CRITICAL_MODE` | `exit` | Use `maintenance` only to hold a critical container for inspection. |

Bootstrap values are instance facts, not ordinary runtime overrides. Inspect the
persisted values with:

```bash
docker compose exec openvpn ovpn config print
```

## Client Management

Create a client certificate and profile:

```bash
docker compose exec openvpn ovpn add-client laptop
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn
```

`export-client` writes only the profile to standard output, so redirection does
not mix it with status output. List or revoke clients with:

```bash
docker compose exec openvpn ovpn list-clients
docker compose exec openvpn ovpn revoke-client laptop
```

A revoked certificate is added to the CRL and its active profile is moved out of
the active-client set.

## Operations and Maintenance

Use the maintenance profile for one-shot state inspection and repair. It mounts
the same persistent data but does not request TUN, `NET_ADMIN`, or published
ports.

```bash
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance doctor --json
docker compose run --rm openvpn-maintenance repair --plan
docker compose run --rm openvpn-maintenance repair
```

`doctor` is read-only. `repair --plan` is also read-only and shows eligible
SAFE and equivalent recovery actions. `repair` stages, validates, snapshots,
and atomically applies only permitted repairs. Critical and unrecoverable states
fail closed with exit code `78`; use `OVPN_CRITICAL_MODE=maintenance` only when
an operator needs an unhealthy container to stay available for inspection.

Runtime and compatibility information is available through:

```bash
docker compose exec openvpn ovpn status
docker compose exec openvpn ovpn healthcheck
docker compose exec openvpn ovpn capabilities
docker compose exec openvpn ovpn version
```

## Persistent Data and Backups

`./openvpn-data` contains the CA private key, server and client private keys,
profiles, tls-crypt key, and instance metadata. Restrict access to this
directory and back it up securely.

A consistent backup includes at least `config/`, `meta/`, `pki/`, `secrets/`,
and `ccd/`; retaining `clients/` is also recommended because profiles can be
redundant recovery material. Prefer stopping the service before taking a
backup. After restoring data, run `doctor`, review `repair --plan`, and then
start the server.

Do not delete or point the bind mount at a different empty directory by
mistake: an empty directory is intentionally treated as a request to create a
new VPN instance.

## Security Notes

- The default design keeps the CA online inside the persistent data volume for
  operational convenience. Compromise of that volume can compromise the CA.
- Private keys and exported `.ovpn` profiles are sensitive credentials. Store
  them with restrictive permissions and deliver them over a trusted channel.
- The container changes forwarding and firewall rules only inside its own
  network namespace. Host firewall, cloud security-group, and port-forwarding
  configuration remain the operator's responsibility.
- This image validates source checksums, runtime version, configuration load,
  and required capabilities before publishing a stable release.

## Images, Builds, and Releases

Docker Hub stable releases use the OpenVPN runtime version as their only tag:

```text
szcq/openvpn:<OPENVPN_VERSION>
```

Pin an explicit tag in production rather than relying on a moving tag. GitHub
Container Registry also receives project-version tags for release management.

Build the current source tree locally:

```bash
scripts/docker-build.sh -t szcq/openvpn-server:dev .
OVPN_IMAGE=szcq/openvpn-server:dev docker compose up -d
```

GitHub Actions runs compatibility, container, E2E, upgrade-state, and
multi-architecture gates. A default-branch Candidate publishes the GHCR
candidate; a successful Candidate automatically triggers Release, which
promotes stable GHCR tags and publishes the Docker Hub OpenVPN-version tag.

Maintainers: if `DOCKER_TOKEN` expires, replace it in `Settings -> Secrets and
variables -> Actions`, then manually start a new default-branch Candidate. The
successful Candidate queues a fresh Release. Do not rely on rerunning the old
Release after replacing a repository secret.

## Development

The tracked release inputs are in `versions.env`. The OpenVPN version, source
checksum, supported range, and project image version are verified by CI.

Run the focused local checks before changing control-plane or workflow code:

```bash
tests/check.sh
tests/cli-smoke.sh
tests/workflow-smoke.sh
```

Container and E2E tests use `OVPN_NETWORK=10.88.0.0/24` to avoid the common
`10.8.0.0/24` test collision. Some checks require Docker, outbound source
access, and `/dev/net/tun`.
