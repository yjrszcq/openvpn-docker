# Native Linux installation

Linux archives include `ovpn`, OpenVPN, OpenSSL, Easy-RSA, the musl loader, shared libraries, providers, network tools, shell and editor. These components do not need to be installed on the host. Docker remains available with its existing configuration and commands.

Use `linux-amd64` for x86-64 and `linux-arm64` for AArch64. The host needs Linux TUN support, network administration privileges and a reachable OpenVPN port. Configure host/cloud firewalls yourself; `ovpn` manages only its instance rules and does not disable SELinux or firewalld. IPv4 tunnels support UDP/TCP transport over IPv4 or IPv6; IPv6 tunnel addressing is not supported.

Compatibility covers Debian 13.7, Ubuntu 26.10, Rocky Linux 10.2, Alpine 3.24.3 and current Arch Linux. AArch64 Arch compatibility refers separately to Arch Linux ARM. Local AArch64 verification uses emulation; the Release workflow additionally requires native AArch64 acceptance before uploading attachments.

## Online installation

Wait until the Release page contains both architecture archives and `SHA256SUMS`. The bootstrap requires Bash, curl, tar, a `sha256sum` tool and standard Linux command-line utilities. It does not install host packages.

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/install.sh')"
```

The installer selects the latest stable Release and the host architecture, verifies the archive, installs to `/opt/ovpn`, creates `/usr/local/bin/ovpn`, and registers a systemd or OpenRC service. It leaves the service stopped and disabled. Existing unrelated commands, directories and service definitions are refused.

To choose a directory and a published version, replace `vX.Y.Z`:

```bash
sudo env OVPN_INSTALL_DIR=/opt/vpn OVPN_VERSION=vX.Y.Z bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/install.sh')"
```

Use the same `OVPN_INSTALL_DIR` for subsequent updates. One managed `ovpn` service is supported per host.

## Configure and start

```bash
sudo cp /opt/ovpn/config/config.example.yaml /opt/ovpn/config/config.yaml
sudo /opt/ovpn/bin/nano /opt/ovpn/config/config.yaml
```

Set `server.endpoint` to your public hostname/IP and choose a non-overlapping `ipv4.network`. Review transport, NAT, routes and DNS settings before starting. The bundled editor is also available as `/opt/ovpn/bin/nano`.

For systemd:

```bash
sudo systemctl enable --now ovpn
sudo systemctl status ovpn
```

For OpenRC:

```bash
sudo rc-update add ovpn default
sudo rc-service ovpn start
```

For foreground operation:

```bash
sudo /opt/ovpn/ovpn
```

No arguments starts the service and initializes empty data using the existing configuration. Missing configuration produces instructions and stops. Do not run a foreground instance alongside the managed service. SIGTERM or Ctrl-C stops child processes and cleans instance network rules.

## Files and everyday commands

| Path relative to the extracted directory | Purpose |
| --- | --- |
| `ovpn` | Relocatable CLI and foreground entrypoint |
| `runtime/`, `bin/` | Bundled programs, libraries, resources and licenses |
| `config/config.yaml` | Your configuration |
| `data/` | SQLite, PKI, profiles, generated configuration and audit state |
| `logs/` | Link to `data/logs/`, including rotated OpenVPN logs |
| `run/` | Runtime sockets and service diagnostic output |

```bash
sudo ovpn runtime health
sudo ovpn client create laptop --ipv4 auto
sudo ovpn client export laptop --output /root/laptop.ovpn
sudo ovpn state doctor
sudo ovpn config plan
sudo ovpn config apply --yes
```

The CLI and optional REST API retain their existing interfaces and `OVPN_*` overrides. Overrides apply to each process; use the same settings for the service and CLI. For installer-managed updates, keep writable state under the installation's `config/`, `data/` and `run/` directories so it is included in recovery snapshots. Use `journalctl -u ovpn` for service startup diagnostics; OpenRC writes them under `run/service*.log`. The [REST API guide](rest-api.md) documents enabling and securing the API.

## Update and recover

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/update.sh')"
```

The updater downloads and verifies first, then locks the installation. A running service is stopped, snapshotted, updated and restarted with a health check; a stopped service stays stopped. Your configuration, certificates, database, profiles, logs, permissions and service enablement are retained. A failed replacement or health check restores the previous program and snapshot. After an interrupted update, rerun the updater to recover the transaction. A retained `.update-backup/` means recovery needs attention; preserve it until the instance is healthy.

Updates require the same data schema and refuse downgrades. Repeating installation or updating to the installed version preserves state. Stop manually launched instances before updating; the updater refuses to terminate them. Keep independent backups as described in the [operations guide](operations.md).

## Manual extraction and relocation

Download the matching archive and `SHA256SUMS` from the same Release. Replace `X.Y.Z` and the architecture as needed:

```bash
awk '$2 == "openvpn-server-X.Y.Z-linux-amd64.tar.gz" {print}' SHA256SUMS | sha256sum -c -
tar -xzf openvpn-server-X.Y.Z-linux-amd64.tar.gz
cd openvpn-server
cp config/config.example.yaml config/config.yaml
# Edit config/config.yaml before starting.
sudo ./ovpn
```

No service manager is required for manual foreground operation. To use the CLI through PATH, add the extraction root, for example `export PATH="/opt/ovpn:$PATH"`; keep the whole directory together. Absolute paths, symlinks, different working directories and paths containing spaces are supported.

Before initialization, move the directory freely. For an initialized instance, stop it, move the complete directory, then run `sudo /new/path/ovpn repair apply --yes` to regenerate derived absolute paths before restarting. Update any service definition and command symlink that still points to the old location. The installer-managed updater targets the directory and service originally registered; manual relocations require adjusting those paths first.
