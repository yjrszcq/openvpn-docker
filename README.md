# OpenVPN Server Docker Image

[中文](README_CN.md)

This image runs OpenVPN Community Edition with a Go control plane and SQLite
schema 4 state. It is intended for Linux hosts that need certificate-based
IPv4 TUN access without a web interface.

## What v4 provides

- Go binaries for the CLI, entrypoint, OpenVPN hook, supervisor, and management
  broker. Python and the legacy runtime Shell control plane are not included.
- SQLite at `/etc/openvpn/meta/state.db` as the sole authority for structured
  configuration, client, address, artifact metadata, audit, and operation
  state.
- Easy-RSA remains the PKI authority. Certificates, private keys, CRL,
  tls-crypt material, profiles, CCD files, and logs remain files under the data
  directory.
- Declarative YAML configuration with strict unknown-field, duplicate-field,
  type, null, and multi-document rejection.
- IPv4 TUN addressing, static and dynamic allocation, NAT, route and DNS
  pushes, and client-to-client operation.
- UDP or TCP transport over public IPv4 or IPv6. IPv6 tunnel addressing and a
  dual-stack VPN data plane are not implemented yet.
- `linux/amd64` and `linux/arm64` images built from checksum-pinned OpenVPN
  source.

The project does not currently provide a web UI, TAP, LDAP/RADIUS/OIDC,
Kubernetes integration, PostgreSQL/MySQL storage, or HA coordination.

## Quick start

### Requirements

- Docker Engine with the Docker Compose plugin.
- A Linux host with `/dev/net/tun` and permission to grant `NET_ADMIN`.
- A public hostname or IP reachable by clients, with the selected OpenVPN port
  allowed by host and cloud firewalls.
- A private IPv4 CIDR that does not overlap the server or client networks.

### Create the configuration

Create the persistent and configuration directories:

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
```

Create `openvpn-config/config.yaml`:

```yaml
version: 1

server:
  endpoint: vpn.example.com
  transport:
    protocol: udp
    family: auto
    port: 1194
  clientToClient: true

ipv4:
  network: 10.42.0.0/24
  dynamicPoolSize: 64
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  dns: []
  routes: []

logging:
  maxBytes: 10485760
  backups: 5
```

Only `server.endpoint` and `ipv4.network` are required beyond `version: 1`.
Omitted values use the defaults shown above, except `dynamicPoolSize`, which
defaults to half of the usable client addresses.

Create `compose.yaml`:

```yaml
x-openvpn-data: &openvpn-data
  volumes:
    - ./openvpn-data:/etc/openvpn
    - ./openvpn-config:/etc/openvpn-config

services:
  openvpn:
    image: ${OVPN_IMAGE:-szcq/openvpn:2.7.5}
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    <<: *openvpn-data
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun

  openvpn-maintenance:
    image: ${OVPN_IMAGE:-szcq/openvpn:2.7.5}
    restart: "no"
    network_mode: host
    environment:
      OVPN_MAINTENANCE: "true"
    <<: *openvpn-data
    profiles:
      - maintenance
    entrypoint:
      - /usr/local/bin/ovpn
    command:
      - state
      - doctor
```

Docker Hub tags follow the embedded OpenVPN version. The v4.0.0 project image
shown here contains OpenVPN 2.7.5. Pin a concrete tag in production.

### Initialize and start

```bash
docker compose up -d openvpn
docker compose logs -f openvpn
```

The entrypoint initializes only an empty data directory and requires a valid
YAML file for a new instance. Initialization creates the SQLite database, PKI,
server identity, CRL, tls-crypt key, and derived runtime files as one staged
operation.

YAML is the desired configuration; SQLite stores the last operator-confirmed
applied revision. If YAML later becomes missing or differs from that revision,
`server run` warns and continues with the applied SQLite snapshot. It never
applies configuration implicitly.

### Create and export a client

```bash
# Lowest available static IPv4 address
docker compose exec openvpn ovpn client create laptop --ipv4 auto

# Dynamic address
docker compose exec openvpn ovpn client create phone --ipv4 dynamic

# Explicit static address
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

docker compose exec -T openvpn \
  ovpn client export --name laptop --output - > laptop.ovpn
```

Import the resulting profile into an OpenVPN client. Profiles contain private
keys and must be transported and stored as credentials.

## Configuration workflow

Validate and preview YAML without changing applied state:

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

Applying configuration is deliberately offline. Stop the server, inspect the
plan, apply under the exclusive lock, then restart:

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
```

The plan reports restart, address remap, firewall reconciliation, derived-file,
and profile redistribution impact. Network and dynamic-pool changes are part of
the same `config apply`; there is no separate online network migration command.

## Common operations

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime logs --lines 100 --follow
docker compose exec openvpn ovpn runtime events --lines 100 --json

docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose run --rm openvpn-maintenance repair apply --yes
```

Existing clients must be selected with exactly one of `--name` or `--id`.
`--id` accepts an unambiguous UUID prefix of at least eight hexadecimal
characters. Mutating commands that can destroy or broadly rewrite state require
interactive confirmation or `--yes`.

## Schema 3 migration

The v4 image directly migrates schema 3 only. Schema 1 or 2 instances must
first be upgraded to schema 3 with the `sh-ver` image.

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance \
  config export --output /etc/openvpn-config/config.yaml
docker compose up -d openvpn
```

Migration creates `/etc/openvpn/repair/migrations/schema3-pre-v4.tar.gz` and a
matching SHA-256 sidecar before installing schema 4. To roll back after a
successful migration, stop all containers, verify and restore the complete
snapshot, then run the `sh-ver` image. Switching the image alone is not a data
rollback.

## Backup and restore

The database and all PKI/artifact files are one restore unit. Never copy only
`state.db`, and never copy a database while the service can write it. For an
operator backup, stop the server and archive both mounted directories:

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-backup.tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

Restore into empty target directories while the service is stopped, preserve
ownership and permissions, then run `state doctor` before startup. Backups
contain CA and client private keys and must be encrypted and access-controlled.

## Documentation

- [v4 command reference](docs/en/v4/commands.md)
- [v4 operations guide](docs/en/v4/operations.md)
- [data schema upgrade policy](docs/en/data-schema-upgrade-policy.md)
- [image update policy](docs/en/image-update-policy.md)
- Historical references: [v1](docs/en/v1/commands.md),
  [v2](docs/en/v2/commands.md), and [v3](docs/en/v3/commands.md)

## Development

Version inputs live in `versions.env`. Go dependencies use `GOPROXY=direct`;
the build wrapper forwards the host's standard proxy variables to Docker.

```bash
scripts/verify-go-toolchain.sh
tests/smoke/shell/check.sh
tests/smoke/shell/workflow-smoke.sh
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

CI runs Go formatting, vet, unit and race tests, dependency-license checks,
retained Shell contracts, schema 3 handoff/rollback, real UDP/TCP tunnels, and
amd64/arm64 builds.

## License

Copyright (C) 2026 yjrszcq.

Original project source and build configuration are licensed under
[GPL-2.0-only](LICENSE). [NOTICE](NOTICE) records the third-party boundary.
OpenVPN, Easy-RSA, Go modules, and system packages retain their own licenses.
