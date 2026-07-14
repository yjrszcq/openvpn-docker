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
- A publicly reachable hostname or IP address, with `1194/udp` open in the
  host and cloud firewall. Use the selected port and protocol if you change
  the default.
- An unused private IPv4 CIDR that does not overlap with the server or client
  networks.

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
      - ./openvpn-runtime:/var/lib/openvpn
    environment:
      OVPN_ENDPOINT: vpn.example.com
      OVPN_PROTO: udp
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

The repository's `docker-compose.example.yaml` also includes the optional
`openvpn-maintenance` service described below. The default uses host networking:
OpenVPN listens directly on the host, so it has no Docker `ports:` mapping.
Before starting it, allow `1194/udp` in the host and cloud firewall. Host
networking makes the VPN gateway address a host address; the `NET_ADMIN`
capability therefore affects host networking and should be used only on a
controlled Linux host.


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

With host networking, OpenVPN listens directly on `OVPN_PORT` and `OVPN_PROTO`.
When either value changes, open the same port and protocol in the host and
cloud firewall.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | Image used by Compose. Pin a released OpenVPN-version tag. |
| `OVPN_ENDPOINT` | required | Public hostname or IP embedded in client profiles on initialization. |
| `OVPN_PROTO` | `udp` | Transport protocol: `udp` or `tcp`. |
| `OVPN_PORT` | `1194` | OpenVPN listen port. |
| `OVPN_NETWORK` | `10.8.0.0/24` | IPv4 tunnel network. Select a non-overlapping canonical CIDR. |
| `OVPN_TOPOLOGY` | `subnet` | Required IPv4 topology; no other topology is accepted. |
| `OVPN_DYNAMIC_POOL_SIZE` | half of usable client addresses | Tail of the usable address range reserved for dynamic clients; 0 and full capacity are valid boundaries. |
| `OVPN_NAT` | `true` | Masquerade client traffic leaving the VPN network namespace. |
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

### Change an existing configuration

To change a running instance, update the Compose configuration first. For
example, to change OpenVPN from UDP to TCP on port 1194:

```yaml
environment:
  OVPN_PROTO: tcp
  OVPN_PORT: "1194"
```

Host networking has no Docker `ports:` mapping. Open the matching TCP port in
the host and cloud firewall. Then apply the full current Compose environment to
the existing data directory:

```bash
docker compose config --quiet
docker compose down # Do not use `-v`.
docker compose run --rm openvpn ovpn config init
docker compose up -d openvpn
docker compose exec openvpn ovpn config print
```

`config init` rewrites only the persisted configuration; it does not remove or
reissue client certificates, keys, or profiles. It writes every configuration
value from the Compose environment, so keep all `OVPN_*` values complete and
correct before running it. Do not use config init to change OVPN_NETWORK or OVPN_DYNAMIC_POOL_SIZE on an initialized instance; use the migration command below so the server can reload and roll back atomically.

When `OVPN_ENDPOINT`, `OVPN_PROTO`, or `OVPN_PORT` changes, export and
redistribute a new profile for every active client. The temporary file prevents
a failed export from replacing an existing local profile:

```bash
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn.tmp &&
mv laptop.ovpn.tmp laptop.ovpn
```

Do not run `add-client` again for an existing name. Existing client
certificates remain valid; clients need the newly rendered profile to use the
new endpoint, protocol, or port.

On a healthy instance, `export-client` atomically refreshes the matching
`clients/active/<name>.ovpn` file before writing the same profile to standard
output. If an active profile was deleted, the instance correctly becomes
`DEGRADED_REPAIRABLE`; restore it from the current persisted configuration
before exporting:

```bash
docker compose run --rm openvpn-maintenance repair
```

## Client Management

Create a client certificate and profile:

`laptop` is only an example client name. Replace it consistently with a unique
device name, such as `phone` or `nas`.

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

## Client IP Management

data/client-ip.csv is the sole IP-assignment fact: a non-empty second column is
a static address and an empty column is dynamic. The accepted registry is
mirrored to meta/client-ip.applied.csv; CCD files and
/var/lib/openvpn/pool-persist.txt are derived state, never a static-address
source.

Create or change assignments through the standard commands:

~~~bash
docker compose exec openvpn ovpn client create phone
docker compose exec openvpn ovpn client create tablet --dynamic
docker compose exec openvpn ovpn client set-static phone --ip 10.42.0.2
docker compose exec openvpn ovpn client set-dynamic phone
~~~

For a deliberate direct edit, modify only
./openvpn-data/data/client-ip.csv, then explicitly validate and apply it:

~~~bash
docker compose exec openvpn ovpn client-ip validate
docker compose exec openvpn ovpn client-ip apply
~~~

validate is read-only. apply checks client identities, duplicate or out-of-range
addresses, static/dynamic pool separation, capacity, and PKI state under the
shared lock. On success it sorts static entries by numeric IP and dynamic
entries by client name, regenerates CCD, clears affected dynamic leases, and
disconnects affected online clients through the local root-only management
socket. On rejection or a later transaction failure it restores the draft
exactly from the applied snapshot. client-ip sync remains an alias for apply;
client-ip edit only opens the draft.

A pending direct edit is never started or repaired automatically. doctor reports
it as waiting for explicit application.

## Network and Dynamic-Pool Migration

Changing the tunnel CIDR or dynamic-pool size is a migration, not a config init
update. Run it inside the live openvpn service (not the low-privilege
maintenance container), because it uses that container's local root-only
management socket:

~~~bash
docker compose exec openvpn ovpn network reconfigure \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --dry-run
docker compose exec openvpn ovpn network reconfigure \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --yes
~~~

The preview preserves valid static IPs where possible, otherwise retains the
host portion or allocates the lowest free static address. The confirmed
operation snapshots configuration, registries, CCD, leases, rendered server
configuration, and the audit log; reloads OpenVPN with SIGHUP; and waits for
management-socket and container health. A failed reload or health check restores
the snapshot and reloads the old server configuration. Expect affected clients
to reconnect.

## Operations and Maintenance

The minimal quick-start configuration omits maintenance. To add one-shot state
inspection and repair, append this service under `services:`; the repository
template already includes it. It mounts the same data but does not request TUN,
`NET_ADMIN`, or published ports.

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    volumes:
      - ./openvpn-data:/etc/openvpn
      - ./openvpn-runtime:/var/lib/openvpn
    profiles:
      - maintenance
    command:
      - doctor
    entrypoint:
      - /usr/local/bin/ovpn
```

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

A consistent backup includes config/, data/, meta/, pki/, secrets/, ccd/, and
clients/, plus the separately mounted ./openvpn-runtime/pool-persist.txt
dynamic-lease file. The audit log and applied registry snapshot are in meta/;
do not copy private keys into tickets, logs, or ad-hoc recovery notes. Prefer
stopping the service before taking a backup. After restoring both volumes, run
doctor, review repair --plan, and then start the server.

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

## License

Copyright (C) 2026 yjrszcq.

The original source code and build configuration in this repository are
licensed under [GPL-2.0-only](LICENSE). [NOTICE](NOTICE) defines that scope and
identifies third-party components. Container images include OpenVPN Community
Edition and other third-party components under their own licenses; they are not
relicensed by this project. The image includes this project's license files and
OpenVPN's `COPYING` file under `/usr/local/share/licenses/`.

Release images are built from this source tree and the checksum-pinned OpenVPN
source declared in [versions.env](versions.env). The build retrieval logic is
in [scripts/fetch-openvpn-source.sh](scripts/fetch-openvpn-source.sh).
