# OpenVPN CLI v4 Reference

This is the command contract for the Go and SQLite schema 4 control plane.

## Invocation and paths

Run live commands through the service container:

```bash
docker compose exec openvpn ovpn <command>
```

Run offline maintenance commands through the one-shot service:

```bash
docker compose run --rm openvpn-maintenance <command>
```

The maintenance service uses `ovpn` as its entrypoint, so `<command>` starts with `config`, `state`, `repair`, or `migrate`, not with another `ovpn`.

Default paths:

| Purpose | Path | Override |
|---|---|---|
| Desired YAML | `/etc/openvpn-config/config.yaml` | `OVPN_CONFIG_FILE` |
| Persistent data | `/etc/openvpn` | `OVPN_DATA_DIR` |
| Coordination locks | `/etc/openvpn` | `OVPN_DATA_DIR` |
| Runtime sockets | `/run/openvpn-container` | `OVPN_RUNTIME_DIR` |
| SQLite authority | `/etc/openvpn/meta/state.db` | derived from data dir |

`OVPN_MAINTENANCE=true` authorizes offline migration. `OVPN_EDITOR`, then `EDITOR`, selects the batch address editor. Binary/template overrides are test and development interfaces, not normal deployment configuration.

`ovpn client`, `ovpn state`, and `ovpn runtime` are safe shortcuts for `client list`, `state doctor`, and `runtime status`. At the top level, `-v` prints only the project version while `-V` and `--version` print the full version report. Running `ovpn` without arguments prints a compact command tree with every leaf usage; `ovpn -h` retains the detailed root help.

## One-time environment bootstrap

A new empty instance can generate its first YAML file from Compose environment variables. This is an initialization input, not a second configuration mode. Set `OVPN_BOOTSTRAP_FROM_ENV=true` and provide the two required values:

| Environment variable | YAML field | Required |
|---|---|---|
| `OVPN_BOOTSTRAP_ENDPOINT` | `server.endpoint` | yes |
| `OVPN_BOOTSTRAP_IPV4_NETWORK` | `ipv4.network` | yes |
| `OVPN_BOOTSTRAP_PROTOCOL` | `server.transport.protocol` | no |
| `OVPN_BOOTSTRAP_FAMILY` | `server.transport.family` | no |
| `OVPN_BOOTSTRAP_PORT` | `server.transport.port` | no |
| `OVPN_BOOTSTRAP_CLIENT_TO_CLIENT` | `server.clientToClient` | no |
| `OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE` | `ipv4.dynamicPoolSize` | no |
| `OVPN_BOOTSTRAP_NAT_ENABLED` | `ipv4.nat.enabled` | no |
| `OVPN_BOOTSTRAP_NAT_INTERFACE` | `ipv4.nat.interface` | no |
| `OVPN_BOOTSTRAP_REDIRECT_GATEWAY` | `ipv4.redirectGateway` | no |
| `OVPN_BOOTSTRAP_DNS` | `ipv4.dns`, comma-separated | no |
| `OVPN_BOOTSTRAP_ROUTES` | `ipv4.routes`, comma-separated | no |
| `OVPN_BOOTSTRAP_LOG_MAX_BYTES` | `logging.maxBytes` | no |
| `OVPN_BOOTSTRAP_LOG_BACKUPS` | `logging.backups` | no |

The normal YAML defaults and validation are applied, then a canonical mode `0600` file is installed at `OVPN_CONFIG_FILE`. An existing YAML file is accepted only when it normalizes to exactly the same configuration, allowing a failed initialization to be retried safely. A conflicting file is refused.

After schema 4 exists, bootstrap variables are ignored with a warning. All later changes must use YAML with `config validate`, `config plan`, and offline `config apply`. Remove the bootstrap flag, or set it to `false`, after the first successful initialization.

## Output and exit codes

Query and plan commands default to stable human-readable output and support `--json` where listed. JSON errors are written as structured objects to standard error. `runtime events --json` emits one JSON object per line. `runtime logs --raw` preserves original OpenVPN log text.

Human client output uses 12 hexadecimal UUID characters by default; `--full-id/-u` prints the canonical UUID. JSON always contains full UUIDs. All client mutations return versioned JSON with the operation ID, client state, profile redistribution impact, and post-commit runtime convergence state.

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | runtime or operation failure |
| `64` | CLI usage error |
| `65` | invalid input or configuration data |
| `69` | required external dependency unavailable |
| `75` | lock, busy state, or temporary resource conflict |
| `78` | schema, state, confirmation, or security policy refusal |

`migrate apply`, `config apply`, `repair apply`, `client delete`, and batch address editing require interactive confirmation or `--yes`. A non-TTY call without `--yes` is refused.

Mutations perform read-only validation, target selection, and planning before asking for confirmation. Invalid requests never prompt, and a no-op plan exits successfully without requiring `--yes`. Human errors include an actionable hint where one is safe; JSON errors retain their stable object form.

Every public multi-letter option has a single-token short alias. Long and short forms are equivalent; specifying both repeats the same logical option and is rejected. Short options cannot be clustered or joined to their values. `-6` is reserved for future IPv6 behavior.

| Long option | Short |
|---|---|
| `--help` | `-h` |
| `--json` | `-j` |
| `--output` | `-o` |
| `--yes` | `-y` |
| `--name` | `-n` |
| `--id` | `-i` |
| `--ipv4` | `-4` |
| `--release-ipv4` | `-4` within `client revoke` |
| `--all` | `-a` |
| `--detail` | `-d` |
| `--full-id` | `-u` |
| `--lines` | `-l` |
| `--follow` | `-f` |
| `--raw` | `-r` |
| `--short` | `-s` |

## Command tree

```text
ovpn
├── server
│   ├── init
│   ├── run
│   └── render [--output|-o FILE|-]
├── config
│   ├── validate [--json|-j]
│   ├── show [--json|-j]
│   ├── export [--output|-o FILE|-]
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── client
│   ├── create NAME [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]
│   ├── list [--detail|-d] [--full-id|-u] [--json|-j]
│   ├── export (NAME|--name|-n NAME|--id|-i ID) [--output|-o FILE|-]
│   ├── rename (NAME|--name|-n NAME|--id|-i ID) NEW_NAME [--full-id|-u] [--json|-j]
│   ├── revoke (NAME|--name|-n NAME|--id|-i ID) [--release-ipv4|-4] [--full-id|-u] [--json|-j]
│   ├── reissue (NAME|--name|-n NAME|--id|-i ID) [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]
│   ├── delete (NAME|--name|-n NAME|--id|-i ID) [--yes|-y] [--full-id|-u] [--json|-j]
│   └── address
│       ├── set (NAME|--name|-n NAME|--id|-i ID) --ipv4|-4 [auto|dynamic|ADDRESS] [--full-id|-u] [--json|-j]
│       ├── edit (--all|-a|NAME...|--name|-n NAME...|--id|-i ID...) [--yes|-y] [--json|-j]
│       └── release (NAME|--name|-n NAME|--id|-i ID) [--full-id|-u] [--json|-j]
├── state
│   ├── show [--json|-j]
│   └── doctor [--json|-j]
├── repair
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── migrate
│   ├── plan [--json|-j]
│   └── apply [--yes|-y] [--json|-j]
├── runtime
│   ├── status [--json|-j] [--full-id|-u]
│   ├── disconnect (NAME|--name|-n NAME|--id|-i ID) [--json|-j] [--full-id|-u]
│   ├── health
│   ├── capabilities [--json|-j]
│   ├── logs [--lines|-l N] [--follow|-f] [--raw|-r] [--full-id|-u]
│   └── events [--lines|-l N] [--follow|-f] [--json|-j] [--full-id|-u]
├── completion (bash|zsh|fish)
└── version [--short|-s|--json|-j]
```

All command groups and leaf commands accept `--help` or `-h`.

`ovpn-broker` is an internal standalone binary with its own aliases: `--help/-h`, `--version/-v`, `--listen/-l`, `--backend/-b`, `--raw-log/-r`, `--max-bytes/-m`, `--backups/-B`, and `--timeout/-t`.

## Server commands

### `ovpn server init`

Initializes an empty schema 4 data directory from valid YAML. It stages and validates SQLite, PKI, server credentials, CRL, tls-crypt, configuration, and derived files before installation. A non-empty or unsupported legacy directory is refused.

The image entrypoint calls initialization automatically only when the mounted data directory is empty. Explicit `server run` does not initialize missing state.

### `ovpn server run`

Loads the applied SQLite snapshot, recovers interrupted operations, reconciles IPv4 forwarding/firewall state, starts the Go broker and OpenVPN, supervises both processes, and forwards TERM, INT, and HUP.

Missing or changed YAML causes a warning; runtime continues with the last applied database revision. Configuration is never applied at startup.

### `ovpn server render [--output FILE|-]`

Renders the server configuration from applied SQLite state. The default output is standard output. `--output -` is explicit standard output; any file target must satisfy the command's safe output rules.

## Configuration commands

YAML version 1 requires `server.endpoint` and `ipv4.network`. Parsing is strict: unknown or duplicate fields, null values, multiple documents, incorrect types, noncanonical networks, unsupported values, and IPv6 tunnel state are rejected.

### `ovpn config validate [--json]`

Validates and normalizes desired YAML only. It does not open SQLite and does not compare or change applied state.

### `ovpn config show [--json]`

Displays the normalized applied configuration, revision, and SHA-256 digest from SQLite. It does not read desired YAML.

### `ovpn config export [--output FILE|-]`

Writes a complete YAML v1 document from the applied SQLite snapshot. This is required after schema 3 migration if the instance did not already have v4 YAML. A file output is created with mode `0600` and is never overwritten; use stdout plus a temporary host file when replacing an existing YAML document.

### `ovpn config plan [--json]`

Compares desired YAML with the applied revision. The plan lists field changes and whether they require restart, address remapping, firewall reconciliation, derived artifact regeneration, or client-profile redistribution.

### `ovpn config apply [--yes] [--json]`

Requires OpenVPN to be stopped and takes the exclusive runtime lock. It applies ordinary configuration and IPv4 network/pool changes in one staged operation, updates SQLite, remaps addresses when required, regenerates derived files, and reports restart and redistribution requirements. It never performs an online reload.

## Client selection and identity

Each client has an immutable UUID and a current display name. Commands that operate on an existing client accept exactly one selector form:

```text
NAME
--name NAME
--id ID
```

When neither `--name` nor `--id` is present, the positional value is treated as a name. Names are exact and case-sensitive. IDs accept a standard UUID or an unambiguous hexadecimal UUID prefix of at least eight characters. Active and revoked names are unique. Deleted clients retain UUID tombstones while their old names may be reused by a new UUID.

IPv4 intent is expressed uniformly as:

- `auto`: allocate the lowest available static address.
- `dynamic`: use the configured dynamic pool.
- `ADDRESS`: use the specified free static IPv4 address.

When `--ipv4` is present without a value, it means `auto`. Omitting the option entirely keeps each command's documented default behavior.

### `ovpn client create NAME [--ipv4 ...] [--output FILE|-] [--json]`

Creates the UUID, Easy-RSA certificate and key, profile, address assignment, artifact metadata, operation record, and audit event. The default IPv4 intent is `auto`. `--output` exports the committed profile in the same command. File targets are mode `0600` and never overwritten; `--output -` writes only the profile to stdout and cannot be combined with JSON.

### `ovpn client list [--detail] [--full-id] [--json]`

Lists current clients. `--detail` includes assignment and lease information. The default text ID is shortened; `--full-id` prints the full UUID. JSON uses a stable object representation.

### `ovpn client export SELECTOR [--output FILE|-]`

Exports an active client's current profile. The default output is standard output. Treat the result as a private credential.

### `ovpn client rename SELECTOR NEW_NAME [--json]`

Changes the display name and profile filename without changing the UUID, certificate identity, address assignment, or audit history.

### `ovpn client revoke SELECTOR [--release-ipv4] [--json]`

Revokes the certificate, regenerates the CRL, and marks the profile revoked. Static IPv4 is retained unless `--release-ipv4` is supplied. After the durable commit, the command tries to disconnect any current session. Runtime failure is reported as a warning/pending result and does not roll back the revocation.

### `ovpn client reissue SELECTOR [--ipv4 ...] [--output FILE|-] [--json]`

Issues a new key/certificate for the same UUID, updates CRL/profile state, and optionally changes the address intent. Omitting `--ipv4` retains the current assignment intent; specifying it without a value changes it to `auto`. The old session is disconnected after commit, and `--output` can return the replacement profile directly. The replacement profile must be redistributed.

### `ovpn client delete SELECTOR [--yes]`

Revokes an active certificate when needed, removes local credentials and assignment state, and retains the UUID tombstone. It also attempts to remove a stale session for active or previously revoked clients. Deleted private keys are recoverable only from a secure backup.

### `ovpn client address set SELECTOR --ipv4 ...`

Changes one active client's assignment atomically and updates the CCD artifact. `--ipv4` without a value selects `auto`. The command disconnects the current session after commit so the new assignment can take effect.

### `ovpn client address edit TARGETS [--yes]`

Selects all active clients or repeated names/IDs and opens a private CSV file:

```text
# client,ipv4
laptop,auto
phone,dynamic
tablet,10.42.0.20
```

Every selected client must appear exactly once. Values are `auto`, `dynamic`, or a static IPv4 address. Positional names, `--name`, `--id`, and `--all` cannot be mixed. The complete set is validated and committed atomically, so address swaps are supported. The editor is selected from `OVPN_EDITOR`, then `EDITOR`, then installed `nano`; selected live sessions are disconnected after commit.

### `ovpn client address release SELECTOR`

Releases the retained static assignment of a revoked client. It does not delete the client, profile history, certificate evidence, or tombstone.

## State and repair

### `ovpn state show [--json]`

Prints the aggregate state: `HEALTHY`, `DEGRADED_REPAIRABLE`, `DEGRADED_RECOVERABLE`, `DEGRADED_REISSUABLE`, `CRITICAL`, or `UNRECOVERABLE`. An empty directory is reported separately during initialization workflows.

### `ovpn state doctor [--json]`

Scans SQLite integrity and constraints, applied configuration, PKI, artifact metadata, certificates, keys, CRL, profiles, CCD, and interrupted operations. It reports issue IDs, evidence, severity, and recommended actions. It does not guess or recreate a missing/corrupt authoritative database.

### `ovpn repair plan [--json]`

Builds a read-only plan from the state report. Safe derived-file or evidence- backed recovery actions are listed separately from blockers and deferred work.

### `ovpn repair apply [--yes] [--json]`

Applies eligible actions under the exclusive lock with staging, operation journaling, validation, and rollback. Missing/corrupt SQLite authority or conflicting security evidence remains a backup-restore condition.

## Migration commands

### `ovpn migrate plan [--json]`

Reads schema 3 state without mutation and reports imported clients, assignments, leases, audit events, artifacts, normalization repairs, profile impact, snapshot paths, YAML export, and rollback instructions. Schema 1/2 is refused with an instruction to upgrade through `sh-ver`; newer, unknown, or corrupt sources are also refused.

### `ovpn migrate apply [--yes] [--json]`

Requires `OVPN_MAINTENANCE=true`, a stopped server, confirmation, and the exclusive lock. It creates and hashes a complete schema 3 snapshot, builds schema 4 in staging, validates `state doctor`, and installs atomically. Interrupted transactions are completed or rolled back deterministically.

After success, export YAML. Rollback requires restoring the complete verified snapshot and then running the `sh-ver` image.

## Runtime commands

### `ovpn runtime status [--json]`

Queries the management broker for daemon state, management state, connected clients, virtual addresses, and remote addresses. It requires a running server.

### `ovpn runtime disconnect SELECTOR [--json] [--full-id]`

Disconnects current sessions for the selected immutable client UUID through the management broker. An already-offline client is a successful no-op. Deleted tombstones can be selected by ID to retry cleanup after a prior runtime outage. This command does not revoke credentials or prevent reconnection.

### `ovpn runtime health`

Returns success and prints `healthy` only when the broker and OpenVPN runtime are healthy. This is the image healthcheck command.

### `ovpn runtime capabilities [--json]`

Reports the strict compatibility contract and probed OpenVPN features.

### `ovpn runtime logs [options]`

Reads persistent OpenVPN logs. `--lines` defaults to 100, `--follow` follows rotation, `--raw` disables UUID-to-name translation, and `--full-id` prevents UUID shortening in translated output.

### `ovpn runtime events [options]`

Reads `events.jsonl`. Text output is human-readable. `--json` preserves JSONL, `--follow` follows appended events, and `--full-id` prevents UUID shortening.

## Completion command

### `ovpn completion (bash|zsh|fish)`

Writes a shell completion script to stdout. Commands and options are generated from the same static command tree used by help. Explicit `--name/-n` and `--id/-i` selector values query the current client list at completion time; private artifacts are never read. Install examples:

The script completes a direct command named `ovpn`. Use it in a container shell or provide a host wrapper with that name for `docker compose exec openvpn ovpn`. To generate through Compose, replace `ovpn completion` below with `docker compose exec -T openvpn ovpn completion`. Dynamic selectors call the same direct command or wrapper.

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

## Version command

### `ovpn version [--short|--json]`

Prints project image/runtime version, data schema, Go version, VCS revision, build date, and pinned Go module versions. `--short` prints only the project version; `--json` emits the stable version object.
