# OpenVPN CLI v1 Reference

This is the command reference for release commit `6619921e5257e604f5df2c63d2fa10505b680d84`. It describes the command surface implemented by that revision.

## Scope and conventions

- Invoke commands through the image entry point as `ovpn <command>`. In Docker Compose, this is commonly `docker compose exec openvpn ovpn <command>`.
- Persistent instance data defaults to `/etc/openvpn`; `OVPN_DATA_DIR` changes that location. The runtime-state file defaults to `/run/openvpn-container/state.json`.
- A client name must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`.
- Commands that mutate persistent state use the data lock. Do not edit PKI or generated files concurrently with them.
- State-sensitive operations require a healthy instance unless their section says otherwise. `CRITICAL` and `UNRECOVERABLE` diagnostic outcomes exit with status `78`.

## Command tree

```text
ovpn help | -h | --help
ovpn version | --version
ovpn init
ovpn start
ovpn config [print|init]
ovpn render <server|client> ...
ovpn add-client <name>
ovpn export-client <name>
ovpn list-clients
ovpn revoke-client <name>
ovpn state
ovpn doctor [--json]
ovpn repair
ovpn repair --plan [--json]
ovpn status
ovpn healthcheck
ovpn capabilities
ovpn recover
```

## Help and build information

### `ovpn help`

Syntax:

```text
ovpn help
ovpn -h
ovpn --help
```

Prints the top-level command list. It does not inspect or modify instance data.

### `ovpn version`

Syntax:

```text
ovpn version
ovpn --version
```

Prints the build-information JSON from `/usr/local/share/openvpn-container/build-info.json`. If that file is missing, it prints an `unknown` value for image, runtime, Easy-RSA, and support-range fields.

## Instance lifecycle

### `ovpn init`

Syntax:

```text
ovpn init
```

Initializes an empty persistent data directory in one transaction. It creates the project configuration, PKI and CA, server identity, CRL, tls-crypt key, metadata, server configuration, client-profile directories, and repair directories. It refuses a non-empty directory.

Use it when provisioning data explicitly. `ovpn start` performs the same initialization automatically when the data directory is empty.

### `ovpn start`

Syntax:

```text
ovpn start
```

Container entry-point operation. It initializes an empty directory, scans the instance state, applies eligible automatic repairs for repairable or recoverable states, validates runtime compatibility, configures networking, renders the server configuration, records runtime state, and starts OpenVPN in the foreground.

It refuses unsafe states. With `OVPN_CRITICAL_MODE=maintenance`, a critical or unrecoverable state enters maintenance mode instead of immediately terminating the container.

## Persistent configuration

### `ovpn config`

Syntax:

```text
ovpn config
ovpn config print
ovpn config init
```

With no subcommand, `config` defaults to `print`.

`print` loads the persistent project configuration and prints one `KEY=VALUE` line for each supported setting: configuration version, endpoint, transport, port, tunnel network, NAT settings, gateway redirection, client-to-client traffic, DNS servers, and pushed routes.

`init` validates the current `OVPN_*` environment and atomically writes `config/project.env` and `config/schema-version` with mode `0600`. It does not issue, revoke, or remove client certificates. The accepted configuration keys are `OVPN_CONFIG_VERSION`, `OVPN_ENDPOINT`, `OVPN_PROTO`, `OVPN_PORT`, `OVPN_NETWORK`, `OVPN_NAT`, `OVPN_NAT_INTERFACE`, `OVPN_REDIRECT_GATEWAY`, `OVPN_CLIENT_TO_CLIENT`, `OVPN_DNS`, and `OVPN_ROUTES`.

## Rendering

### `ovpn render server`

Syntax:

```text
ovpn render server [--stdout|--output <path>]
```

Renders the server configuration from the persisted configuration, PKI paths, and compatible OpenVPN template family. With no output option it atomically updates `server/server.conf`; `--stdout` writes the result to standard output; `--output <path>` writes a mode-`0600` file at the supplied path.

### `ovpn render client`

Syntax:

```text
ovpn render client <name> [--stdout|--output <path>]
```

Builds a client `.ovpn` profile by embedding the CA certificate, named client certificate and key, and tls-crypt key. The endpoint must be configured and all required PKI files must exist. Output defaults to standard output; `--output` writes an atomically replaced mode-`0600` file.

## Client certificates and profiles

### `ovpn add-client`

Syntax:

```text
ovpn add-client <name>
```

Requires a healthy instance and a unique valid name. Issues a passwordless client certificate and private key, renders the profile to `clients/active/<name>.ovpn`, and reports the new client on standard error.

### `ovpn export-client`

Syntax:

```text
ovpn export-client <name>
```

Requires a healthy instance and an active client. It regenerates the active profile atomically, then writes the same profile to standard output. Redirect standard output to save the profile locally.

### `ovpn list-clients`

Syntax:

```text
ovpn list-clients
```

Requires a healthy instance. Prints one `name state` line per client from the Easy-RSA index. State is `active` for a valid certificate and `revoked` for a revoked certificate; the server identity is excluded.

### `ovpn revoke-client`

Syntax:

```text
ovpn revoke-client <name>
```

Requires a healthy active client. Revokes the certificate, regenerates the CRL, and moves its profile from `clients/active/` to `clients/revoked/` when present. It does not delete the private key or certificate material.

## State and repair

### `ovpn state`

Syntax:

```text
ovpn state
```

Scans persistent data and prints one state name: `EMPTY`, `HEALTHY`, `DEGRADED_REPAIRABLE`, `DEGRADED_RECOVERABLE`, `DEGRADED_REISSUABLE`, `CRITICAL`, or `UNRECOVERABLE`. It is read-only.

### `ovpn doctor`

Syntax:

```text
ovpn doctor [--json]
```

Runs the same state scan and lists detected issues with their severities and recommended actions. Without `--json`, output is a readable state-and-issue report. `--json` emits an object containing `state` and an `issues` array. Critical and unrecoverable states return exit status `78` after printing.

### `ovpn repair`

Syntax:

```text
ovpn repair
ovpn repair --plan [--json]
```

Without arguments, repair builds an automatic-repair plan and applies it under the data lock when the state is `HEALTHY`, `DEGRADED_REPAIRABLE`, or `DEGRADED_RECOVERABLE`. It stages changes, validates the staged instance, snapshots affected persistent files, installs the result atomically, and writes a repair journal. A failed transaction restores its snapshot.

`--plan` is read-only. It lists proposed `SAFE` and `RECOVER` actions, blocked issues, and the current state; `--json` changes that report to JSON. The planner can propose schema-version writes, metadata or configuration rendering, CRL regeneration, missing-profile rendering, recovery of verified certificate or key copies, and runtime-directory creation. It does not apply blocked or critical recovery actions.

### `ovpn recover`

Syntax:

```text
ovpn recover
```

The command name is recognized in this revision but is not implemented. It prints an error and exits with status `2`; it performs no recovery action.

## Runtime inspection

### `ovpn status`

Syntax:

```text
ovpn status
```

Prints runtime-state JSON. When the runtime-state file is unavailable, it returns a synthesized object with the detected instance state, `daemon` set to `unknown`, and `maintenance` set to `false`.

### `ovpn healthcheck`

Syntax:

```text
ovpn healthcheck
```

Returns success only when runtime state reports a healthy, running, non-maintenance service and both `/dev/net/tun` and an OpenVPN process are present. On failure it writes a reason to standard error and returns nonzero.

### `ovpn capabilities`

Syntax:

```text
ovpn capabilities
```

Prints JSON describing the detected OpenVPN version, whether it is inside the supported range, the selected compatibility adapter, and each required runtime feature. The command returns nonzero when the version, adapter, or any required feature is unsupported.

## Examples

```bash
# Inspect an existing instance.
docker compose run --rm openvpn-maintenance doctor

# Create a client and save its profile.
docker compose exec openvpn ovpn add-client laptop
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn

# Inspect automatic repair without changing persistent data.
docker compose run --rm openvpn-maintenance repair --plan
```
