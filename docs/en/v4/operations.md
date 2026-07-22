# OpenVPN Operations Guide

This workflow-oriented guide covers deployment, routine client administration, configuration, diagnosis, migration, and recovery. See the [command reference](commands.md) for complete syntax, options, and exit codes.

Persistent compatibility follows the [data schema upgrade policy](../data-schema-upgrade-policy.md). Image delivery and rollback follow the [image update policy](../image-update-policy.md).

## Runtime conventions

- `openvpn`: live service with `/dev/net/tun`, `NET_ADMIN`, the management broker, and OpenVPN.
- `openvpn-maintenance`: one-shot CLI container mounting the same data and YAML without TUN or `NET_ADMIN`. It sets `OVPN_MAINTENANCE=true`.

```bash
# live query or client operation
docker compose exec openvpn ovpn client list

# offline state/config/migration operation
docker compose run --rm openvpn-maintenance state doctor
```

Both services must use the same target image and mount the same `openvpn-data` and `openvpn-config` directories.

Add the following service next to `openvpn` in `compose.yaml`. It deliberately has no `devices`, `cap_add`, or published ports:

```yaml
  openvpn-maintenance:
    image: szcq/openvpn:2.7.5
    restart: "no"
    network_mode: host
    environment:
      OVPN_MAINTENANCE: "true"
    volumes:
      - ./openvpn-data:/etc/openvpn
      - ./openvpn-config:/etc/openvpn-config
    profiles:
      - maintenance
    entrypoint:
      - /usr/local/bin/ovpn
    command:
      - state
      - doctor
```

Use the same pinned image tag as the live service. The default `state doctor` command runs only when the service is started without an explicit command; `docker compose run --rm openvpn-maintenance config plan` replaces it with `config plan`.

---

## Initial deployment

1. Create the data and configuration directories with restricted permissions.
2. Copy the repository's root `config.example.yaml` to `openvpn-config/config.yaml`, then validate the endpoint, IPv4 network, routing, and NAT choices.
3. Start the service. The entrypoint initializes only an empty data directory.
4. Verify state and runtime health before issuing clients.

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
cp config.example.yaml openvpn-config/config.yaml
$EDITOR openvpn-config/config.yaml

docker compose up -d openvpn
docker compose logs openvpn
docker compose exec openvpn ovpn state doctor
docker compose exec openvpn ovpn runtime health
```

Initialization fails closed if YAML is missing or invalid, the data directory is non-empty but unrecognized, PKI generation fails, or staged state does not validate.

For a first deployment without manually creating YAML, leave the configuration directory empty and set the Compose service environment below. The entrypoint validates it, writes mode-`0600` canonical YAML, and then runs the same initialization:

```yaml
services:
  openvpn:
    environment:
      OVPN_BOOTSTRAP_FROM_ENV: "true"
      OVPN_BOOTSTRAP_ENDPOINT: vpn.example.com
      OVPN_BOOTSTRAP_IPV4_NETWORK: 10.42.0.0/24
```

After a successful start, set `OVPN_BOOTSTRAP_FROM_ENV=false`. Keeping it true only produces an ignored-bootstrap warning; it never updates the initialized instance. See the [command reference](commands.md#one-time-environment-bootstrap) for optional fields.

## Choose routing deliberately

Private-network access:

```yaml
ipv4:
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  routes:
    - 192.168.50.0/24
```

The destination network must have a return route to the VPN CIDR through the OpenVPN host.

Full-tunnel Internet access:

```yaml
ipv4:
  nat:
    enabled: true
    interface: auto
  redirectGateway: true
  dns:
    - 1.1.1.1
    - 8.8.8.8
```

With `network_mode: host`, forwarding and project-owned iptables rules exist in the host network namespace. Review the host firewall and cloud security group. The runtime reconciles only its instance-specific chain/comments and does not flush unrelated rules.

## Day-to-day operations

Every public multi-letter option has a single-token short form. The examples use both forms interchangeably; pass short options separately rather than clustering them.

### Create and distribute clients

Create and export in one command when the profile should be written to the operator's current directory:

```bash
docker compose exec -T openvpn \
  ovpn client create laptop -4 -o - > laptop.ovpn
chmod 600 laptop.ovpn
docker compose exec openvpn ovpn client create phone -4 dynamic
docker compose exec openvpn ovpn client create tablet -4 10.42.0.20
```

Profiles contain private keys. Store the exported file with mode `0600` and distribute it through a secure channel.

### View client status

```bash
docker compose exec openvpn ovpn client list -d
docker compose exec openvpn ovpn client list -u
```

Typical `client list --detail` output looks like this (IDs and addresses are examples):

```text
CLIENT ID     NAME      STATUS   IPV4 MODE  IPV4 ADDRESS  IPV4 STATE
111111111111  laptop    active   static     10.42.0.2    active
222222222222  phone     active   dynamic    10.42.0.129  active
333333333333  retired   revoked  static     10.42.0.20   retained
```

`STATUS` is the credential lifecycle state. `IPV4 MODE` is `static`, `dynamic`, or `none`; a dynamic address is the last recorded lease and may be `-` before the first connection. `IPV4 STATE` is assignment state such as `active`, `retained`, or `none`. Use `--full-id/-u` for complete UUIDs and `--json/-j` for automation.

The default shortened ID can be copied after `--id/-i`. Positional values are exact names; `--name/-n` is the explicit equivalent:

```bash
docker compose exec -T openvpn \
  ovpn client export -i 844854e4 -o - > laptop.ovpn
docker compose exec -T openvpn \
  ovpn client export -n laptop -o - > laptop.ovpn
```

ID prefixes require at least eight hexadecimal characters and must identify exactly one client. A positional value is never inferred to be an ID.

### Rename a client

```bash
docker compose exec openvpn ovpn client rename laptop office-laptop
```

Rename preserves the immutable UUID, certificate identity, address assignment, and audit history. Redistribute the profile only when users need the new filename or embedded display name.

### Revoke and release addresses

```bash
# revoke and retain a static reservation
docker compose exec openvpn ovpn client revoke office-laptop

# revoke and release the reservation immediately
docker compose exec openvpn ovpn client revoke office-laptop --release-ipv4

# release a retained reservation later
docker compose exec openvpn ovpn client address release office-laptop
```

Revocation regenerates the CRL and attempts to disconnect the active session after committing. Releasing an address does not delete the client or certificate history.

### Reissue credentials

```bash
# retain the current address intent
docker compose exec -T openvpn \
  ovpn client reissue laptop -o - > laptop.ovpn

# reissue and allocate the lowest free static address
docker compose exec -T openvpn \
  ovpn client reissue laptop -4 -o - > laptop.ovpn

# reissue with dynamic addressing
docker compose exec -T openvpn \
  ovpn client reissue laptop -4 dynamic -o - > laptop.ovpn

chmod 600 laptop.ovpn
```

Reissue keeps the UUID but replaces the certificate and private key. The old session is disconnected after commit; always redistribute the replacement profile.

### Delete a client

```bash
docker compose exec openvpn ovpn client delete office-laptop --yes
```

Delete removes local credentials and assignment state while retaining a UUID tombstone. Recover deleted private keys only from a secure backup.

Revoke, reissue, delete, and address changes attempt to disconnect affected sessions after their durable commit. If the broker is unavailable, the mutation remains committed and reports a pending warning; retry `ovpn runtime disconnect NAME` after runtime health is restored.

---

## Address management

### Single-client operations

```bash
docker compose exec openvpn \
  ovpn client address set -n laptop -4 dynamic
docker compose exec openvpn \
  ovpn client address set phone -4
docker compose exec openvpn \
  ovpn client address set -n tablet -4 10.42.0.30
```

`-4` without a value means `auto` and allocates the lowest available static address.

### Batch operations

Batch changes always open an editor. Use `--yes` only to skip the confirmation prompt before the editor opens:

```bash
docker compose exec openvpn \
  ovpn client address edit -n laptop -n phone -y
```

The file contains one `client,ipv4` row per selected active client. Use `auto`, `dynamic`, or a static address. The whole file is validated and committed atomically. The default editor is the installed `nano`; set `EDITOR` to change the general default or `OVPN_EDITOR` to override it only for this command. Selection order is `OVPN_EDITOR`, `EDITOR`, then `nano`.

### Release a revoked reservation

Release a revoked client's retained static address:

```bash
docker compose exec openvpn \
  ovpn client address release -n retired-device
```

## Declarative configuration changes

### Validate and apply changes

YAML changes do nothing until explicitly applied. Start with online validation and planning:

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

Then stop OpenVPN and apply through the maintenance service:

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config validate
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply -y
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

Inspect the plan before applying. Endpoint/transport changes require profile redistribution. Network/pool changes can remap static assignments and require CCD/server regeneration. NAT, routes, and redirect-gateway changes require firewall reconciliation after restart.

If YAML differs but has not been applied, restarting the server continues with the old applied revision and prints a warning. This protects the running service from accidental file edits.

### Export the applied configuration

Recover a complete desired YAML from applied state:

```bash
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export -o - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
```

## State diagnosis and repair

### Inspect instance state

Read-only diagnosis can run online, but offline diagnosis produces a stable view when a repair or restore is being considered:

```bash
docker compose exec openvpn ovpn state show
docker compose exec openvpn ovpn state doctor -j

docker compose stop openvpn
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```

### Repair a degraded instance

Apply only the reported eligible actions:

```bash
docker compose run --rm openvpn-maintenance repair apply -y
docker compose run --rm openvpn-maintenance state doctor
```

Repair can rebuild derived files and recover artifacts only from mutually consistent evidence. It does not reconstruct a missing or corrupt SQLite authority from guesses. `CRITICAL` and `UNRECOVERABLE` database conditions require a trusted backup.

## Runtime inspection

### Status and session control

```bash
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime status -j
docker compose exec openvpn ovpn runtime disconnect laptop
docker compose exec openvpn ovpn runtime disconnect -i 844854e4 -j
docker compose exec openvpn ovpn runtime capabilities -j
```

### Logs and events

```bash
docker compose exec openvpn ovpn runtime logs -l 200
docker compose exec openvpn ovpn runtime logs -l 0 -f
docker compose exec openvpn ovpn runtime logs -r -u
docker compose exec openvpn ovpn runtime events -l 200 -j
docker compose exec openvpn ovpn runtime events -l 0 -f
```

Logs are persistent and rotate according to applied configuration. Events are a user-facing JSONL stream. SQLite `audit_events` are authoritative business audit records and are not a substitute for runtime logs.

## Shell completion

Generate scripts from the same command contract used by `ovpn help`:

These scripts complete a direct command named `ovpn`. Run them inside an interactive service container, or define a host wrapper with that name for `docker compose exec openvpn ovpn`. To generate through Compose, replace `ovpn completion` below with `docker compose exec -T openvpn ovpn completion`.

```bash
mkdir -p ~/.local/share/bash-completion/completions ~/.zfunc \
  ~/.config/fish/completions
ovpn completion bash > ~/.local/share/bash-completion/completions/ovpn
ovpn completion zsh > ~/.zfunc/_ovpn
ovpn completion fish > ~/.config/fish/completions/ovpn.fish
```

Start a new shell after installation. Name and ID completion performs a read-only client list query through the same command/wrapper only after an explicit selector option.

## Upgrade from schema 3

Before migration:

- Confirm the source instance reports schema 3 under the `sh-ver` image.
- Upgrade schema 1/2 to schema 3 with `sh-ver`; v4 does not read schema 1/2.
- Make an independent operator backup of the complete data directory.
- Pin the target v4 image for both Compose services.

Plan and apply:

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance migrate plan
docker compose run --rm openvpn-maintenance migrate plan --json
docker compose run --rm openvpn-maintenance migrate apply --yes
docker compose run --rm openvpn-maintenance state doctor
umask 077
docker compose run --rm -T openvpn-maintenance \
  config export --output - > openvpn-config/config.yaml.new &&
  mv openvpn-config/config.yaml.new openvpn-config/config.yaml
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

Migration preserves schema 3 UUID certificate identities and imports current client/address/audit/artifact state. It removes the live legacy structured files after success; originals remain only in the migration snapshot.

## Roll back a successful schema migration

An image switch is not sufficient. Restore the complete migration snapshot and then run `sh-ver`:

```bash
docker compose stop openvpn

sudo cp openvpn-data/repair/migrations/schema3-pre-v4.tar.gz .
sudo cp openvpn-data/repair/migrations/schema3-pre-v4.tar.gz.sha256 .
sudo chown "$(id -u):$(id -g)" schema3-pre-v4.tar.gz schema3-pre-v4.tar.gz.sha256
sha256sum -c schema3-pre-v4.tar.gz.sha256

mkdir openvpn-data-schema3
chmod 750 openvpn-data-schema3
sudo tar --numeric-owner -xzf schema3-pre-v4.tar.gz -C openvpn-data-schema3

mv openvpn-data openvpn-data-schema4
mv openvpn-data-schema3 openvpn-data

# Point OVPN_IMAGE to the stable sh-ver image before startup.
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
```

Keep `openvpn-data-schema4` until the rollback is verified. The restored schema 3 tree and `sh-ver` image are a matched unit.

## Offline backup and restore

SQLite and file artifacts must always be backed up and restored together.

### Backup

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-$(date +%Y%m%d%H%M%S).tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

Store the archive encrypted. It contains the CA, server/client private keys, tls-crypt key, profiles, and database.

### Restore

Restore into an empty working directory while no container is running:

```bash
mkdir restore-work
sudo tar --numeric-owner -xzf openvpn-v4-backup.tar.gz -C restore-work

mv openvpn-data openvpn-data-before-restore
mv openvpn-config openvpn-config-before-restore
mv restore-work/openvpn-data ./openvpn-data
mv restore-work/openvpn-config ./openvpn-config

docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

Do not merge a backup into an existing data directory and do not restore only `state.db`. Keep the previous directories until the restored instance and at least one client connection are verified.

## Image update without a schema change

When both images use schema 4:

```bash
docker compose stop openvpn
# Update OVPN_IMAGE, then:
docker compose pull openvpn openvpn-maintenance
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn version --json
docker compose exec openvpn ovpn runtime health
```

Do not run `migrate apply` for a same-schema image change.
