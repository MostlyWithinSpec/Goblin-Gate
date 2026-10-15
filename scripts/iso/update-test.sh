#!/bin/bash
# Update test (task 051), run by scripts/vm-update-test.ps1 in a privileged
# Debian 12 container with KVM:
#
#   [inet netns]──br-wan──eth0 [firewall VM] eth1──br-lan──[client netns]
#   203.0.113.1: DHCP, DNS, NAT     installed from the     192.168.1.250
#   203.0.113.80:8000: APT repo     test ISO (version 1)
#
# The repository is signed with a throwaway test key that the test packages
# ship instead of ours. The test points system.update_url at the local
# repository and, over the API: updates 1 -> 2 (must succeed), then 2 -> 3,
# whose goblind is broken (exits at once): the updater must roll back to 2
# and the box must keep working. Artifacts go to /work/update/artifacts.
#
# Usage: update-test.sh <iso> <v1> <v2> <v3>   (debs in /debs, key in /key)
set -uo pipefail
ISO=$1 V1=$2 V2=$3 V3=$4
export DEBIAN_FRONTEND=noninteractive
W=/work/update A=/work/update/artifacts
mkdir -p "$W" "$A"; cd "$W"
log() { echo "[upd] $*"; }
FAILS=0
check() { # check <name> <command...>: PASS/FAIL line, never aborts
	local name=$1; shift
	if "$@" >"$A/check-$name.txt" 2>&1; then echo "[upd] PASS $name"; else echo "[upd] FAIL $name: $(tail -c 300 "$A/check-$name.txt" | tr '\n' ' ')"; FAILS=$((FAILS + 1)); fi
}
retry() { # retry <seconds> <command...>
	local t=$1; shift
	local end=$(( $(date +%s) + t ))
	until "$@"; do [ "$(date +%s)" -ge "$end" ] && return 1; sleep 3; done
}
deb() { echo "/debs/goblin-gate_$1_amd64.deb"; }

log "installing tools"
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq qemu-system-x86 qemu-utils kmod dnsmasq iproute2 iptables curl dnsutils jq python3 procps apt-utils gnupg >/dev/null 2>&1
modprobe kvm_amd 2>/dev/null || modprobe kvm_intel 2>/dev/null || true
[ -e /dev/kvm ] || mknod /dev/kvm c 10 232
ACCEL=kvm; qemu-system-x86_64 -accel kvm -M none -display none -S -daemonize -pidfile /tmp/k.pid 2>/dev/null && kill "$(cat /tmp/k.pid)" || ACCEL=tcg

# --- the repository: versions 1 and 2 for now
REPO=/tmp/repo
repo_add() { APT_SIGNING_KEY="$(cat /key/test.asc)" bash /scripts/apt-repo.sh "$REPO" beta "$@"; }
repo_add "$(deb "$V1")" "$(deb "$V2")" || { log "could not build the repository"; exit 1; }

# --- firewall disk: install once per ISO (cached), then a fresh overlay per run
BASE=$W/base-$(basename "$ISO" .iso).qcow2
if [ ! -f "$BASE" ]; then
	log "installing the firewall from $(basename "$ISO") ($ACCEL, ~6 min)"
	rm -f "$W"/base-*.qcow2
	qemu-img create -f qcow2 "$BASE.tmp" 12G >/dev/null
	timeout 3600 qemu-system-x86_64 -machine q35 -accel $ACCEL -cpu max -m 2048 -smp 2 -display none -no-reboot \
		-device ahci,id=ahci -drive file="$BASE.tmp",if=none,id=hd -device ide-hd,drive=hd,bus=ahci.0 \
		-netdev user,id=n0 -device virtio-net-pci,netdev=n0 -netdev user,id=n1,restrict=on -device virtio-net-pci,netdev=n1 \
		-drive file="$ISO",media=cdrom,if=none,id=cd,readonly=on -device ide-cd,drive=cd,bus=ahci.1 -boot order=dc \
		-serial file:"$A/install.log"
	mv "$BASE.tmp" "$BASE"
fi
rm -f run.qcow2; qemu-img create -f qcow2 -b "$BASE" -F qcow2 run.qcow2 >/dev/null

# --- networks (as in net-test.sh)
log "building the networks"
sysctl -qw net.ipv4.ip_forward=1
for br in br-wan br-lan; do ip link add $br type bridge; ip link set $br up; done
for t in tap-wan tap-lan; do ip tuntap add $t mode tap; done
ip link set tap-wan master br-wan up; ip link set tap-lan master br-lan up
sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null || true

ip netns add inet
ip link add inet-br type veth peer name inet0; ip link set inet0 netns inet; ip link set inet-br master br-wan up
ip link add up-root type veth peer name up-inet; ip link set up-inet netns inet
ip addr add 10.99.0.1/30 dev up-root; ip link set up-root up
ip route add 203.0.113.0/24 via 10.99.0.2
iptables -t nat -A POSTROUTING -s 10.99.0.0/30 -j MASQUERADE
iptables -t nat -A POSTROUTING -s 203.0.113.0/24 ! -d 203.0.113.0/24 -j MASQUERADE
iptables -P FORWARD ACCEPT
# The repository is signed with this container's clock (the host's, which may be
# off); NTP would give the box the real time and apt would reject a Release
# "not valid yet". Without NTP the box keeps the host clock from its RTC.
iptables -I FORWARD -p udp --dport 123 -j DROP
IN="ip netns exec inet"
$IN ip link set lo up
$IN ip addr add 203.0.113.1/24 dev inet0
$IN ip addr add 203.0.113.80/24 dev inet0
$IN ip link set inet0 up
$IN ip addr add 10.99.0.2/30 dev up-inet; $IN ip link set up-inet up
$IN ip route add default via 10.99.0.1
$IN sysctl -qw net.ipv4.ip_forward=1
UPSTREAM=$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)
$IN dnsmasq --interface=inet0 --bind-interfaces --except-interface=lo \
	--dhcp-range=203.0.113.100,203.0.113.150,1h --dhcp-option=3,203.0.113.1 --dhcp-option=6,203.0.113.1 \
	--address=/www.test.example/203.0.113.80 \
	--no-resolv --server="${UPSTREAM:-8.8.8.8}" --log-dhcp --log-facility="$A/dnsmasq.log" \
	--dhcp-leasefile=/tmp/inet.leases --pid-file=/tmp/dnsmasq.pid
$IN python3 -m http.server 8000 --bind 203.0.113.80 --directory "$REPO" >"$A/repo-http.log" 2>&1 &

ip netns add client
mkdir -p /etc/netns/client; : > /etc/netns/client/resolv.conf
ip link add client-br type veth peer name lan0; ip link set lan0 netns client; ip link set client-br master br-lan up
CL="ip netns exec client"
$CL ip link set lo up; $CL ip link set lan0 up
$CL ip addr add 192.168.1.250/24 dev lan0

# --- boot the firewall: eth0 = WAN (br-wan), eth1 = LAN (br-lan)
log "booting the firewall"
qemu-system-x86_64 -machine q35 -accel $ACCEL -cpu max -m 3072 -smp 2 -display none \
	-device ahci,id=ahci -drive file=run.qcow2,if=none,id=hd -device ide-hd,drive=hd,bus=ahci.0 \
	-netdev tap,id=wan,ifname=tap-wan,script=no,downscript=no -device virtio-net-pci,netdev=wan,mac=52:54:00:00:01:01 \
	-netdev tap,id=lan,ifname=tap-lan,script=no,downscript=no -device virtio-net-pci,netdev=lan,mac=52:54:00:00:01:02 \
	-serial file:"$A/boot.log" -daemonize -pidfile /tmp/fw.pid
cleanup() {
	kill "$(cat /tmp/fw.pid 2>/dev/null)" 2>/dev/null
	kill "$(cat /tmp/dnsmasq.pid 2>/dev/null)" 2>/dev/null
	pkill -f http.server 2>/dev/null
}
trap cleanup EXIT

BOX=https://192.168.1.1
retry 420 $CL curl -sk -m 5 -o /dev/null "$BOX/api/v1/setup/state" || { log "the API never came up"; tail -40 "$A/boot.log"; exit 1; }
log "API up after boot"
PASS='Goblin-Test-Passw0rd!'
$CL curl -sk -m 120 -H 'Content-Type: application/json' -d "{\"admin_password\":\"$PASS\",\"wan\":{\"device\":\"eth0\",\"mode\":\"dhcp\"},\"lan\":{\"device\":\"eth1\",\"address\":\"192.168.1.1/24\",\"dhcp_start\":\"192.168.1.100\",\"dhcp_end\":\"192.168.1.199\"},\"security_level\":\"off\"}" \
	"$BOX/api/v1/setup" >"$A/setup.json"
CSRF=$($CL curl -sk -m 30 -c /tmp/jar -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$PASS\"}" "$BOX/api/v1/auth/login" | jq -r .csrf_token)
TOKEN=$($CL curl -sk -m 30 -b /tmp/jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{"name":"update-test"}' "$BOX/api/v1/auth/tokens" | jq -r .token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || { log "could not get an API token"; exit 1; }
api() { # api <METHOD> <path> [json body]
	$CL curl -sk -m 300 -X "$1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"} "$BOX$2"
}
patch() { api PATCH /api/v1/config/candidate "{\"path\":\"$1\",\"value\":$2}" | jq -c '.validation_errors'; }
patch dns '{"forwarders":["203.0.113.1"],"dnssec":false}'
patch system.update_url '"http://203.0.113.80:8000"'
api POST /api/v1/commit '{"comment":"update-test"}' >"$A/commit.json"

version() { api GET /api/v1/system/status | jq -r .version; }
# wait_update: until the last update is no longer running (goblind restarts
# during it, so failed requests are retried); prints the final state.
wait_update() {
	local end=$(( $(date +%s) + $1 )) st
	while [ "$(date +%s)" -lt "$end" ]; do
		st=$(api GET /api/v1/system/updates 2>/dev/null | jq -r '.last.state // empty' 2>/dev/null)
		case "$st" in done | rolled_back | failed) echo "$st"; return 0 ;; esac
		sleep 5
	done
	echo timeout
}
updates() { api GET "/api/v1/system/updates?refresh=1" | tee "$A/updates-$1.json"; }

log "installed: $(version)"
check v1-installed sh -c "test '$(version)' = '$V1'"

# --- 1 -> 2: a good update
updates 1 >/dev/null
check v2-available sh -c "jq -e '.available == \"$V2\"' '$A/updates-1.json'"
api POST /api/v1/system/updates/apply '{}' | tee "$A/apply-2.json"; echo
R=skipped; grep -q started "$A/apply-2.json" && R=$(wait_update 900)
log "update to $V2: $R"
api GET /api/v1/system/updates >"$A/after-2.json"
check update-done sh -c "test '$R' = done"
check v2-running retry 60 sh -c "test \"\$($CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/system/status | jq -r .version)\" = '$V2'"

# --- 2 -> 3: goblind 3 does not start; the updater must restore 2
repo_add "$(deb "$V3")" >/dev/null
updates 2 >/dev/null
check v3-available sh -c "jq -e '.available == \"$V3\"' '$A/updates-2.json'"
api POST /api/v1/system/updates/apply '{}' | tee "$A/apply-3.json"; echo
R=skipped; grep -q started "$A/apply-3.json" && R=$(wait_update 1200)
log "update to $V3: $R"
api GET /api/v1/system/updates >"$A/after-3.json"
check rolled-back sh -c "test '$R' = rolled_back"
check v2-restored sh -c "test '$(version)' = '$V2'"
check box-works retry 60 sh -c "$CL dig +time=3 +tries=2 @192.168.1.1 +short www.test.example | grep -qx 203.0.113.80"
log "last update: $(jq -c '.last' "$A/after-3.json")"

for p in /api/v1/system/status /api/v1/config/running; do
	f=$(echo "$p" | tr '/?=&' '____'); api GET "$p" >"$A/api$f.json" 2>&1
done
if [ $FAILS -eq 0 ]; then log "ALL PASS"; else log "$FAILS FAILED"; exit 1; fi
