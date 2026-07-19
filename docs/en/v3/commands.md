# OpenVPN CLI v3 Reference

This is the complete command reference for the current CLI in this source tree.

## Scope and conventions

- Invoke commands through the image entry point as `ovpn <command>`. In Docker
  Compose, this is commonly `docker compose exec openvpn ovpn <command>`.
- Persistent instance data defaults to `/etc/openvpn`; `OVPN_DATA_DIR` changes
  that location. Runtime state defaults to `/run/openvpn-container/state.json`.
- A client name must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`.
- Commands marked read-only do not change persistent data. Commands that alter
  data use the shared data lock and either apply a transaction or explicitly
  require confirmation.
- Configuration, client, and network operations operate on the persistent
  instance. Run them against the service holding the mounted data directory.

## Command tree

Every command and subcommand supports `--help` / `-h`. The tree below shows
every leaf with its `--help` description:

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
│   ├── rename          Change a client's display name without changing its UUID.
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
│   ├── version         Print image and runtime build information.
│   ├── logs            Read translated or raw persistent OpenVPN logs.
│   └── events          Read structured runtime and lifecycle events.
├── migrate             Plan or apply an offline data-schema migration.
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

`-v` prints only the image version (e.g. `3.0.0`). `--version` prints the image,
data schema, OpenVPN, Easy-RSA, runtime strategy, base image, source revision,
build date, and OpenVPN candidate range:

```text
image:           3.0.0
data schema:     3
openvpn:         2.7.5
easy-rsa:        3.2.2
runtime:         source-build
base image:      debian:trixie-slim
vcs revision:    <commit>
build date:      <UTC timestamp>
candidate:       >=2.7.0 <2.8.0
```

Use `ovpn runtime version` for the complete build-information JSON.

### `ovpn init`

Syntax:

```text
ovpn init
```

Initializes an empty persistent data directory in one transaction. It creates
the project configuration, PKI and CA, server identity, CRL, tls-crypt key,
metadata, server configuration, client-IP registry, generated-state directories,
and repair directories. It refuses to initialize a non-empty data directory.

Use it for explicit provisioning. `ovpn start` initializes automatically when
the data directory is empty.

### `ovpn start`

Syntax:

```text
ovpn start
```

Container entry-point operation. It initializes an empty instance, scans state,
applies eligible automatic repairs, validates runtime compatibility, configures
networking, renders the server configuration, records healthy runtime state,
and starts OpenVPN in the foreground.

Unsafe states are not started. With `OVPN_CRITICAL_MODE=maintenance`, a critical
or unrecoverable state enters maintenance mode for inspection instead of
immediately terminating the container.

## Persistent configuration

### `ovpn config show`

Syntax:

```text
ovpn config show
```

Read-only. Loads persistent configuration and prints one `KEY=VALUE` line for
all configuration facts: schema version, endpoint, transport protocol, public
transport address family, port, network, topology, dynamic-pool size, NAT
settings, gateway redirection, client-to-client setting, DNS servers, and
pushed routes.

### `ovpn config apply`

Syntax:

```text
ovpn config apply
```

Validates the current `OVPN_*` environment and atomically replaces
`config/project.env` and `config/schema-version` with mode `0600`. It writes
data schema version `3` and does not issue, revoke, delete, or reissue
client certificates.

`OVPN_ENDPOINT` must be a valid hostname or IP string; `OVPN_PROTO` is `udp` or
`tcp`; `OVPN_TRANSPORT_FAMILY` is `auto`, `ipv4`, or `ipv6`. The persisted
`auto` value is retained: rendering detects IPv4 and IPv6 literals, while a
hostname selects a dual-stack server transport and a family-neutral client
transport. `config apply` does not resolve DNS.
`OVPN_TOPOLOGY` is `subnet`; boolean fields are `true` or `false`;
`OVPN_DNS` and `OVPN_ROUTES` are comma-separated IPv4 values; and the network
and dynamic-pool size must form a valid IPAM layout. Apply writes every
configuration value from the current environment. `OVPN_LOG_MAX_BYTES` must be
a positive integer and `OVPN_LOG_BACKUPS` a non-negative integer.

## Client lifecycle

Each client has an immutable UUID identity. The certificate CN, Easy-RSA
entity, CCD filename, dynamic-lease filename, and OpenVPN management identity
use that UUID; the client name remains the human-facing label and profile
filename. Generated profiles include `ovpn-client-id` and `ovpn-client-name`
comments so both identities can be recovered without changing OpenVPN syntax.
Except for `create`, each `<client>` argument accepts either the current display
name or the immutable UUID. A UUID cannot be used as a display name.

### `ovpn client create`

Syntax:

```text
ovpn client create <name> [--dynamic|--ip <IPv4>]
```

Creates a unique UUID-backed client certificate, private key, active profile,
registry record, and IP assignment in one transaction. Without an option the
client receives the lowest available static address. `--dynamic` creates a
dynamic assignment and requires a nonzero dynamic-pool capacity. `--ip <IPv4>`
requests a specific unused address in the static region. `--dynamic` and `--ip`
cannot be combined.

### `ovpn client export`

Syntax:

```text
ovpn client export <client>
```

Requires a healthy active client. Regenerates
`clients/active/<name>.ovpn` atomically, then writes the same profile to
standard output. Redirect standard output to save the client profile.

### `ovpn client list`

Syntax:

```text
ovpn client list [--detail]
```

Without `--detail`, prints the aligned columns `CLIENT`, `ID`, and `STATE`.
With `--detail`, it additionally prints `MODE`, `IP`, `IP STATE`, and
`CONNECTION`. The immutable `ID` is shown in both views.

For the IP view, static assignments are `configured` or `retained` after
revocation. Dynamic addresses are `connected` when the management socket has a
current lease, `last-known` when a persisted lease record exists, or `unavailable`.
`CONNECTION` is `online`, `offline`, or `unknown` according to management-socket
availability and the current route. The view reads the applied registry, not an
unapplied draft.

### `ovpn client rename`

Syntax:

```text
ovpn client rename <client> <new-name>
```

Atomically changes the human-facing display name while preserving the UUID,
certificate, key, IP assignment, CCD, lease, and any current OpenVPN
connection. The identity registry, draft and applied IP registries, profile
filename, and embedded name comment change together. The source may be the
current name or UUID. The new name must be valid and unused by a current client.
Renaming or deleting a client releases its old name for reuse; deleted UUID
tombstones remain authoritative history.

### `ovpn client revoke`

Syntax:

```text
ovpn client revoke <client> [--release-ip]
```

Revokes an active certificate, regenerates the CRL, moves its active profile to
`clients/revoked/`, records the client as revoked, and disconnects it when the
management socket is available. By default, a static assignment remains
reserved. `--release-ip` releases that static reservation as part of the same
operation.

### `ovpn client reissue`

Syntax:

```text
ovpn client reissue <client> [--dynamic|--ip <IPv4>]
```

Issues a new key and certificate for an existing client name. For an active
client, it first revokes the old certificate and moves its profile to the
revoked set. It probes the shipped Easy-RSA runtime for same-CN reissue support
before changing the live PKI, so a failing validation causes no changes.

Clients that already have a static IP keep their assignment by default. Clients
without an IP (released or originally dynamic) auto-allocate the lowest available
static IP; reissue is refused when the static region has no free capacity. Options:

- `--dynamic` → use a dynamic assignment after reissue; requires nonzero
  dynamic-pool capacity.
- `--ip <IPv4>` → use the specified static address, which must lie inside the
  static region and be unoccupied.

### `ovpn client delete`

Syntax:

```text
ovpn client delete <client>
```

Irreversibly removes a client. An active client is revoked first; the command
then removes its IP record, active or revoked profile, private key, issued
certificate, and request file, while retaining a UUID tombstone in the identity
registry. The deleted display name may be reused by a new UUID. Recover an old
private key only from a secure backup.

## Client IP management

The draft registry is `data/client-ip.csv`; the last accepted registry is
`meta/client-ip.applied.csv`. Both require a `# id,name,ip` header followed by
`id,name,ip` rows, where `id` is the client's immutable UUID. A non-empty IP is
a static assignment; an empty IP is dynamic. UUIDs, names, and static addresses
must be unique, static addresses must fall in the static region, and the
registry must contain every active or revoked client from the authoritative
identity registry.

### `ovpn client ip release`

Syntax:

```text
ovpn client ip release <client>
```

Releases the retained static assignment of a revoked client. The client must be
revoked and must still have a static reservation. The revoked profile, private
key, certificate history, and audit history remain.

### `ovpn client ip set`

Syntax:

```text
ovpn client ip set <client...|--all> [--dynamic|--ip <IPv4>]
```

Sets active clients to the specified IP assignment and applies the transaction
immediately.

Single-client mode:
- No flag → auto-allocate the lowest available static address
- `--ip <IPv4>` → assign an explicit static address
- `--dynamic` → assign a dynamic address

Multiple clients or `--all` → opens an editor containing `client,ip` rows with
three assignment modes:

- Enter `auto` to allocate the lowest available static address
- Enter an explicit IPv4 address to assign a specific static IP
- Leave the IP empty to keep the client dynamic

```text
laptop,auto               # auto-allocate
phone,10.88.0.20          # explicit assignment
desktop,                  # keep dynamic
```

The editor is chosen from `OVPN_EDITOR`, then `EDITOR`, then `nano` (image ships
`nano` and `vim`).

## Tunnel-network migration

### `ovpn network plan`

Syntax:

```text
ovpn network plan [--network <CIDR>] [--dynamic-pool-size <N>]
```

Read-only. Builds and prints a migration plan for the requested values, using
current values for omitted options. The plan preserves valid static assignments
where possible, otherwise retains a host portion when valid or allocates the
lowest free static address. It does not contact the management socket.

### `ovpn network apply`

Syntax:

```text
ovpn network apply [--network <CIDR>] [--dynamic-pool-size <N>] [--yes]
```

Builds and prints the same plan, then asks for confirmation on an interactive
terminal unless `--yes` is supplied. It snapshots configuration, registries,
CCD, leases, rendered server configuration, and audit state; updates the
configuration and registry; reloads OpenVPN; and checks the management socket
and container health. A failed reload or health check restores the snapshot and
reloads the old configuration.

Run this against the live OpenVPN service because applying requires its local
management socket. After a successful migration, update `OVPN_NETWORK` (and
`OVPN_DYNAMIC_POOL_SIZE` if changed) in `docker-compose.yaml` — the persisted
`project.env` holds the new value, but `ovpn config apply` reads environment
variables and would revert to the stale compose-file value.

## State and repair

### `ovpn repair plan`

Syntax:

```text
ovpn repair plan [--json]
```

Read-only. Scans the instance and prints eligible automatic actions and blocked
issues. The text report labels actions `SAFE` or `RECOVER`; `--json` emits the
state, actions, and blocked entries as JSON. Critical and unrecoverable states
return exit status `78` after reporting.

Eligible actions include restoring derived configuration or metadata,
regenerating a CRL, rendering a missing active profile, recovering verified
certificate or key copies, recovering the current client identity/IP registries,
and creating the runtime directory.

When `meta/client-state.csv` is missing or invalid, recovery starts from UUID
client entries in the current PKI. A display name is accepted only when
current-format draft/applied IP registries, profile identity comments, and the
latest applicable rename audit record agree. Conflicting evidence is
`CRITICAL` and requires a backup or manual review. If no name evidence remains,
repair assigns the deterministic temporary name `client-<uuid-without-dashes>`;
rename it after repair. Historical registry or audit formats are not parsed by
the current runtime. Because the PKI records only active versus revoked
certificates, a deleted tombstone cannot be distinguished from a revoked client
after the authoritative identity registry is lost; repair keeps that UUID
revoked rather than making it active.

### `ovpn repair apply`

Syntax:

```text
ovpn repair apply
```

Builds the plan and applies eligible actions under the data lock. It stages and
validates the result, snapshots affected persistent files, installs the changes
atomically, writes a repair journal, and restores the snapshot if the
transaction fails. Unsafe states are refused.

### `ovpn state show`

Syntax:

```text
ovpn state show
```

Read-only. Prints the detected instance state, including `EMPTY`, `HEALTHY`,
repairable or recoverable degraded states, and critical or unrecoverable states.

### `ovpn state doctor`

Syntax:

```text
ovpn state doctor [--json]
```

Read-only. Prints the detected state and every issue with its severity and
recommended action. `--json` emits an object with `state` and `issues`. Critical
and unrecoverable states return exit status `78` after output.

## Rendering

### `ovpn render server`

Syntax:

```text
ovpn render server [--stdout|--output <path>]
```

Renders the server configuration from persistent configuration, transport
address family, IPAM layout, PKI paths, and the compatible template family.
`auto` infers `ipv4` or `ipv6` from IP literals. For hostnames it renders an
IPv6 dual-stack server socket (`udp6` or `tcp6-server` without
`bind ipv6only`); client profiles preserve family-neutral `udp`/`tcp` and resolve
A/AAAA records when connecting. The socket accepts both native IPv6 and
IPv4-mapped peers. Explicit `ipv6` adds `bind ipv6only`; explicit `ipv4` needs
no matching bind option because its IPv4 socket cannot accept IPv6. With no
output option it
atomically updates `server/server.conf`; `--stdout` writes the result to
standard output; `--output <path>` writes a mode-`0600` file at that path.

### `ovpn render client`

Syntax:

```text
ovpn render client <client> [--stdout|--output <path>]
```

Builds a client `.ovpn` profile from the configured endpoint, CA certificate,
selected client certificate and key, and tls-crypt key. `<client>` may be the
current name or UUID. Output defaults to standard output; `--output` writes an
atomically replaced mode-`0600` file.

## Persistent data migration

### `ovpn migrate`

Syntax:

```text
ovpn migrate plan [--json]
ovpn migrate apply [--yes]
```

Available only through the stopped `openvpn-maintenance` service. `plan` is
read-only and reports source/target schemas, the ordered migration chain,
client count, blockers, credential impact, and profile redistribution needs.
`apply` requires confirmation, or `--yes` outside a TTY. An already-current
schema is an idempotent success.

Migration always uses code embedded in the current maintenance image and does
not access the network. Apply obtains the exclusive runtime lock, snapshots
persistent data, migrates only a staging copy, validates schema, PKI,
registries, profiles, and configuration, then atomically commits the data.
Failure or interruption restores the original data.
Schema 1 runs `1→2→3`; schema 2 runs `2→3`. Both replace name-CN client
credentials with UUID-CN credentials, so every active profile reported after
success must be redistributed.

Data-dependent commands reject old, conflicting, invalid, or newer schemas
with exit status `78`; only `migrate` may read historical formats. Help,
version, and capability inspection remain available without parsing instance
data.
Snapshots and reports are retained below `repair/migrations`. Restoring an old
image after migration requires restoring its matching pre-migration snapshot;
an image rollback alone is not a data rollback.

## Runtime inspection

### `ovpn runtime status`

Syntax:

```text
ovpn runtime status
```

Prints runtime-state JSON. If the runtime-state file is unavailable, it returns
a synthesized object with the detected instance state, `daemon` set to
`unknown`, and `maintenance` set to `false`.

### `ovpn runtime health`

Syntax:

```text
ovpn runtime health
```

Returns success only when runtime state reports a healthy, running,
non-maintenance service and both `/dev/net/tun` and an OpenVPN process are
present. On failure it writes a reason to standard error and returns nonzero.

### `ovpn runtime capabilities`

Syntax:

```text
ovpn runtime capabilities
```

Prints JSON for the detected OpenVPN version, exact-version support result,
verified version list, selected compatibility adapter, and required runtime
features. It returns nonzero when the version, adapter, or any required feature
is unsupported.

### `ovpn runtime version`

Syntax:

```text
ovpn runtime version
```

Prints build-information JSON from
`/usr/local/share/openvpn-container/build-info.json`, with the Easy-RSA version
detected at runtime. It reports image version, data schema, OpenVPN, Easy-RSA,
runtime/build provenance, and the OpenVPN candidate range used by automation.
The candidate range is not a runtime compatibility claim. If build information
is missing, it detects Easy-RSA and prints `unknown` for unavailable fields.

### `ovpn runtime logs`

Syntax:

```text
ovpn runtime logs [--lines N] [--follow] [--raw]
```

Reads persistent rotated OpenVPN logs, defaulting to the latest 100 lines.
Known UUIDs are displayed as `name [uuid]`; unknown identities remain
unchanged. `--raw` disables translation. `--follow` continues across append,
rotation, and atomic replacement without owning or blocking the OpenVPN
management socket.

### `ovpn runtime events`

Syntax:

```text
ovpn runtime events [--lines N] [--follow] [--json]
```

Reads the latest 100 structured connection, disconnection, client lifecycle,
IP, rename, network migration, and data migration events.
The default is human-readable text; `--json` emits one JSON object per event.
`--follow` streams new records without blocking management commands.

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
