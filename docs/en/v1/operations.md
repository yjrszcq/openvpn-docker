# OpenVPN v1 Operations Guide

A workflow-oriented guide for operators on release commit
`6619921e5257e604f5df2c63d2fa10505b680d84`. For complete command syntax and
options, see the [v1 command reference](commands.md).

## Runtime conventions

- **openvpn container** — the live server. Run day-to-day client operations,
  config changes, and runtime inspection here.
- **openvpn-maintenance container** — a low-privilege one-shot container
  without TUN or NET_ADMIN. Run diagnostics and repairs here.

```bash
# live container
docker compose exec openvpn ovpn <command>

# maintenance container (one-shot)
docker compose run --rm openvpn-maintenance <command>
```

`ovpn` is the maintenance container's entrypoint, so `<command>` is the part that follows
`ovpn` — for example, `state doctor` runs `ovpn state doctor`.

If your compose file does not include the maintenance service, add it under
`services:`:

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    volumes:
      - ./openvpn-data:/etc/openvpn
    profiles:
      - maintenance
    command:
      - doctor
    entrypoint:
      - /usr/local/bin/ovpn
```

It mounts the same persistent data but does not request TUN, `NET_ADMIN`, or
exposed ports.

---

## Day-to-day operations

### Create and distribute clients

```bash
# create a client certificate and profile
docker compose exec openvpn ovpn add-client laptop

# save the profile locally
docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn
```

`add-client` creates the certificate, private key, and active profile in one
step. `export-client` refreshes the active profile atomically and writes it to
standard output.

### View client status

```bash
docker compose exec openvpn ovpn list-clients
```

Prints one `name state` line per client from the Easy-RSA index. State is
`active` for valid certificates and `revoked` for revoked certificates.

### Revoke a client

```bash
docker compose exec openvpn ovpn revoke-client laptop
```

Revokes the certificate, regenerates the CRL, and moves the profile from
`clients/active/` to `clients/revoked/`. Private key and certificate material
are preserved.

---

## Configuration changes

### Changing endpoint, protocol, or port

Example: switch from UDP to TCP.

1. Update Compose environment:

   ```yaml
   environment:
     OVPN_PROTO: tcp
     OVPN_PORT: "1194"
   ```

2. Open the corresponding firewall port, then apply:

   ```bash
   docker compose config --quiet
   docker compose down          # do not use -v
   docker compose run --rm openvpn ovpn config init
   docker compose up -d openvpn
   docker compose exec openvpn ovpn config print
   ```

3. Re-export and distribute profiles for all active clients. Use a temp file to
   avoid overwriting the local copy on export failure:

   ```bash
   docker compose exec -T openvpn ovpn export-client laptop > laptop.ovpn.tmp &&
   mv laptop.ovpn.tmp laptop.ovpn
   ```

> **Note**: `config init` only rewrites persistent configuration. It does not
> modify client certificates, keys, or profiles. Do not run `add-client` again
> for an existing name — re-export the profile instead.

### View current configuration

```bash
docker compose exec openvpn ovpn config print
```

---

## Diagnostics and repair

### Inspect instance state

```bash
# live container
docker compose exec openvpn ovpn state

# maintenance container
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance doctor --json
```

`state` prints a single state name. `doctor` lists detected issues with
severities and recommended actions; `--json` emits a `state` and `issues`
object.

State values: `EMPTY`, `HEALTHY`, `DEGRADED_REPAIRABLE`,
`DEGRADED_RECOVERABLE`, `DEGRADED_REISSUABLE`, `CRITICAL`, `UNRECOVERABLE`.

### Repair a degraded instance

```bash
# preview repair plan (read-only)
docker compose run --rm openvpn-maintenance repair --plan

# preview with JSON output
docker compose run --rm openvpn-maintenance repair --plan --json

# apply repairs
docker compose run --rm openvpn-maintenance repair
```

`repair --plan` lists proposed `SAFE` and `RECOVER` actions without modifying
any files. `repair` stages, validates, snapshots, and atomically applies allowed
repairs only when the state is `HEALTHY`, `DEGRADED_REPAIRABLE`, or
`DEGRADED_RECOVERABLE`. `CRITICAL` and `UNRECOVERABLE` states refuse repair by
default (exit code 78); set `OVPN_CRITICAL_MODE=maintenance` only to preserve a
broken container for inspection.

### Runtime inspection

```bash
docker compose exec openvpn ovpn status        # runtime state JSON
docker compose exec openvpn ovpn healthcheck    # container health check
docker compose exec openvpn ovpn capabilities   # compatibility information
docker compose exec openvpn ovpn version        # build information
```

---

## Backup and restore

### Backup

`./openvpn-data` stores CA, server, and client private keys, profiles, tls-crypt
material, and instance metadata. Stop the service before archiving for a
consistent snapshot:

```bash
docker compose stop openvpn
tar --numeric-owner -C . -czf openvpn-backup-YYYYMMDD.tar.gz openvpn-data
docker compose up -d openvpn
```

Encrypt backup archives and restrict access. Never copy private keys into
tickets, logs, or temporary recovery notes.

### Restore

Place the data directory at its original mount path, then inspect state before
starting:

```bash
docker compose run --rm openvpn-maintenance doctor
docker compose run --rm openvpn-maintenance repair --plan
docker compose up -d openvpn
```

> **Warning**: Do not point the bind mount at a fresh empty directory — an empty
> directory is intentionally treated as a request to create a new instance.
