# OpenVPN Server Docker Image

[中文](README_CN.md)

This image runs OpenVPN Community Edition with a Go control plane and SQLite state. It is intended for Linux hosts that need certificate-based IPv4 TUN access without a web interface.

## Features

- Go binaries provide the CLI, entrypoint, OpenVPN hook, process supervisor, and management broker.
- SQLite at `/etc/openvpn/meta/state.db` as the sole authority for structured configuration, client, address, artifact metadata, audit, and operation state.
- Easy-RSA remains the PKI authority. Certificates, private keys, CRL, tls-crypt material, profiles, CCD files, and logs remain files under the data directory.
- Declarative YAML configuration with strict unknown-field, duplicate-field, type, null, and multi-document rejection.
- IPv4 TUN addressing, static and dynamic allocation, NAT, route and DNS pushes, and client-to-client operation.
- UDP or TCP transport over public IPv4 or IPv6. IPv6 tunnel addressing and a dual-stack VPN data plane are not implemented yet.
- `linux/amd64` and `linux/arm64` images built from checksum-pinned OpenVPN source.

The project does not currently provide a web UI, TAP, LDAP/RADIUS/OIDC, Kubernetes integration, PostgreSQL/MySQL storage, or HA coordination.

## Quick start

### Requirements

- Docker Engine with the Docker Compose plugin.
- A Linux host with `/dev/net/tun` and permission to grant `NET_ADMIN`.
- A public hostname or IP reachable by clients, with the selected OpenVPN port allowed by host and cloud firewalls.
- A private IPv4 CIDR that does not overlap the server or client networks.

### Create the deployment

Create the persistent and configuration directories:

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
```

Create `compose.yaml`. This version is self-contained and does not require a `.env` file. Replace `vpn.example.com` and choose a non-overlapping IPv4 network before starting:

```yaml
x-openvpn-data: &openvpn-data
  volumes:
    - ./openvpn-data:/etc/openvpn
    - ./openvpn-config:/etc/ovpn-conf

services:
  openvpn:
    image: szcq/openvpn:2.7.5
    container_name: openvpn
    restart: unless-stopped
    network_mode: host
    environment:
      OVPN_BOOTSTRAP_FROM_ENV: "true"
      OVPN_BOOTSTRAP_ENDPOINT: vpn.example.com
      OVPN_BOOTSTRAP_IPV4_NETWORK: 10.42.0.0/24
    <<: *openvpn-data
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun

  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
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

Docker Hub tags follow the embedded OpenVPN version. The image shown here contains OpenVPN 2.7.5. Pin a concrete tag in production.

The example shows only the three values required for one-time environment initialization; all omitted settings use the normal defaults. See the [environment-variable table](#environment-variables), [.env.example](.env.example), or the [bootstrap command reference](docs/en/v4/commands.md#one-time-environment-bootstrap) for every optional variable. After the first successful start, change `OVPN_BOOTSTRAP_FROM_ENV` to `"false"`; later bootstrap values are ignored and never overwrite YAML or SQLite.

To skip environment initialization and manage the configuration file yourself, create it before the first start:

```bash
cp config.example.yaml openvpn-config/config.yaml
$EDITOR openvpn-config/config.yaml
```

Then remove the entire `environment:` block from the `openvpn` service in the Compose example. The mounted file becomes the desired configuration; only `version`, `server.endpoint`, and `ipv4.network` are required, and the example file documents the remaining defaults.

### Initialize and start

```bash
docker compose up -d openvpn
docker compose logs -f openvpn
```

The entrypoint initializes only an empty data directory and requires either a valid YAML file or enabled bootstrap environment for a new instance. Environment bootstrap writes the canonical YAML first; initialization then creates the SQLite database, PKI, server identity, CRL, tls-crypt key, and derived runtime files as one staged operation.

YAML is the desired configuration; SQLite stores the last operator-confirmed applied revision. If YAML later becomes missing or differs from that revision, `server run` warns and continues with the applied SQLite snapshot. It never applies configuration implicitly.

### Create and export a client

```bash
# Lowest available static IPv4 address, with the profile returned directly
docker compose exec -T openvpn \
  ovpn client create laptop --ipv4 --output - > laptop.ovpn

# Dynamic address
docker compose exec openvpn ovpn client create phone --ipv4 dynamic

# Explicit static address
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

chmod 600 laptop.ovpn
```

Import the resulting profile into an OpenVPN client. Profiles contain private keys and must be transported and stored as credentials.

## Environment variables

Persistent server settings belong in declarative YAML. Environment variables configure Compose, filesystem locations, one-time initialization, maintenance authorization, and development overrides; they are not a second long-lived configuration source.

### Deployment and operation

| Variable | Runtime default / Compose fallback | `.env.example` value | Purpose |
|---|---|---|---|
| `OVPN_IMAGE` | `szcq/openvpn:2.7.5` | `szcq/openvpn:2.7.5` | Image used by Compose. Pin a released tag in production. |
| `OVPN_CONFIG_FILE` | `/etc/ovpn-conf/config.yaml` | unset | Desired declarative YAML path. |
| `OVPN_DATA_DIR` | `/etc/openvpn` | unset | Persistent data directory containing SQLite, PKI, artifacts, logs, and locks. |
| `OVPN_RUNTIME_DIR` | `/run/openvpn-container` | unset | Ephemeral directory for runtime sockets and the server-process lock. |
| `OVPN_MAINTENANCE` | unset | unset | Must be exactly `true` for `migrate apply`; the Compose maintenance service sets it automatically. |
| `OVPN_EDITOR` | `EDITOR`, then `nano` | unset | Editor command used by `client address edit`. |
| `EDITOR` | `nano` | unset | Standard fallback editor when `OVPN_EDITOR` is unset. |

### One-time environment bootstrap

These variables are read only while initializing an empty instance with `OVPN_BOOTSTRAP_FROM_ENV=true`. They produce the first canonical YAML file. After initialization they are ignored with a warning and never overwrite YAML or SQLite; set the switch to `false` after the first successful start.

| Variable | Initial configuration default | `.env.example` value | Purpose |
|---|---|---|---|
| `OVPN_BOOTSTRAP_FROM_ENV` | `false` | `true` | Enables generation of the initial YAML from the remaining bootstrap variables. |
| `OVPN_BOOTSTRAP_ENDPOINT` | required | `vpn.example.com` | Public hostname or IP used by client profiles. Replace the example before starting. |
| `OVPN_BOOTSTRAP_PROTOCOL` | `udp` | `udp` | Public transport protocol: `udp` or `tcp`. |
| `OVPN_BOOTSTRAP_FAMILY` | `auto` | `auto` | Public transport family: `auto`, `ipv4`, or `ipv6`; this does not enable IPv6 tunnel addressing. |
| `OVPN_BOOTSTRAP_PORT` | `1194` | `1194` | OpenVPN listen port. |
| `OVPN_BOOTSTRAP_CLIENT_TO_CLIENT` | `true` | `true` | Allows direct traffic between VPN clients. |
| `OVPN_BOOTSTRAP_IPV4_NETWORK` | required | `10.42.0.0/24` | Canonical, non-overlapping IPv4 tunnel network from `/30` through `/0`. |
| `OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE` | half of usable client addresses | `64` | Tail of the usable range reserved for dynamic clients; `0` disables the dynamic pool. |
| `OVPN_BOOTSTRAP_NAT_ENABLED` | `false` | `false` | Masquerades client traffic leaving the VPN network namespace. |
| `OVPN_BOOTSTRAP_NAT_INTERFACE` | `auto` | `auto` | NAT egress interface, or `auto` to resolve it from the route table. |
| `OVPN_BOOTSTRAP_REDIRECT_GATEWAY` | `false` | `false` | Routes client default traffic through the VPN. |
| `OVPN_BOOTSTRAP_DNS` | empty | empty | Comma-separated IPv4 DNS servers pushed to clients. |
| `OVPN_BOOTSTRAP_ROUTES` | empty | empty | Comma-separated canonical IPv4 CIDRs pushed to clients. |
| `OVPN_BOOTSTRAP_LOG_MAX_BYTES` | `10485760` | `10485760` | Maximum persistent OpenVPN log size in bytes before rotation. |
| `OVPN_BOOTSTRAP_LOG_BACKUPS` | `5` | `5` | Rotated log backups to retain; `0` disables backup retention. |

### Development and test overrides

These variables replace trusted files, executables, or host networking interfaces. The production image supplies valid defaults; normal deployments should not set them.

| Variable | Default | Purpose |
|---|---|---|
| `OVPN_COMPATIBILITY_FILE` | `/usr/local/share/openvpn-container/compatibility/contract.json` | Compatibility contract read by rendering, initialization, and repair. |
| `OVPN_TEMPLATE_ROOT` | `/usr/local/share/openvpn-container/templates` | Root containing the template family selected by the compatibility contract. |
| `OVPN_OPENVPN_BIN` | `openvpn` | OpenVPN executable used by runtime supervision, PKI validation, and capability inspection. |
| `OVPN_BROKER_BIN` | `ovpn-broker` | Management broker executable supervised by `server run`. |
| `OVPN_EASYRSA_BIN` | `/usr/share/easy-rsa/easyrsa`, otherwise `easyrsa` | Easy-RSA executable used for PKI lifecycle operations. |
| `OVPN_IP_BIN` | `ip` | Linux `ip` executable used by network reconciliation. |
| `OVPN_IPTABLES_BIN` | `iptables` | Linux `iptables` executable used by firewall reconciliation. |
| `OVPN_IP_FORWARD_FILE` | `/proc/sys/net/ipv4/ip_forward` | Host IPv4 forwarding control file used by network reconciliation. |

## Configuration workflow

Validate and preview YAML without changing applied state:

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

Apply through the running container. The supervisor temporarily stops OpenVPN and its broker, performs the exclusive configuration transaction, then restarts the managed runtime before the command returns:

```bash
docker compose exec openvpn ovpn config plan
docker compose exec openvpn ovpn config apply --yes
```

Before changing state, apply checks SQLite, PKI, certificates, CRL, and artifacts and refuses a non-healthy instance. Review `ovpn state doctor` and repair the cause first. `--force/-f` bypasses only this preflight result when it is known to be a false negative; schema, path, lock, pending-operation, and transaction safety checks still apply.

The container remains running, but connected VPN clients are disconnected during the controlled OpenVPN restart. The plan reports restart, address remap, firewall reconciliation, derived-file, and profile redistribution impact. Network and dynamic-pool changes are part of the same `config apply`; there is no separate network migration command. The stopped `openvpn-maintenance` workflow remains available for recovery and explicit offline operation; it applies the data change without starting runtime processes.

## Common operations

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime disconnect laptop
docker compose exec openvpn ovpn runtime logs --lines 100 --follow
docker compose exec openvpn ovpn runtime events --lines 100 --json

docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose run --rm openvpn-maintenance repair apply --yes
```

Existing clients may be selected by positional `NAME`, explicit `--name NAME`, or `--id ID`. When neither selector option is present, the positional value is treated as the client name. `--id` accepts an unambiguous UUID prefix of at least eight hexadecimal characters. Mutating commands that can destroy or broadly rewrite state require interactive confirmation or `--yes`.

`ovpn client`, `ovpn state`, and `ovpn runtime` default to `list`, `doctor`, and `status`. Client mutations support `--json`; create and reissue can return the new profile with `--output`. Revoke, reissue, delete, and address changes try to disconnect affected live sessions after the durable commit. A runtime warning means the state change succeeded and `runtime disconnect` can be used as a manual retry.

Generate shell completion without an external CLI framework:

The generated script completes a direct command named `ovpn`. Use it inside an interactive service container, or define a host wrapper named `ovpn` that runs `docker compose exec openvpn ovpn`. When generating through Compose, replace `ovpn completion` below with `docker compose exec -T openvpn ovpn completion`. Dynamic client name/ID completion also uses that direct command or wrapper.

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

## Backup and restore

The database and all PKI/artifact files are one restore unit. Never copy only `state.db`, and never copy a database while the service can write it. For an operator backup, stop the server and archive both mounted directories:

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-backup.tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

Restore into empty target directories while the service is stopped, preserve ownership and permissions, then run `state doctor` before startup. Backups contain CA and client private keys and must be encrypted and access-controlled.

## Documentation

- [command reference](docs/en/v4/commands.md)
- [operations guide](docs/en/v4/operations.md)
- [data upgrade and migration policy](docs/en/data-schema-upgrade-policy.md)
- [image update policy](docs/en/image-update-policy.md)
- Historical references: [v1](docs/en/v1/commands.md), [v2](docs/en/v2/commands.md), and [v3](docs/en/v3/commands.md)

## Development

Version inputs live in `versions.env`. Go dependencies use `GOPROXY=direct`; the build wrapper forwards the host's standard proxy variables to Docker.

```bash
scripts/verify-go-toolchain.sh
tests/smoke/shell/check.sh
tests/smoke/shell/workflow-smoke.sh
scripts/docker-build.sh -t szcq/openvpn-server:dev .
```

CI runs Go formatting, vet, unit and race tests, dependency-license checks, retained Shell contracts, data migration and rollback, real UDP/TCP tunnels, and amd64/arm64 builds.

## License

Copyright (C) 2026 yjrszcq.

Original project source and build configuration are licensed under [GPL-2.0-only](LICENSE). [NOTICE](NOTICE) records the third-party boundary. OpenVPN, Easy-RSA, Go modules, and system packages retain their own licenses.
