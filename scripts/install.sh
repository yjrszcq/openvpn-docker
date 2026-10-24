#!/usr/bin/env bash
# Online installer/updater; safe to execute using bash -c "$(curl -fsSL URL)".
set -euo pipefail
umask 077
fail() {
  printf 'ovpn: %s\n' "$*" >&2
  exit 1
}
[ "$(id -u)" = 0 ] || fail 'installation requires root'
for tool in curl tar sha256sum awk sed grep mktemp readlink seq find; do command -v "$tool" >/dev/null || fail "missing prerequisite: $tool"; done
action="${OVPN_INSTALL_ACTION:-install}"
case "$action" in install | update) ;; *) fail 'invalid install action' ;; esac
root="${OVPN_INSTALL_DIR:-/opt/ovpn}"
case "$root" in /*) ;; *) fail 'installation directory must be absolute' ;; esac
case "$root" in / | /opt | /usr | /usr/local | /usr/local/bin | *$'\n'* | *$'\r'* | *'"'* | *'%'* | *\\* | *'$'* | *'`'* | *'/../'* | *'/./'* | */.. | */.) fail 'unsafe installation directory' ;; esac
root="${root%/}"
[ ! -L "$root" ] || fail 'installation directory must not be a symlink'
parent="$(dirname "$root")"
mkdir -p "$parent"
root="$(cd "$parent" && pwd -P)/$(basename "$root")"
link="${OVPN_COMMAND_LINK:-/usr/local/bin/ovpn}"
[ "$link" != "$root/ovpn" ] || fail 'command link must be outside the installation'
if [ -e "$link" ] || [ -L "$link" ]; then
  if [ ! -L "$link" ] || [ "$(readlink "$link")" != "$root/ovpn" ]; then fail "refusing to replace existing command: $link"; fi
fi
manager=''
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  manager=systemd
elif command -v rc-service >/dev/null && [ -d /run/openrc ]; then
  manager=openrc
else
  fail 'no running systemd/OpenRC found; extract the archive manually for foreground use'
fi
case "$(uname -m)" in x86_64) architecture=amd64 ;; aarch64 | arm64) architecture=arm64 ;; *) fail 'unsupported CPU architecture' ;; esac
version="${OVPN_VERSION:-}"
repository='yjrszcq/openvpn-docker'
if [ -z "$version" ]; then
  location="$(curl --fail --silent --show-error --location --retry 3 --output /dev/null --write-out '%{url_effective}' "https://github.com/$repository/releases/latest")"
  version="${location##*/}"
fi
version="${version#v}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'invalid release version'
archive="openvpn-server-$version-linux-$architecture.tar.gz"
base="${OVPN_DOWNLOAD_BASE_URL:-https://github.com/$repository/releases/download/v$version}"
work="$(mktemp -d)"
staging=''
transaction=0
new_install=0
new_service=0
was_running=0
journal="$root/.update-backup"
service() {
  if [ "$manager" = systemd ]; then
    systemctl "$1" ovpn.service
  else rc-service ovpn "$1"; fi
}
healthy() {
  local _
  for _ in $(seq 1 60); do
    if "$root/ovpn" runtime health >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}
rollback() {
  [ -d "$journal" ] || return 0
  if [ ! -f "$journal/ready" ]; then
    if [ "$(cat "$journal/was-running" 2>/dev/null)" = 1 ]; then service start || return 1; fi
    rm -rf -- "$journal"
    return 0
  fi
  service stop >/dev/null 2>&1 || true
  for name in ovpn bin runtime MANIFEST.sha256 config data run; do
    rm -rf -- "${root:?}/$name"
    if [ -e "$journal/$name" ] || [ -L "$journal/$name" ]; then cp -a "$journal/$name" "$root/$name"; fi
  done
  if [ "$(cat "$journal/was-running")" = 1 ]; then
    if ! service start || ! healthy; then
      printf 'ovpn: rollback restored files, but service recovery failed; backup: %s\n' "$journal" >&2
      return 1
    fi
  fi
  rm -rf -- "$journal"
}
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  exec 8>&-
  if [ "$transaction" = 1 ]; then rollback || status=1; fi
  if [ "$status" != 0 ] && [ "$new_install" = 1 ]; then
    if [ "$new_service" = 1 ]; then
      if [ "$manager" = systemd ]; then
        rm -f /etc/systemd/system/ovpn.service
        systemctl daemon-reload || true
      else rm -f /etc/init.d/ovpn; fi
    fi
    if [ -L "$link" ] && [ "$(readlink "$link")" = "$root/ovpn" ]; then rm -f "$link"; fi
    rm -rf -- "$root"
  fi
  rm -rf -- "$work"
  if [ -n "$staging" ]; then rm -rf -- "$staging"; fi
  exec 9>&-
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
curl --fail --silent --show-error --location --retry 3 "$base/$archive" -o "$work/$archive"
curl --fail --silent --show-error --location --retry 3 "$base/SHA256SUMS" -o "$work/SHA256SUMS"
checksum="$(awk -v name="$archive" '$2 == name {print $1}' "$work/SHA256SUMS")"
[[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || fail 'missing or duplicate archive checksum'
(cd "$work" && printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c -)
# Validate every member before extraction, including links. No devices, hard
# links, absolute paths, parent traversal or symlinked extraction directories.
tar -tzf "$work/$archive" >"$work/members"
awk '
 /^openvpn-server\/bin\/\[\[?$/ {next}
 !/^openvpn-server\/[A-Za-z0-9_.+@,=\/-]*$/ {exit 1}
 /(^|\/)\.\.?($|\/)/ {exit 1}
 END {if (NR == 0) exit 1}
' "$work/members" || fail 'unsafe archive member'
tar -tvzf "$work/$archive" >"$work/details"
awk '
 substr($1,1,1) !~ /^[-dl]$/ {exit 1}
 substr($1,1,1) == "l" {
   if ($(NF-1) != "->") exit 1
   name=$(NF-2); target=$NF
   if (name ~ /^openvpn-server\/bin\// && target == "../ovpn") next
   if (name == "openvpn-server/logs" && target == "data/logs") next
   if (target !~ /^[A-Za-z0-9_.+-]+$/ || target == "." || target == "..") exit 1
 }
' "$work/details" || fail 'unsafe archive type or link'
# Reject any member beneath a symlink, regardless of archive ordering.
awk 'substr($1,1,1)=="l" {print $(NF-2)}' "$work/details" >"$work/links"
while IFS= read -r target; do
  if awk -v prefix="$target/" 'index($0,prefix)==1 {found=1} END {exit !found}' "$work/members"; then fail 'archive writes through a symlink'; fi
done <"$work/links"
mkdir "$work/extract"
tar -xzf "$work/$archive" -C "$work/extract"
package="$work/extract/openvpn-server"
if [ ! -f "$package/MANIFEST.sha256" ] || [ -L "$package/MANIFEST.sha256" ]; then fail 'package manifest missing'; fi
awk '$1 !~ /^[0-9a-f]+$/ || length($1)!=64 || NF!=2 || $2 !~ /^(ovpn|runtime\/[A-Za-z0-9_.+@,=\/-]+|config\/config.example.yaml)$/ || $2 ~ /(^|\/)\.\.?($|\/)/ {exit 1}' "$package/MANIFEST.sha256" || fail 'unsafe file manifest'
(cd "$package" && sha256sum -c MANIFEST.sha256 >/dev/null) || fail 'file manifest verification failed'
# Every regular file must be listed, and every listed path must be regular.
awk '{print $2}' "$package/MANIFEST.sha256" >"$work/expected-files"
(cd "$package" && find . -type f ! -path ./MANIFEST.sha256 | sed 's|^./||') >"$work/actual-files"
awk 'FILENAME==ARGV[1] {expected[$0]++; count++; next} {if (!expected[$0]--) bad=1; actual++} END {exit (bad || actual!=count)}' "$work/expected-files" "$work/actual-files" || fail 'package file inventory mismatch'
manifest="$package/runtime/manifest.json"
grep -Fq "\"version\": \"$version\"" "$manifest" || fail 'package version mismatch'
grep -Fq "\"architecture\": \"$architecture\"" "$manifest" || fail 'package architecture mismatch'
grep -Eq '"data_schema": 4([,[:space:]]|$)' "$manifest" || fail 'unsupported data schema'
[ "$("$package/ovpn" version --short)" = "$version" ] || fail 'packaged executable version mismatch'
lockfile="$root.install-lock"
if [ ! -e "$lockfile" ] && [ ! -L "$lockfile" ]; then
  (
    set -C
    : >"$lockfile"
  ) || fail 'installation lock creation raced another process; retry'
fi
if [ ! -f "$lockfile" ] || [ -L "$lockfile" ]; then fail 'unsafe installation lock'; fi
exec 9<>"$lockfile"
"$package/bin/flock" -n 9 || fail 'another installation/update is active'
if [ -e "$root" ]; then
  [ -f "$root/.ovpn-install" ] || fail 'existing directory is not an ovpn installation'
  [ "$(cat "$root/.ovpn-install")" = "$manager" ] || fail 'service manager changed'
  if [ -d "$journal" ]; then rollback || fail 'interrupted update recovery failed'; fi
  grep -Eq '"data_schema": 4([,[:space:]]|$)' "$root/runtime/manifest.json" || fail 'installed data schema is incompatible'
  [ -x "$root/ovpn" ] || fail 'existing executable is missing'
  old_version="$("$root/ovpn" version --short)"
  [[ "$old_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'invalid installed version'
  IFS=. read -r old_major old_minor old_patch <<<"$old_version"
  IFS=. read -r new_major new_minor new_patch <<<"$version"
  if ((10#$new_major < 10#$old_major || (10#$new_major == 10#$old_major && 10#$new_minor < 10#$old_minor) || (10#$new_major == 10#$old_major && 10#$new_minor == 10#$old_minor && 10#$new_patch < 10#$old_patch))); then fail 'downgrade refused'; fi
  [ "$action" = update ] || {
    printf 'ovpn is installed; use update.sh\n'
    exit 0
  }
  if [ "$old_version" = "$version" ]; then
    printf 'ovpn is already %s\n' "$version"
    exit 0
  fi
  if service status >/dev/null 2>&1; then
    was_running=1
  elif "$root/ovpn" runtime status >/dev/null 2>&1; then fail 'unmanaged instance detected; stop it before updating'; fi
  # Preserve state before any new executable can open the database.
  mkdir "$journal"
  printf '%s\n' "$was_running" >"$journal/was-running"
  transaction=1
  if [ "$was_running" = 1 ]; then service stop; fi
  if [ -f "$root/run/.server.lock" ]; then
    exec 8<>"$root/run/.server.lock"
    "$package/bin/flock" -n 8 || fail 'unmanaged instance detected; stop it before updating'
  fi
  for name in ovpn bin runtime MANIFEST.sha256 config data run; do
    if [ -e "$root/$name" ] || [ -L "$root/$name" ]; then cp -a "$root/$name" "$journal/$name"; fi
  done
  : >"$journal/ready"
  transaction=1
  for name in ovpn bin runtime MANIFEST.sha256; do
    rm -rf -- "${root:?}/$name"
    cp -a "$package/$name" "$root/$name"
  done
  cp -a "$package/config/config.example.yaml" "$root/config/config.example.yaml"
  exec 8>&-
  if [ "$was_running" = 1 ]; then
    service start
    healthy || fail 'updated service failed health checks'
  fi
  transaction=0
  rm -rf -- "$journal"
  printf 'updated ovpn %s -> %s; original service state restored\n' "$old_version" "$version"
else
  [ "$action" = install ] || fail 'no installation to update'
  if [ "$manager" = systemd ]; then
    if [ -e /etc/systemd/system/ovpn.service ] || [ -L /etc/systemd/system/ovpn.service ]; then fail 'existing ovpn.service must not be replaced'; fi
  else
    if [ -e /etc/init.d/ovpn ] || [ -L /etc/init.d/ovpn ]; then fail 'existing OpenRC ovpn service must not be replaced'; fi
  fi
  staging="$(mktemp -d "$parent/.ovpn-install.XXXXXX")"
  cp -a "$package/." "$staging/"
  printf '%s\n' "$manager" >"$staging/.ovpn-install"
  mv "$staging" "$root"
  new_install=1
  mkdir -p "$(dirname "$link")"
  ln -s "$root/ovpn" "$link"
  if [ "$manager" = systemd ]; then
    if [ -e /etc/systemd/system/ovpn.service ] || [ -L /etc/systemd/system/ovpn.service ]; then fail 'existing ovpn.service must not be replaced'; fi
    new_service=1
    cat >/etc/systemd/system/ovpn.service <<SERVICE
[Unit]
Description=OpenVPN server
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart="$root/ovpn"
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
KillMode=mixed
[Install]
WantedBy=multi-user.target
SERVICE
    systemctl daemon-reload
    printf 'Configure %s/config/config.yaml, then: systemctl enable --now ovpn\n' "$root"
  else
    if [ -e /etc/init.d/ovpn ] || [ -L /etc/init.d/ovpn ]; then fail 'existing OpenRC ovpn service must not be replaced'; fi
    new_service=1
    cat >/etc/init.d/ovpn <<SERVICE
#!/sbin/openrc-run
name="ovpn"
command="$root/ovpn"
pidfile="/run/ovpn-service.pid"
output_log="$root/run/service.log"
error_log="$root/run/service-error.log"
depend() { need net; }
start() {
 ebegin "Starting ovpn"
 start-stop-daemon --start --background --make-pidfile --pidfile "\$pidfile" \
  --exec "\$command" --stdout "\$output_log" --stderr "\$error_log"
 eend \$?
}
stop() {
 ebegin "Stopping ovpn"
 if start-stop-daemon --stop --test --quiet --pidfile "\$pidfile"; then
  start-stop-daemon --stop --retry TERM/30/KILL/5 --pidfile "\$pidfile" || { eend 1; return 1; }
 fi
 rm -f "\$pidfile"
 eend 0
}
status() {
 if start-stop-daemon --stop --test --quiet --pidfile "\$pidfile"; then einfo "ovpn is started"; return 0; fi
 einfo "ovpn is stopped"
 return 3
}
SERVICE
    chmod 755 /etc/init.d/ovpn
    printf 'Configure %s/config/config.yaml, then: rc-update add ovpn default; rc-service ovpn start\n' "$root"
  fi
  printf 'installed ovpn %s at %s; service is not started or enabled\n' "$version" "$root"
fi
