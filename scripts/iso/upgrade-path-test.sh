#!/bin/bash
# Upgrade-path test, run by scripts/vm-upgrade-path-test.ps1 in a privileged
# Debian 12 container with KVM. Same networks as update-test.sh:
#
#   [inet netns]──br-wan──eth0 [firewall VM] eth1──br-lan──[client netns]
#   203.0.113.1: DHCP, DNS, NAT     installed from an OLD     192.168.1.250
#   203.0.113.80:8000: APT repo     release's ISO
#
# The VM is installed from the oldest supported release (built from its tag),
# set up with a config that uses every feature that release had, and then
# updated straight to the current build through its own updater, the way a
# user who skipped every beta would. Checks: the update finishes, the new
# version runs, the running config came through unchanged, a new commit works,
# DNS and DHCP still answer, and the new APIs answer.
#
# Usage: upgrade-path-test.sh <iso> <old version> <new version>   (debs in /debs, key in /key)
set -uo pipefail
ISO=$1 OLD=$2 NEW=$3
W=/work/upgrade
A=$W/artifacts
mkdir -p "$A"
cd "$W"
FAILS=0
log() { echo "[path] $*"; }
check() { # check <name> <command...>: PASS/FAIL line, never aborts
	if "${@:2}" >>"$A/checks.log" 2>&1; then log "PASS $1"; else log "FAIL $1"; FAILS=$((FAILS + 1)); fi
}
retry() { # retry <seconds> <command...>
	local end=$(( $(date +%s) + $1 )); shift
	until "$@" >/dev/null 2>&1; do [ "$(date +%s)" -lt "$end" ] || return 1; sleep 3; done
}
deb() { echo "/debs/goblin-gate_$1_amd64.deb"; }

log "installing tools"
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq qemu-system-x86 qemu-utils kmod dnsmasq iproute2 iptables curl dnsutils jq python3 procps apt-utils gnupg isc-dhcp-client >/dev/null 2>&1
modprobe kvm_amd 2>/dev/null || modprobe kvm_intel 2>/dev/null || true
[ -e /dev/kvm ] || mknod /dev/kvm c 10 232
ACCEL=kvm; qemu-system-x86_64 -accel kvm -M none -display none -S -daemonize -pidfile /tmp/k.pid 2>/dev/null && kill "$(cat /tmp/k.pid)" || ACCEL=tcg

# --- the repository: the old and the new version
REPO=/tmp/repo
APT_SIGNING_KEY="$(cat /key/test.asc)" bash /scripts/apt-repo.sh "$REPO" beta "$(deb "$OLD")" "$(deb "$NEW")" ||
	{ log "could not build the repository"; exit 1; }

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

# --- networks (as in update-test.sh)
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
iptables -I FORWARD -p udp --dport 123 -j DROP # keep the host clock (see update-test.sh)
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
	--no-resolv --server="${UPSTREAM:-8.8.8.8}" --log-facility="$A/dnsmasq.log" \
	--dhcp-leasefile=/tmp/inet.leases --pid-file=/tmp/dnsmasq.pid
$IN python3 -m http.server 8000 --bind 203.0.113.80 --directory "$REPO" >"$A/repo-http.log" 2>&1 &
ip netns add client
mkdir -p /etc/netns/client; : > /etc/netns/client/resolv.conf
ip link add client-br type veth peer name lan0; ip link set lan0 netns client; ip link set client-br master br-lan up
CL="ip netns exec client"
$CL ip link set lo up; $CL ip link set lan0 up
$CL ip addr add 192.168.1.250/24 dev lan0

# --- boot the old release
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
PASS='Goblin-Test-Passw0rd!'
$CL curl -sk -m 120 -H 'Content-Type: application/json' -d "{\"admin_password\":\"$PASS\",\"wan\":{\"device\":\"eth0\",\"mode\":\"dhcp\"},\"lan\":{\"device\":\"eth1\",\"address\":\"192.168.1.1/24\",\"dhcp_start\":\"192.168.1.100\",\"dhcp_end\":\"192.168.1.199\"},\"security_level\":\"basic\"}" \
	"$BOX/api/v1/setup" >"$A/setup.json"
CSRF=$($CL curl -sk -m 30 -c /tmp/jar -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$PASS\"}" "$BOX/api/v1/auth/login" | jq -r .csrf_token)
TOKEN=$($CL curl -sk -m 30 -b /tmp/jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{"name":"path-test"}' "$BOX/api/v1/auth/tokens" | jq -r .token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || { log "could not get an API token"; exit 1; }
api() { # api <METHOD> <path> [json body]
	$CL curl -sk -m 300 -X "$1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"} "$BOX$2"
}
patch() { # patch <path> <json>: fails on validation errors
	api PATCH /api/v1/config/candidate "{\"path\":\"$1\",\"value\":$2}" | tee -a "$A/patches.log" | jq -e '(.validation_errors // []) | length == 0' >/dev/null ||
		log "patch $1 had validation errors (see patches.log)"
}
version() { api GET /api/v1/system/status | jq -r .version; }
log "installed: $(version)"
check old-installed sh -c "test '$(version)' = '$OLD'"

# --- a config with everything the old release could do
patch dns '{"forwarders":["203.0.113.1"],"dnssec":false,"local_records":{"nas.home.arpa":"192.168.1.20"}}'
patch system.update_url '"http://203.0.113.80:8000"'
patch vlans '[{"name":"iot","parent":"lan","vlan_id":20,"mode":"static","address":"192.168.20.1/24","enabled":true,"allow_access":["ping"]}]'
patch addresses '[{"name":"nas","type":"host","value":"192.168.1.20"},{"name":"kid-tablet","type":"host","value":"192.168.1.140"},{"name":"lab","type":"subnet","value":"10.50.0.0/16"}]'
patch address_groups '[{"name":"kids","members":["kid-tablet"]}]'
patch services '[{"name":"plex","protocol":"tcp","ports":"32400"}]'
patch schedules '[{"name":"bedtime","ranges":[{"days":["mon","tue","wed","thu","sun"],"start":"21:00","end":"07:00"}]}]'
patch profiles '{"ips":[{"name":"default"}],"dnsfilter":[{"name":"family"}]}'
patch dhcp '[{"interface":"lan","range_start":"192.168.1.100","range_end":"192.168.1.199","lease_seconds":86400,"dns":["192.168.1.1"],"gateway":"192.168.1.1","static_leases":[{"mac":"00:11:32:aa:bb:01","ip":"192.168.1.20","hostname":"nas"}]},{"interface":"iot","range_start":"192.168.20.100","range_end":"192.168.20.199","dns":["192.168.20.1"],"gateway":"192.168.20.1"}]'
patch policies '[{"id":1,"name":"kids-bedtime","enabled":true,"src_intf":["lan"],"dst_intf":["wan1"],"src_addr":["kids"],"dst_addr":["all"],"service":["all"],"action":"deny","schedule":"bedtime","log":"all"},{"id":2,"name":"lan-to-internet","enabled":true,"src_intf":["lan"],"dst_intf":["wan1"],"src_addr":["all"],"dst_addr":["all"],"service":["all"],"action":"accept","nat":true,"dnsfilter_profile":"family"},{"id":3,"name":"iot-to-internet","enabled":true,"src_intf":["iot"],"dst_intf":["wan1"],"src_addr":["all"],"dst_addr":["all"],"service":["all"],"action":"accept","nat":true,"ips_profile":"default"},{"id":4,"name":"lan-to-iot","enabled":true,"src_intf":["lan"],"dst_intf":["iot"],"src_addr":["all"],"dst_addr":["all"],"service":["all"],"action":"accept"}]'
patch nat '{"port_forwards":[{"name":"plex","in_intf":"wan1","ext_port":"32400","protocol":"tcp","to_addr":"192.168.1.20","to_port":"32400"}]}'
api POST /api/v1/commit '{"comment":"the old config"}' >"$A/commit-old.json"
check old-commit sh -c "jq -e '.status == \"ok\" or .revision != null' '$A/commit-old.json'"
api GET /api/v1/config/running >"$A/running-old.json"

# --- update straight to the new build, through the box's own updater
api GET "/api/v1/system/updates?refresh=1" >"$A/updates.json"
check new-available sh -c "jq -e '.available == \"$NEW\"' '$A/updates.json'"
api POST /api/v1/system/updates/apply '{}' | tee "$A/apply.json"; echo
wait_update() {
	local end=$(( $(date +%s) + $1 )) st
	while [ "$(date +%s)" -lt "$end" ]; do
		st=$(api GET /api/v1/system/updates 2>/dev/null | jq -r '.last.state // empty' 2>/dev/null)
		case "$st" in done | rolled_back | failed) echo "$st"; return 0 ;; esac
		sleep 5
	done
	echo timeout
}
R=skipped; grep -q started "$A/apply.json" && R=$(wait_update 1200)
log "update $OLD -> $NEW: $R"
api GET /api/v1/system/updates >"$A/after.json"
check update-done sh -c "test '$R' = done"
check new-running retry 120 sh -c "test \"\$($CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/system/status | jq -r .version)\" = '$NEW'"

# --- the config came through: every section the old release wrote is the same
api GET /api/v1/config/running >"$A/running-new.json"
for s in interfaces vlans addresses address_groups services schedules policies nat dhcp dns profiles; do
	check "config-$s" sh -c "test \"\$(jq -cS '.$s' '$A/running-old.json')\" = \"\$(jq -cS '.$s' '$A/running-new.json')\""
done
# --- and the new build works with it
check new-commit sh -c "api() { $CL curl -sk -m 300 -X \"\$1\" -H 'Authorization: Bearer $TOKEN' -H 'Content-Type: application/json' \${3:+-d \"\$3\"} '$BOX'\"\$2\"; }
	api PATCH /api/v1/config/candidate '{\"path\":\"system.hostname\",\"value\":\"after-upgrade\"}' >/dev/null &&
	api POST /api/v1/commit '{\"comment\":\"after the upgrade\"}' | tee '$A/commit-new.json' | jq -e '.status == \"ok\" or .revision != null'"
check dns-works retry 60 sh -c "$CL dig +time=3 +tries=2 @192.168.1.1 +short nas.home.arpa | grep -qx 192.168.1.20"
check dhcp-works sh -c "$CL timeout 30 dhclient -1 -v -sf /bin/true lan0 2>&1 | grep -q 'DHCPACK'"
check checks-api sh -c "$CL curl -sk -m 30 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/checks | jq -e '.score >= 0'"
check templates-api sh -c "$CL curl -sk -m 30 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/templates | jq -e '.templates | length >= 5'"
api GET /api/v1/checks >"$A/checks.json"
log "Goblin Checks after the upgrade: score $(jq .score "$A/checks.json"), $(jq '.findings | length' "$A/checks.json") finding(s)"

if [ $FAILS -eq 0 ]; then log "ALL PASS"; else log "$FAILS FAILED"; exit 1; fi
