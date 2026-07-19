# OpenVPN CLI v2 Reference

This is the complete command reference for the current CLI in this source tree.

## Scope and conventions

- Invoke commands through the image entry point as `ovpn <command>`. In Docker Compose, this is commonly `docker compose exec openvpn ovpn <command>`.
- Persistent instance data defaults to `/etc/openvpn`; `OVPN_DATA_DIR` changes that location. Runtime state defaults to `/run/openvpn-container/state.json`.
- A client name must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`.
- Commands marked read-only do not change persistent data. Commands that alter data use the shared data lock and either apply a transaction or explicitly require confirmation.
- Configuration, client, and network operations operate on the persistent instance. Run them against the service holding the mounted data directory.

## Command tree

Every command and subcommand supports `--help` / `-h`. The tree below shows every leaf with its `--help` description:

```
ovpn
├── init                Initialize an empty OpenVPN data directory.
├── start               Scan state and start OpenVPN.
├── config
│   ├── show            Print persisted project configuration.
│   └── apply           Validate environment and write persistent project configuration.
├── client
│   ├── create          Create a client certificate, profile, and IP assignment.
│   ├── export          Write an active client profile to stdout.
│   ├── list            List client certificate state and optional detailed IP assignment.
│   ├── revoke          Revoke a client certificate, optionally release its static IP.
│   ├── reissue         Issue a new certificate for an existing client, optionally adjusting IP assignment.
│   ├── delete          Remove a client and its local credentials.
│   └── ip
│       ├── release     Release the retained static IP of a revoked client.
│       └── set         Assign client IP addresses.
├── network
│   ├── plan            Preview a tunnel-network migration.
│   └── apply           Apply a tunnel-network migration.
├── repair
│   ├── plan            Inspect eligible repair actions.
│   └── apply           Apply eligible repair actions.
├── state
│   ├── show            Print the detected instance state.
│   └── doctor          Print detected issues and recommended actions.
├── render
│   ├── server          Render the server configuration.
│   └── client          Render a client profile.
├── runtime
│   ├── status          Print runtime state as JSON.
│   ├── health          Return success only when the container is healthy.
│   ├── capabilities    Print compatibility and feature information.
│   └── version         Print image and runtime build information.
└── help                Print this help message.
```

## Help and lifecycle

### `ovpn help`

Syntax:

```text
ovpn help
ovpn -h
ovpn --help
```

Prints the top-level command tree. It does not inspect or modify instance data.

### `ovpn -v` / `ovpn --version`

Syntax:

```text
ovpn -v
ovpn --version
```

`-v` prints only the image version (e.g. `2.1.1`). `--version` prints a three-line summary with image, OpenVPN, and Easy-RSA versions:

```text
image:     2.1.1
openvpn:   2.7.5
easy-rsa:  3.2.2
```

Use `ovpn runtime version` for the complete build-information JSON.

### `ovpn init`

Syntax:

```text
ovpn init
```

Initializes an empty persistent data directory in one transaction. It creates the project configuration, PKI and CA, server identity, CRL, tls-crypt key, metadata, server configuration, client-IP registry, generated-state directories, and repair directories. It refuses to initialize a non-empty data directory.

Use it for explicit provisioning. `ovpn start` initializes automatically when the data directory is empty.

### `ovpn start`

Syntax:

```text
ovpn start
```

Container entry-point operation. It initializes an empty instance, scans state, applies eligible automatic repairs, validates runtime compatibility, configures networking, renders the server configuration, records healthy runtime state, and starts OpenVPN in the foreground.

Unsafe states are not started. With `OVPN_CRITICAL_MODE=maintenance`, a critical or unrecoverable state enters maintenance mode for inspection instead of immediately terminating the container.

## Persistent configuration

### `ovpn config show`

Syntax:

```text
ovpn config show
```

Read-only. Loads persistent configuration and prints one `KEY=VALUE` line for all configuration facts: schema version, endpoint, transport protocol, public transport address family, port, network, topology, dynamic-pool size, NAT settings, gateway redirection, client-to-client setting, DNS servers, and pushed routes.

### `ovpn config apply`

Syntax:

```text
ovpn config apply
```

Validates the current `OVPN_*` environment and atomically replaces `config/project.env` and `config/schema-version` with mode `0600`. It writes configuration schema version `2` and does not issue, revoke, delete, or reissue client certificates.

`OVPN_ENDPOINT` must be a valid hostname or IP string; `OVPN_PROTO` is `udp` or `tcp`; `OVPN_TRANSPORT_FAMILY` is `auto`, `ipv4`, or `ipv6`. The persisted `auto` value is retained: rendering detects IPv4 and IPv6 literals, while a hostname selects a dual-stack server transport and a family-neutral client transport. `config apply` does not resolve DNS. `OVPN_TOPOLOGY` is `subnet`; boolean fields are `true` or `false`; `OVPN_DNS` and `OVPN_ROUTES` are comma-separated IPv4 values; and the network and dynamic-pool size must form a valid IPAM layout. Apply writes every configuration value from the current environment.

## Client lifecycle

### `ovpn client create`

Syntax:

```text
ovpn client create <name> [--dynamic|--ip <IPv4>]
```

Creates a unique client certificate, private key, active profile, registry record, and IP assignment in one transaction. Without an option the client receives the lowest available static address. `--dynamic` creates a dynamic assignment and requires a nonzero dynamic-pool capacity. `--ip <IPv4>` requests a specific unused address in the static region. `--dynamic` and `--ip` cannot be combined.

### `ovpn client export`

Syntax:

```text
ovpn client export <name>
```

Requires a healthy active client. Regenerates `clients/active/<name>.ovpn` atomically, then writes the same profile to standard output. Redirect standard output to save the client profile.

### `ovpn client list`

Syntax:

```text
ovpn client list [--detail]
```

Without `--detail`, prints a two-column table with `CLIENT` and `STATE` headers, auto-sized to the widest name. With `--detail`, prints the aligned columns `CLIENT`, `STATE`, `MODE`, `IP`, `IP STATE`, and `CONNECTION`.

For the IP view, static assignments are `configured` or `retained` after revocation. Dynamic addresses are `connected` when the management socket has a current lease, `last-known` when a persisted lease record exists, or `unavailable`. `CONNECTION` is `online`, `offline`, or `unknown` according to management-socket availability and the current route. The view reads the applied registry, not an unapplied draft.

### `ovpn client revoke`

Syntax:

```text
ovpn client revoke <name> [--release-ip]
```

Revokes an active certificate, regenerates the CRL, moves its active profile to `clients/revoked/`, records the client as revoked, and disconnects it when the management socket is available. By default, a static assignment remains reserved. `--release-ip` releases that static reservation as part of the same operation.

### `ovpn client reissue`

Syntax:

```text
ovpn client reissue <name> [--dynamic|--ip <IPv4>]
```

Issues a new key and certificate for an existing client name. For an active client, it first revokes the old certificate and moves its profile to the revoked set. It probes the shipped Easy-RSA runtime for same-CN reissue support before changing the live PKI, so a failing validation causes no changes.

Clients that already have a static IP keep their assignment by default. Clients without an IP (released or originally dynamic) auto-allocate the lowest available static IP; reissue is refused when the static region has no free capacity. Options:

- `--dynamic` → use a dynamic assignment after reissue; requires nonzero dynamic-pool capacity.
- `--ip <IPv4>` → use the specified static address, which must lie inside the static region and be unoccupied.

### `ovpn client delete`

Syntax:

```text
ovpn client delete <name>
```

Irreversibly removes a client. An active client is revoked first; the command then removes its registry record, active or revoked profile, private key, issued certificate, and request file. Recover an old private key only from a secure backup.

## Client IP management

The draft registry is `data/client-ip.csv`; the last accepted registry is `meta/client-ip.applied.csv`. Both use `client,ip` rows. A non-empty IP is a static assignment; an empty IP is dynamic. The optional first line is exactly `# client,ip`. Names and static addresses must be unique, static addresses must fall in the static region, and the registry must contain every logical PKI client.

### `ovpn client ip release`

Syntax:

```text
ovpn client ip release <name>
```

Releases the retained static assignment of a revoked client. The client must be revoked and must still have a static reservation. The revoked profile, private key, certificate history, and audit history remain.

### `ovpn client ip set`

Syntax:

```text
ovpn client ip set <client...|--all> [--dynamic|--ip <IPv4>]
```

Sets active clients to the specified IP assignment and applies the transaction immediately.

Single-client mode:
- No flag → auto-allocate the lowest available static address
- `--ip <IPv4>` → assign an explicit static address
- `--dynamic` → assign a dynamic address

Multiple clients or `--all` → opens an editor containing `client,ip` rows with three assignment modes:

- Enter `auto` to allocate the lowest available static address
- Enter an explicit IPv4 address to assign a specific static IP
- Leave the IP empty to keep the client dynamic

```text
laptop,auto               # auto-allocate
phone,10.88.0.20          # explicit assignment
desktop,                  # keep dynamic
```

The editor is chosen from `OVPN_EDITOR`, then `EDITOR`, then `nano` (image ships `nano` and `vim`).

## Tunnel-network migration

### `ovpn network plan`

Syntax:

```text
ovpn network plan [--network <CIDR>] [--dynamic-pool-size <N>]
```

Read-only. Builds and prints a migration plan for the requested values, using current values for omitted options. The plan preserves valid static assignments where possible, otherwise retains a host portion when valid or allocates the lowest free static address. It does not contact the management socket.

### `ovpn network apply`

Syntax:

```text
ovpn network apply [--network <CIDR>] [--dynamic-pool-size <N>] [--yes]
```

Builds and prints the same plan, then asks for confirmation on an interactive terminal unless `--yes` is supplied. It snapshots configuration, registries, CCD, leases, rendered server configuration, and audit state; updates the configuration and registry; reloads OpenVPN; and checks the management socket and container health. A failed reload or health check restores the snapshot and reloads the old configuration.

Run this against the live OpenVPN service because applying requires its local management socket. After a successful migration, update `OVPN_NETWORK` (and `OVPN_DYNAMIC_POOL_SIZE` if changed) in `docker-compose.yaml` — the persisted `project.env` holds the new value, but `ovpn config apply` reads environment variables and would revert to the stale compose-file value.

## State and repair

### `ovpn repair plan`

Syntax:

```text
ovpn repair plan [--json]
```

Read-only. Scans the instance and prints eligible automatic actions and blocked issues. The text report labels actions `SAFE` or `RECOVER`; `--json` emits the state, actions, and blocked entries as JSON. Critical and unrecoverable states return exit status `78` after reporting.

Eligible actions include restoring derived configuration or metadata, regenerating a CRL, rendering a missing active profile, recovering verified certificate or key copies, and creating the runtime directory.

### `ovpn repair apply`

Syntax:

```text
ovpn repair apply
```

Builds the plan and applies eligible actions under the data lock. It stages and validates the result, snapshots affected persistent files, installs the changes atomically, writes a repair journal, and restores the snapshot if the transaction fails. Unsafe states are refused.

### `ovpn state show`

Syntax:

```text
ovpn state show
```

Read-only. Prints the detected instance state, including `EMPTY`, `HEALTHY`, repairable or recoverable degraded states, and critical or unrecoverable states.

### `ovpn state doctor`

Syntax:

```text
ovpn state doctor [--json]
```

Read-only. Prints the detected state and every issue with its severity and recommended action. `--json` emits an object with `state` and `issues`. Critical and unrecoverable states return exit status `78` after output.

## Rendering

### `ovpn render server`

Syntax:

```text
ovpn render server [--stdout|--output <path>]
```

Renders the server configuration from persistent configuration, transport address family, IPAM layout, PKI paths, and the compatible template family. `auto` infers `ipv4` or `ipv6` from IP literals. For hostnames it renders an IPv6 dual-stack server socket (`udp6` or `tcp6-server` without `bind ipv6only`); client profiles preserve family-neutral `udp`/`tcp` and resolve A/AAAA records when connecting. The socket accepts both native IPv6 and IPv4-mapped peers. Explicit `ipv6` adds `bind ipv6only`; explicit `ipv4` needs no matching bind option because its IPv4 socket cannot accept IPv6. With no output option it atomically updates `server/server.conf`; `--stdout` writes the result to standard output; `--output <path>` writes a mode-`0600` file at that path.

### `ovpn render client`

Syntax:

```text
ovpn render client <name> [--stdout|--output <path>]
```

Builds a client `.ovpn` profile from the configured endpoint, CA certificate, named client certificate and key, and tls-crypt key. Output defaults to standard output; `--output` writes an atomically replaced mode-`0600` file.

## Runtime inspection

### `ovpn runtime status`

Syntax:

```text
ovpn runtime status
```

Prints runtime-state JSON. If the runtime-state file is unavailable, it returns a synthesized object with the detected instance state, `daemon` set to `unknown`, and `maintenance` set to `false`.

### `ovpn runtime health`

Syntax:

```text
ovpn runtime health
```

Returns success only when runtime state reports a healthy, running, non-maintenance service and both `/dev/net/tun` and an OpenVPN process are present. On failure it writes a reason to standard error and returns nonzero.

### `ovpn runtime capabilities`

Syntax:

```text
ovpn runtime capabilities
```

Prints JSON for the detected OpenVPN version, support-range result, selected compatibility adapter, and required runtime features. It returns nonzero when the version, adapter, or any required feature is unsupported.

### `ovpn runtime version`

Syntax:

```text
ovpn runtime version
```

Prints build-information JSON from `/usr/local/share/openvpn-container/build-info.json`, with the Easy-RSA version detected at runtime. If that file is missing, it detects the Easy-RSA version and prints `unknown` for the remaining fields.

## Examples

```bash
# Create and export a static client profile.
docker compose exec openvpn ovpn client create laptop
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn

# Preview a network change without changing persistent state.
docker compose exec openvpn ovpn network plan --network 10.43.0.0/24 --dynamic-pool-size 96

# Diagnose an instance from the maintenance service.
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```
