#!/bin/bash
# Builds the Goblin Gate installer ISO with simple-cdd (task 038). Runs as
# root in a Debian 12 container with the repo at /src and the goblin-gate
# .deb in /src/dist (scripts/build-iso.ps1 does all of it).
#
# UNATTENDED=1 builds a test image: it installs to /dev/sda without
# questions, uses classic NIC names (eth0/eth1, as in the default config),
# logs to the serial port and runs a self-test after the first boot that
# prints "GG-SELFTEST" lines there (scripts/vm-install-test.ps1).
set -euo pipefail
VERSION=${VERSION:-dev}
UNATTENDED=${UNATTENDED:-0}
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq simple-cdd xorriso curl gnupg ca-certificates openssl >/dev/null

LOCAL=/build/local
mkdir -p "$LOCAL" /build/dl
cp /src/dist/goblin-gate_*.deb "$LOCAL/"

# Suricata 7 and suricata-update come from bookworm-backports: ship the
# backports packages on the CD (their other dependencies are in main).
echo "deb http://deb.debian.org/debian bookworm-backports main" > /etc/apt/sources.list.d/backports.list
# Tailscale from its own repository
curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.noarmor.gpg -o /usr/share/keyrings/tailscale-archive-keyring.gpg
curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.tailscale-keyring.list -o /etc/apt/sources.list.d/tailscale.list
apt-get update -qq
apt-get install -y -qq --download-only -o Dir::Cache::archives=/build/dl -t bookworm-backports suricata suricata-update >/dev/null
apt-get install -y -qq --download-only -o Dir::Cache::archives=/build/dl tailscale >/dev/null
cp /build/dl/*bpo*.deb /build/dl/tailscale_*.deb "$LOCAL/"
echo "local packages:"; ls "$LOCAL"

cd /build
rm -rf profiles && cp -r /src/packaging/iso/profiles .
# root: the hash of a random password that is thrown away
ROOTHASH=$(openssl passwd -6 "$(head -c 32 /dev/urandom | base64)")
sed -i "s|@ROOTHASH@|${ROOTHASH}|" profiles/goblin.preseed
if [ "$UNATTENDED" = 1 ]; then
	ST=$(base64 -w0 /src/scripts/iso/goblin-selftest)
	SU=$(base64 -w0 /src/scripts/iso/goblin-selftest.service)
	cat >> profiles/goblin.preseed <<PRESEED
d-i partman-auto/disk string /dev/sda
d-i pkgsel/include string curl
d-i preseed/late_command string in-target sh -c 'echo $ST | base64 -d > /usr/local/sbin/goblin-selftest && chmod 755 /usr/local/sbin/goblin-selftest && echo $SU | base64 -d > /etc/systemd/system/goblin-selftest.service && systemctl enable goblin-selftest.service'
PRESEED
fi
build-simple-cdd --conf profiles/goblin.conf --local-packages "$LOCAL" --dist bookworm --force-root 2>&1 | tail -20
ISO=$(ls /build/images/*.iso | head -1)

if [ "$UNATTENDED" = 1 ]; then
	# boot straight into the automatic install (UEFI grub; BIOS isolinux)
	mkdir -p /build/patch && cd /build/patch
	xorriso -osirrox on -indev "$ISO" -extract /boot/grub/grub.cfg grub.cfg -extract /isolinux/isolinux.cfg isolinux.cfg -extract /isolinux/txt.cfg txt.cfg 2>/dev/null || true
	chmod u+w ./*
	AUTO="auto=true priority=critical locale=en_US.UTF-8 keymap=us preseed/file=/cdrom/simple-cdd/default.preseed simple-cdd/profiles=goblin console=ttyS0,115200"
	TARGET="net.ifnames=0 console=tty1 console=ttyS0,115200"
	{
		echo "set timeout=1"
		echo "set default=0"
		echo "menuentry 'Automatic install' {"
		echo "    linux /install.amd/vmlinuz $AUTO --- $TARGET"
		echo "    initrd /install.amd/initrd.gz"
		echo "}"
		cat grub.cfg
	} > grub.new
	{
		echo "serial 0 115200"
		echo "default autoinstall"
		echo "prompt 0"
		echo "timeout 1"
		echo "label autoinstall"
		echo "    kernel /install.amd/vmlinuz"
		echo "    append initrd=/install.amd/initrd.gz $AUTO --- $TARGET"
	} > isolinux.new # only this entry: the included menus set their own default (installgui)
	xorriso -indev "$ISO" -outdev /build/unattended.iso -boot_image any replay \
		-map grub.new /boot/grub/grub.cfg -map isolinux.new /isolinux/isolinux.cfg 2>&1 | tail -2
	ISO=/build/unattended.iso
	SUFFIX=-unattended
fi
OUT=/src/dist/goblin-gate-${VERSION}-amd64${SUFFIX:-}.iso
# Copy under a temporary name and rename when complete, so a half-written
# ISO never carries the final name (booting one panics the installer).
rm -f "$OUT" "$OUT.sha256"
cp "$ISO" "$OUT.part"
(cd /src/dist && sha256sum "$(basename "$OUT").part" | sed 's/\.part$//' > "$(basename "$OUT").sha256")
mv "$OUT.part" "$OUT"
echo "built $OUT ($(du -h "$OUT" | cut -f1))"
