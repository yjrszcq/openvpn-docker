ARG KERNEL_PLATFORM=linux/amd64
FROM --platform=${KERNEL_PLATFORM} alpine:3.24.3 AS kernel
RUN apk add --no-cache linux-virt mkinitfs
RUN mkdir -p /boot && printf 'features="base scsi virtio ext4"\n' >/etc/mkinitfs/mkinitfs.conf \
 && mkinitfs -c /etc/mkinitfs/mkinitfs.conf "$(ls /lib/modules | head -n1)"
FROM alpine:3.24.3
RUN apk add --no-cache qemu-system-x86_64 qemu-system-aarch64 qemu-img e2fsprogs bash tar coreutils kmod
COPY --from=kernel /boot/ /boot/
COPY --from=kernel /lib/modules/ /lib/modules/
