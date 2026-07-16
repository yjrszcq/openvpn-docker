# OpenVPN Operations Guide

A workflow-oriented guide for operators. For complete command syntax and options,
see the [v2 command reference](commands.md).

## Runtime conventions

- **openvpn container** — the live server with a management socket. Run
  day-to-day client operations, config changes, and network migrations here.
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
# create with default static IP
docker compose exec openvpn ovpn client create laptop

# create with dynamic IP
docker compose exec openvpn ovpn client create phone --dynamic

# create with a specific static IP
docker compose exec openvpn ovpn client create tablet --ip 10.42.0.10

# export profile
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn
```

### View client status

```bash
# compact view (name + state)
docker compose exec openvpn ovpn client list

# detailed view (six-column table with IP and connection state)
docker compose exec openvpn ovpn client list --detail
```

### Revoke and release IPs

```bash
# revoke certificate, retain IP reservation
docker compose exec openvpn ovpn client revoke laptop

# revoke and release static IP in one step
docker compose exec openvpn ovpn client revoke laptop --release-ip

# release static IP after revocation
docker compose exec openvpn ovpn client ip release laptop
```

When a revoked client retains its IP, the assignment shows `retained`. After
release the IP returns to the pool; the revoked profile, private key, and audit
history are kept.

### Reissue certificates

```bash
docker compose exec openvpn ovpn client reissue laptop
docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn
```

Generates a new key and certificate while preserving the IP assignment.
Re-export and distribute the profile afterward.

### Delete a client

```bash
docker compose exec openvpn ovpn client delete laptop
```

Irreversible. Active clients are revoked first, then the registry record,
profile, and private key are removed.

---

## IP address management

### Single-client operations

```bash
# assign static (auto-allocate lowest free address)
docker compose exec openvpn ovpn client ip set phone

# assign static (specific address)
docker compose exec openvpn ovpn client ip set phone --ip 10.42.0.20

# assign dynamic
docker compose exec openvpn ovpn client ip set phone --dynamic
```

### Batch operations

For multiple names or `--all`, the command opens an editor with a `client,ip`
manifest:

```bash
# named multi-client edit
docker compose exec openvpn ovpn client ip set phone tablet laptop

# all active clients
docker compose exec openvpn ovpn client ip set --all
```

Each editor line is `client,ip` with three assignment modes:

```text
phone,auto               # auto-allocate lowest free static address
tablet,10.42.0.20        # explicit static address
laptop,                  # leave empty to keep dynamic
```

Editor selection order: `OVPN_EDITOR` > `EDITOR` > `nano`. The image ships
`nano` and `vim`.

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
   docker compose down          # do not use -v
   docker compose run --rm openvpn ovpn config apply
   docker compose up -d openvpn
   ```

3. Re-export and distribute profiles for all active clients. Use a temp file to
   avoid overwriting the local copy on export failure:

   ```bash
   docker compose exec -T openvpn ovpn client export laptop > laptop.ovpn.tmp &&
   mv laptop.ovpn.tmp laptop.ovpn
   ```

> **Note**: `config apply` only rewrites persistent configuration. It does not
> modify client certificates, keys, or IP assignments. Do not use it to change
> `OVPN_NETWORK` or `OVPN_DYNAMIC_POOL_SIZE` — use the network migration
> commands instead.

### View current configuration

```bash
docker compose exec openvpn ovpn config show
```

---

## Network migration

Changing the tunnel subnet or dynamic pool size requires migration commands,
run **in the live container** (needs the management socket):

```bash
# preview migration plan (read-only)
docker compose exec openvpn ovpn network plan \
  --network 10.43.0.0/24 --dynamic-pool-size 96

# apply migration
docker compose exec openvpn ovpn network apply \
  --network 10.43.0.0/24 --dynamic-pool-size 96 --yes
```

Migration flow: snapshot current state → update config and registry → SIGHUP
reload OpenVPN → verify management socket and container health. On failure it
automatically restores snapshots and reloads the old config. Affected clients
must reconnect.

---

## Diagnostics and repair

### Inspect instance state

```bash
# live container
docker compose exec openvpn ovpn state show

# maintenance container
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance state doctor --json
```

State values: `EMPTY`, `HEALTHY`, `DEGRADED_REPAIRABLE`,
`DEGRADED_RECOVERABLE`, `CRITICAL`, `UNRECOVERABLE`.

### Repair a degraded instance

```bash
# preview eligible repairs (read-only)
docker compose run --rm openvpn-maintenance repair plan

# apply repairs
docker compose run --rm openvpn-maintenance repair apply
```

`repair plan` lists `SAFE` actions (rebuild derived files) and `RECOVER` actions
(restore certificates/keys from backup paths). `repair apply` stages, snapshots,
and atomically applies allowed repairs. `CRITICAL` states refuse repair by
default (exit code 78); set `OVPN_CRITICAL_MODE=maintenance` only to preserve
a broken container for inspection.

### Runtime inspection

```bash
docker compose exec openvpn ovpn runtime status       # runtime state JSON
docker compose exec openvpn ovpn runtime health       # container health check
docker compose exec openvpn ovpn runtime capabilities # compatibility info
docker compose exec openvpn ovpn runtime version      # build information
```

---

## Backup and restore

### Backup

`./openvpn-data` stores CA, server, and client private keys, profiles, tls-crypt
material, instance metadata, and dynamic lease state. Back it up:

```bash
docker compose stop openvpn
tar --numeric-owner -C . -czf openvpn-backup-YYYYMMDD.tar.gz \
  openvpn-data
docker compose up -d openvpn
```

Encrypt backup archives and restrict access. Never copy private keys into
tickets, logs, or temporary recovery notes.

### Restore

Place the data directory at its original mount path, then inspect state before
starting:

```bash
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
docker compose up -d openvpn
```

> **Warning**: Do not point the bind mount at a fresh empty directory — an empty
> directory is intentionally treated as a request to create a new instance.
