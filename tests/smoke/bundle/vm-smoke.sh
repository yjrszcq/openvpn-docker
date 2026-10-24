#!/usr/bin/env bash
# Boot the distribution's real init in an isolated VM with its own kernel/netfilter.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
archive="$(realpath "${1:?archive required}")"
base="${2:-debian:13-slim}"
install_dir="${3:-/opt/ovpn}"
case "$archive" in *-linux-amd64.tar.gz) architecture=amd64 ;; *-linux-arm64.tar.gz) architecture=arm64 ;; *)
  echo 'unknown archive architecture' >&2
  exit 1
  ;;
esac
driver="ovpn-bundle-vm-driver:$architecture"
work="$(mktemp -d)"
container=''
cleanup() {
  if [ -n "$container" ]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
  docker run --rm -v "$work:/work" alpine:3.24 sh -c 'rm -rf /work/*' >/dev/null 2>&1 || true
  rmdir "$work" 2>/dev/null || true
}
trap cleanup EXIT
mkdir -p "$work/fixtures/releases"
python3 "$ROOT_DIR/tests/smoke/bundle/fixtures.py" "$archive" "$work/fixtures"
cp "$ROOT_DIR/scripts/install.sh" "$work/fixtures/"
cp "$ROOT_DIR/tests/smoke/bundle/vm-guest.sh" "$work/fixtures/"
printf '%s\n' "$install_dir" >"$work/fixtures/install-dir"
docker build --build-arg "KERNEL_PLATFORM=linux/$architecture" -t "$driver" -f "$ROOT_DIR/packaging/bundle/vm/Dockerfile" "$ROOT_DIR"
rm -rf "$work/fixtures/releases/openvpn-server"
image="ovpn-vm-guest:$(printf '%s' "${base%@*}" | tr '/:' '--')-$architecture"
docker build --platform "linux/$architecture" --build-arg "BASE=$base" -t "$image" -f "$ROOT_DIR/packaging/bundle/vm/guest.Dockerfile" "$work"
container="$(docker create --platform "linux/$architecture" "$image")"
docker export "$container" >"$work/root.tar"
docker rm "$container" >/dev/null
container=''
vm_args=(--rm -e "VM_ARCH=$architecture" -v "$work:/work")
if [ -c /dev/kvm ] && { { [ "$architecture" = amd64 ] && [ "$(uname -m)" = x86_64 ]; } || { [ "$architecture" = arm64 ] && [ "$(uname -m)" = aarch64 ]; }; }; then vm_args+=(--device /dev/kvm); fi
docker run "${vm_args[@]}" "$driver" bash -euc '
 if [ "$VM_ARCH" = amd64 ]; then
  emulator=(qemu-system-x86_64); cpu=qemu64; console=ttyS0
 else emulator=(qemu-system-aarch64 -machine virt); cpu=cortex-a72; console=ttyAMA0; fi
 if [ -c /dev/kvm ]; then emulator+=(-enable-kvm); cpu=host; fi
 mkdir /work/rootfs
 tar -xf /work/root.tar -C /work/rootfs
 cp -a /lib/modules /work/rootfs/lib/
 # Debian kmod has no gzip support; use uncompressed modules with fresh indices
 # rather than relying on the guest compression configuration.
 find /work/rootfs/lib/modules -name "*.ko.gz" -exec gunzip {} \;
 depmod -b /work/rootfs "$(ls /lib/modules | head -n1)"
 # Docker deliberately injects a read-only hosts/resolver; replace exported
 # placeholders with normal guest files and avoid container-specific init hints.
 rm -f /work/rootfs/.dockerenv
 printf "127.0.0.1 localhost\n" >/work/rootfs/etc/hosts
 printf "nameserver 10.0.2.3\n" >/work/rootfs/etc/resolv.conf
 printf "/dev/vda / ext4 defaults 0 0\n" >/work/rootfs/etc/fstab
 truncate -s 4G /work/root.img
 mkfs.ext4 -q -F -d /work/rootfs /work/root.img
 for boot in 1 2; do
    timeout 900 "${emulator[@]}" -cpu "$cpu" -m 1024 -smp 2 -nographic -no-reboot \
   -kernel /boot/vmlinuz-virt -initrd /boot/initramfs-virt \
      -append "root=/dev/vda rw console=$console rootfstype=ext4 quiet systemd.firstboot=no" \
   -drive file=/work/root.img,format=raw,if=virtio \
   -netdev user,id=net -device virtio-net-pci,netdev=net >"/work/boot-$boot.log" 2>&1
  cat "/work/boot-$boot.log"
 done
 grep -Fq OVPN_VM_TEST_PASS /work/boot-2.log
 ! grep -Fq OVPN_VM_TEST_FAIL /work/boot-1.log
'
printf 'VM service verification passed: %s linux/%s\n' "$base" "$architecture"
