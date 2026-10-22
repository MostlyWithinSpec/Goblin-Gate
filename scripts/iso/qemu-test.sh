#!/bin/bash
# Installs the unattended ISO in QEMU/KVM and checks the first boot through
# the GG-SELFTEST lines on the serial port (scripts/vm-install-test.ps1).
# Usage: qemu-test.sh <iso> <uefi|bios>
set -uo pipefail
ISO=$1 MODE=$2
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq qemu-system-x86 qemu-utils ovmf kmod >/dev/null 2>&1
modprobe kvm_amd 2>/dev/null || modprobe kvm_intel 2>/dev/null || true
[ -e /dev/kvm ] || mknod /dev/kvm c 10 232
ACCEL=kvm; qemu-system-x86_64 -accel kvm -M none -display none -S -daemonize -pidfile /tmp/k.pid 2>/dev/null && kill "$(cat /tmp/k.pid)" || ACCEL=tcg
W=/work/$MODE; rm -rf "$W"; mkdir -p "$W"; cd "$W"
qemu-img create -f qcow2 disk.qcow2 12G >/dev/null
FW=()
if [ "$MODE" = uefi ]; then
	cp /usr/share/OVMF/OVMF_VARS_4M.ms.fd vars.fd
	FW=(-machine q35,smm=on -global driver=cfi.pflash01,property=secure,value=on
		-drive if=pflash,format=raw,unit=0,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.secboot.fd
		-drive if=pflash,format=raw,unit=1,file=vars.fd)
else
	FW=(-machine q35)
fi
COMMON=(-accel $ACCEL -cpu max -m 2048 -smp 2 -display none -no-reboot
	-device ahci,id=ahci -drive file=disk.qcow2,if=none,id=hd -device ide-hd,drive=hd,bus=ahci.0
	-netdev user,id=wan -device virtio-net-pci,netdev=wan
	-netdev user,id=lan,restrict=on -device virtio-net-pci,netdev=lan)

echo "[$MODE] installing ($ACCEL)..."
start=$(date +%s)
timeout 3600 qemu-system-x86_64 "${FW[@]}" "${COMMON[@]}" -serial file:install.log \
	-drive file="$ISO",media=cdrom,if=none,id=cd,readonly=on -device ide-cd,drive=cd,bus=ahci.1 -boot order=dc
echo "[$MODE] installer finished after $(( $(date +%s) - start ))s"
grep -q -i "error\|failed" install.log && grep -i -m5 "error\|failed" install.log | cut -c1-200

echo "[$MODE] first boot..."
qemu-system-x86_64 "${FW[@]}" "${COMMON[@]}" -serial file:boot.log -daemonize -pidfile qemu.pid
for i in $(seq 1 120); do grep -q "GG-SELFTEST end" boot.log 2>/dev/null && break; sleep 5; done
kill "$(cat qemu.pid)" 2>/dev/null
if grep -q "GG-SELFTEST end" boot.log; then
	grep "GG-SELFTEST" boot.log | tr -d '\r'
	# the packages the installer adds besides goblin-gate must be there
	# (a broken install step once left them all out silently)
	missing=""
	grep -q "GG-SELFTEST suricata This is Suricata" boot.log || missing="$missing suricata"
	for p in tailscale suricata-update openssh-server miniupnpd-nftables nut-client nut-server tcpdump traceroute unattended-upgrades git; do
		grep -q "GG-SELFTEST pkg $p ii" boot.log || missing="$missing $p"
	done
	if [ -n "$missing" ]; then echo "[$MODE] FAIL: not installed:$missing"; exit 1; fi
	echo "[$MODE] PASS"
else
	echo "[$MODE] no self-test report; last boot lines:"; tail -30 boot.log | tr -d '\r'
	echo "[$MODE] FAIL"; exit 1
fi
