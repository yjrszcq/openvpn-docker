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

The maintenance service uses `ovpn` as its entrypoint, so `<command>` starts
with `config`, `state`, `repair`, or `migrate`, not with another `ovpn`.

Default paths:

| Purpose | Path | Override |
|---|---|---|
| Desired YAML | `/etc/openvpn-config/config.yaml` | `OVPN_CONFIG_FILE` |
| Persistent data | `/etc/openvpn` | `OVPN_DATA_DIR` |
| Runtime sockets and locks | `/run/openvpn-container` | `OVPN_RUNTIME_DIR` |
| SQLite authority | `/etc/openvpn/meta/state.db` | derived from data dir |

`OVPN_MAINTENANCE=true` authorizes offline migration. `OVPN_EDITOR`, then
`EDITOR`, selects the batch address editor. Binary/template overrides are test
and development interfaces, not normal deployment configuration.

## Output and exit codes

Query and plan commands default to stable human-readable output and support
`--json` where listed. JSON errors are written as structured objects to
standard error. `runtime events --json` emits one JSON object per line.
`runtime logs --raw` preserves original OpenVPN log text.

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | runtime or operation failure |
| `64` | CLI usage error |
| `65` | invalid input or configuration data |
| `69` | required external dependency unavailable |
| `75` | lock, busy state, or temporary resource conflict |
| `78` | schema, state, confirmation, or security policy refusal |

`migrate apply`, `config apply`, `repair apply`, `client delete`, and batch
address editing require interactive confirmation or `--yes`. A non-TTY call
without `--yes` is refused.

## Command tree

```text
ovpn
├── server
│   ├── init
│   ├── run
│   └── render [--output FILE|-]
├── config
│   ├── validate [--json]
│   ├── show [--json]
│   ├── export [--output FILE|-]
│   ├── plan [--json]
│   └── apply [--yes] [--json]
├── client
│   ├── create NAME [--ipv4 auto|dynamic|ADDRESS]
│   ├── list [--detail] [--full-id] [--json]
│   ├── export (--name NAME|--id ID) [--output FILE|-]
│   ├── rename (--name NAME|--id ID) NEW_NAME
│   ├── revoke (--name NAME|--id ID) [--release-ipv4]
│   ├── reissue (--name NAME|--id ID) [--ipv4 auto|dynamic|ADDRESS]
│   ├── delete (--name NAME|--id ID) [--yes]
│   └── address
│       ├── set (--name NAME|--id ID) --ipv4 auto|dynamic|ADDRESS
│       ├── edit (--all|--name NAME...|--id ID...) [--yes]
│       └── release (--name NAME|--id ID)
├── state
│   ├── show [--json]
│   └── doctor [--json]
├── repair
│   ├── plan [--json]
│   └── apply [--yes] [--json]
├── migrate
│   ├── plan [--json]
│   └── apply [--yes] [--json]
├── runtime
│   ├── status [--json]
│   ├── health
│   ├── capabilities [--json]
│   ├── logs [--lines N] [--follow] [--raw] [--full-id]
│   └── events [--lines N] [--follow] [--json] [--full-id]
└── version [--short|--json]
```

All command groups and leaf commands accept `--help` or `-h`.

## Server commands

### `ovpn server init`

Initializes an empty schema 4 data directory from valid YAML. It stages and
validates SQLite, PKI, server credentials, CRL, tls-crypt, configuration, and
derived files before installation. A non-empty or unsupported legacy directory
is refused.

The image entrypoint calls initialization automatically only when the mounted
data directory is empty. Explicit `server run` does not initialize missing
state.

### `ovpn server run`

Loads the applied SQLite snapshot, recovers interrupted operations, reconciles
IPv4 forwarding/firewall state, starts the Go broker and OpenVPN, supervises
both processes, and forwards TERM, INT, and HUP.

Missing or changed YAML causes a warning; runtime continues with the last
applied database revision. Configuration is never applied at startup.

### `ovpn server render [--output FILE|-]`

Renders the server configuration from applied SQLite state. The default output
is standard output. `--output -` is explicit standard output; any file target
must satisfy the command's safe output rules.

## Configuration commands

YAML version 1 requires `server.endpoint` and `ipv4.network`. Parsing is strict:
unknown or duplicate fields, null values, multiple documents, incorrect types,
noncanonical networks, unsupported values, and IPv6 tunnel state are rejected.

### `ovpn config validate [--json]`

Validates and normalizes desired YAML only. It does not open SQLite and does
not compare or change applied state.

### `ovpn config show [--json]`

Displays the normalized applied configuration, revision, and SHA-256 digest
from SQLite. It does not read desired YAML.

### `ovpn config export [--output FILE|-]`

Writes a complete YAML v1 document from the applied SQLite snapshot. This is
required after schema 3 migration if the instance did not already have v4
YAML.

### `ovpn config plan [--json]`

Compares desired YAML with the applied revision. The plan lists field changes
and whether they require restart, address remapping, firewall reconciliation,
derived artifact regeneration, or client-profile redistribution.

### `ovpn config apply [--yes] [--json]`

Requires OpenVPN to be stopped and takes the exclusive runtime lock. It applies
ordinary configuration and IPv4 network/pool changes in one staged operation,
updates SQLite, remaps addresses when required, regenerates derived files, and
reports restart and redistribution requirements. It never performs an online
reload.

## Client selection and identity

Each client has an immutable UUID and a current display name. Commands that
operate on an existing client require exactly one selector:

```text
--name NAME
--id ID
```

Names are exact and case-sensitive. IDs accept a standard UUID or an
unambiguous hexadecimal UUID prefix of at least eight characters. Active and
revoked names are unique. Deleted clients retain UUID tombstones while their
old names may be reused by a new UUID.

IPv4 intent is expressed uniformly as:

- `auto`: allocate the lowest available static address.
- `dynamic`: use the configured dynamic pool.
- `ADDRESS`: use the specified free static IPv4 address.

### `ovpn client create NAME [--ipv4 ...]`

Creates the UUID, Easy-RSA certificate and key, profile, address assignment,
artifact metadata, operation record, and audit event. The default IPv4 intent
is `auto`.

### `ovpn client list [--detail] [--full-id] [--json]`

Lists current clients. `--detail` includes assignment and lease information.
The default text ID is shortened; `--full-id` prints the full UUID. JSON uses a
stable object representation.

### `ovpn client export SELECTOR [--output FILE|-]`

Exports an active client's current profile. The default output is standard
output. Treat the result as a private credential.

### `ovpn client rename SELECTOR NEW_NAME`

Changes the display name and profile filename without changing the UUID,
certificate identity, address assignment, or audit history.

### `ovpn client revoke SELECTOR [--release-ipv4]`

Revokes the certificate, regenerates the CRL, marks the profile revoked, and
reports that a current runtime session must be disconnected. Static IPv4 is
retained unless `--release-ipv4` is supplied.

### `ovpn client reissue SELECTOR [--ipv4 ...]`

Issues a new key/certificate for the same UUID, updates CRL/profile state, and
optionally changes the address intent. Export and redistribute the new profile;
the prior session must be disconnected.

### `ovpn client delete SELECTOR [--yes]`

Revokes an active certificate when needed, removes local credentials and
assignment state, and retains the UUID tombstone. Deleted private keys are
recoverable only from a secure backup.

### `ovpn client address set SELECTOR --ipv4 ...`

Changes one active client's assignment atomically and updates the CCD artifact.
Disconnect the current session so the new assignment takes effect.

### `ovpn client address edit TARGETS [--yes]`

Selects all active clients or repeated names/IDs and opens a private CSV file:

```text
# client,ipv4
laptop,auto
phone,dynamic
tablet,10.42.0.20
```

Every selected client must appear exactly once. Values are `auto`, `dynamic`,
or a static IPv4 address. `--name` and `--id` cannot be mixed. The complete set
is validated and committed atomically, so address swaps are supported.

### `ovpn client address release SELECTOR`

Releases the retained static assignment of a revoked client. It does not delete
the client, profile history, certificate evidence, or tombstone.

## State and repair

### `ovpn state show [--json]`

Prints the aggregate state: `HEALTHY`, `DEGRADED_REPAIRABLE`,
`DEGRADED_RECOVERABLE`, `DEGRADED_REISSUABLE`, `CRITICAL`, or `UNRECOVERABLE`.
An empty directory is reported separately during initialization workflows.

### `ovpn state doctor [--json]`

Scans SQLite integrity and constraints, applied configuration, PKI, artifact
metadata, certificates, keys, CRL, profiles, CCD, and interrupted operations.
It reports issue IDs, evidence, severity, and recommended actions. It does not
guess or recreate a missing/corrupt authoritative database.

### `ovpn repair plan [--json]`

Builds a read-only plan from the state report. Safe derived-file or evidence-
backed recovery actions are listed separately from blockers and deferred work.

### `ovpn repair apply [--yes] [--json]`

Applies eligible actions under the exclusive lock with staging, operation
journaling, validation, and rollback. Missing/corrupt SQLite authority or
conflicting security evidence remains a backup-restore condition.

## Migration commands

### `ovpn migrate plan [--json]`

Reads schema 3 state without mutation and reports imported clients,
assignments, leases, audit events, artifacts, normalization repairs, profile
impact, snapshot paths, YAML export, and rollback instructions. Schema 1/2 is
refused with an instruction to upgrade through `sh-ver`; newer, unknown, or
corrupt sources are also refused.

### `ovpn migrate apply [--yes] [--json]`

Requires `OVPN_MAINTENANCE=true`, a stopped server, confirmation, and the
exclusive lock. It creates and hashes a complete schema 3 snapshot, builds
schema 4 in staging, validates `state doctor`, and installs atomically.
Interrupted transactions are completed or rolled back deterministically.

After success, export YAML. Rollback requires restoring the complete verified
snapshot and then running the `sh-ver` image.

## Runtime commands

### `ovpn runtime status [--json]`

Queries the management broker for daemon state, management state, connected
clients, virtual addresses, and remote addresses. It requires a running server.

### `ovpn runtime health`

Returns success and prints `healthy` only when the broker and OpenVPN runtime
are healthy. This is the image healthcheck command.

### `ovpn runtime capabilities [--json]`

Reports the strict compatibility contract and probed OpenVPN features.

### `ovpn runtime logs [options]`

Reads persistent OpenVPN logs. `--lines` defaults to 100, `--follow` follows
rotation, `--raw` disables UUID-to-name translation, and `--full-id` prevents
UUID shortening in translated output.

### `ovpn runtime events [options]`

Reads `events.jsonl`. Text output is human-readable. `--json` preserves JSONL,
`--follow` follows appended events, and `--full-id` prevents UUID shortening.

## Version command

### `ovpn version [--short|--json]`

Prints project image/runtime version, data schema, Go version, VCS revision,
build date, and pinned Go module versions. `--short` prints only the project
version; `--json` emits the stable version object.
