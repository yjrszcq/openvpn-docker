# OpenVPN v4 Operations Guide

This guide organizes the schema 4 CLI by operator workflow. See the
[v4 command reference](commands.md) for every option and exit code.

Persistent compatibility follows the
[data schema upgrade policy](../data-schema-upgrade-policy.md). Image delivery
and rollback follow the [image update policy](../image-update-policy.md).

## Container roles

- `openvpn`: live service with `/dev/net/tun`, `NET_ADMIN`, the management
  broker, and OpenVPN.
- `openvpn-maintenance`: one-shot CLI container mounting the same data and YAML
  without TUN or `NET_ADMIN`. It sets `OVPN_MAINTENANCE=true`.

```bash
# live query or client operation
docker compose exec openvpn ovpn client list

# offline state/config/migration operation
docker compose run --rm openvpn-maintenance state doctor
```

Both services must use the same target image and mount the same
`openvpn-data` and `openvpn-config` directories.

## Initial deployment

1. Create the data and configuration directories with restricted permissions.
2. Write `openvpn-config/config.yaml` and validate the endpoint, IPv4 network,
   routing, and NAT choices.
3. Start the service. The entrypoint initializes only an empty data directory.
4. Verify state and runtime health before issuing clients.

```bash
mkdir -p openvpn-data openvpn-config
chmod 750 openvpn-data openvpn-config
$EDITOR openvpn-config/config.yaml

docker compose up -d openvpn
docker compose logs openvpn
docker compose exec openvpn ovpn state doctor
docker compose exec openvpn ovpn runtime health
```

Initialization fails closed if YAML is missing or invalid, the data directory
is non-empty but unrecognized, PKI generation fails, or staged state does not
validate.

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

The destination network must have a return route to the VPN CIDR through the
OpenVPN host.

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

With `network_mode: host`, forwarding and project-owned iptables rules exist in
the host network namespace. Review the host firewall and cloud security group.
The runtime reconciles only its instance-specific chain/comments and does not
flush unrelated rules.

## Day-to-day client lifecycle

Create and export:

```bash
docker compose exec openvpn ovpn client create laptop --ipv4 auto
docker compose exec openvpn ovpn client create phone --ipv4 dynamic
docker compose exec openvpn ovpn client create tablet --ipv4 10.42.0.20

docker compose exec -T openvpn \
  ovpn client export --name laptop --output - > laptop.ovpn
chmod 600 laptop.ovpn
```

List and select by immutable ID:

```bash
docker compose exec openvpn ovpn client list --detail
docker compose exec openvpn ovpn client list --full-id
docker compose exec -T openvpn \
  ovpn client export --id 844854e4 --output - > laptop.ovpn
```

Rename, revoke, reissue, and delete:

```bash
docker compose exec openvpn \
  ovpn client rename --name laptop office-laptop

docker compose exec openvpn \
  ovpn client revoke --name office-laptop

docker compose exec openvpn \
  ovpn client reissue --name office-laptop --ipv4 auto
docker compose exec -T openvpn \
  ovpn client export --name office-laptop --output - > office-laptop.ovpn

docker compose exec openvpn \
  ovpn client delete --name office-laptop --yes
```

After revoke, reissue, or an address change, disconnect any prior session.
After reissue, redistribute the new profile. Delete retains a UUID tombstone but
removes local credentials; retain backups if recovery may be required.

## Address management

```bash
docker compose exec openvpn \
  ovpn client address set --name laptop --ipv4 dynamic
docker compose exec openvpn \
  ovpn client address set --name phone --ipv4 auto
docker compose exec openvpn \
  ovpn client address set --name tablet --ipv4 10.42.0.30
```

Batch changes require an interactive editor or `--yes`:

```bash
docker compose exec openvpn \
  ovpn client address edit --name laptop --name phone --yes
```

The file contains one `client,ipv4` row per selected active client. Use
`auto`, `dynamic`, or a static address. The whole file is validated and
committed atomically.

Release a revoked client's retained static address:

```bash
docker compose exec openvpn \
  ovpn client address release --name retired-device
```

## Declarative configuration changes

YAML changes do nothing until explicitly applied. Start with online validation
and planning:

```bash
docker compose exec openvpn ovpn config validate
docker compose exec openvpn ovpn config plan
```

Then stop OpenVPN and apply through the maintenance service:

```bash
docker compose stop openvpn
docker compose run --rm openvpn-maintenance config validate
docker compose run --rm openvpn-maintenance config plan
docker compose run --rm openvpn-maintenance config apply --yes
docker compose run --rm openvpn-maintenance state doctor
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

Inspect the plan before applying. Endpoint/transport changes require profile
redistribution. Network/pool changes can remap static assignments and require
CCD/server regeneration. NAT, routes, and redirect-gateway changes require
firewall reconciliation after restart.

If YAML differs but has not been applied, restarting the server continues with
the old applied revision and prints a warning. This protects the running
service from accidental file edits.

Recover a complete desired YAML from applied state:

```bash
docker compose run --rm openvpn-maintenance \
  config export --output /etc/openvpn-config/config.yaml
```

## State diagnosis and repair

Read-only diagnosis can run online, but offline diagnosis produces a stable
view when a repair or restore is being considered:

```bash
docker compose exec openvpn ovpn state show
docker compose exec openvpn ovpn state doctor --json

docker compose stop openvpn
docker compose run --rm openvpn-maintenance state doctor
docker compose run --rm openvpn-maintenance repair plan
```

Apply only the reported eligible actions:

```bash
docker compose run --rm openvpn-maintenance repair apply --yes
docker compose run --rm openvpn-maintenance state doctor
```

Repair can rebuild derived files and recover artifacts only from mutually
consistent evidence. It does not reconstruct a missing/corrupt SQLite authority
from guesses. `CRITICAL` and `UNRECOVERABLE` database conditions require a
trusted backup.

## Runtime inspection

```bash
docker compose exec openvpn ovpn runtime status
docker compose exec openvpn ovpn runtime status --json
docker compose exec openvpn ovpn runtime capabilities --json
docker compose exec openvpn ovpn runtime logs --lines 200
docker compose exec openvpn ovpn runtime logs --lines 0 --follow
docker compose exec openvpn ovpn runtime logs --raw --full-id
docker compose exec openvpn ovpn runtime events --lines 200 --json
docker compose exec openvpn ovpn runtime events --lines 0 --follow
```

Logs are persistent and rotate according to applied configuration. Events are a
user-facing JSONL stream. SQLite `audit_events` are authoritative business
audit records and are not a substitute for runtime logs.

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
docker compose run --rm openvpn-maintenance \
  config export --output /etc/openvpn-config/config.yaml
docker compose up -d openvpn
docker compose exec openvpn ovpn runtime health
```

Migration preserves schema 3 UUID certificate identities and imports current
client/address/audit/artifact state. It removes the live legacy structured
files after success; originals remain only in the migration snapshot.

## Roll back a successful schema migration

An image switch is not sufficient. Restore the complete migration snapshot and
then run `sh-ver`:

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

Keep `openvpn-data-schema4` until the rollback is verified. The restored schema
3 tree and `sh-ver` image are a matched unit.

## Offline backup and restore

SQLite and file artifacts must always be backed up and restored together.

```bash
docker compose stop openvpn
sudo tar --numeric-owner -czf openvpn-v4-$(date +%Y%m%d%H%M%S).tar.gz \
  openvpn-data openvpn-config
docker compose up -d openvpn
```

Store the archive encrypted. It contains the CA, server/client private keys,
tls-crypt key, profiles, and database.

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

Do not merge a backup into an existing data directory and do not restore only
`state.db`. Keep the previous directories until the restored instance and at
least one client connection are verified.

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
