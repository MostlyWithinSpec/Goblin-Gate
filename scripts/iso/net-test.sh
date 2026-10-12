#!/bin/bash
# Multi-VM network test (task 039), run by scripts/vm-net-test.ps1 in a
# privileged Debian 12 container with KVM:
#
#   [inet netns]──br-wan──eth0 [firewall VM] eth1──br-lan──[client netns]
#   203.0.113.1 (+.80-.82)        installed from        DHCP from the box
#   dnsmasq (DHCP, DNS),          the unattended ISO
#   web server, NAT to the
#   real internet (rules)
#
# The firewall is the real installed system; the "internet" and the LAN
# client are network namespaces on the same L2 bridges (lighter than VMs,
# same packets). The test runs the setup wizard API, commits a config with
# a deny policy, DNS filter, IP feed, IPS profile and port forward, and
# checks: DHCP lease, DNS, NAT out, port forward in, deny + log, DNS filter
# block, feed block, IPS alert. Artifacts go to /work/net/artifacts.
#
# Usage: net-test.sh <iso>
set -uo pipefail
ISO=$1
export DEBIAN_FRONTEND=noninteractive
W=/work/net A=/work/net/artifacts
mkdir -p "$W" "$A"; cd "$W"
log() { echo "[net] $*"; }
FAILS=0
check() { # check <name> <command...>: PASS/FAIL line, never aborts
	local name=$1; shift
	if "$@" >"$A/check-$name.txt" 2>&1; then echo "[net] PASS $name"; else echo "[net] FAIL $name: $(tail -c 300 "$A/check-$name.txt" | tr '\n' ' ')"; FAILS=$((FAILS + 1)); fi
}
retry() { # retry <seconds> <command...>
	local t=$1; shift
	local end=$(( $(date +%s) + t ))
	until "$@"; do [ "$(date +%s)" -ge "$end" ] && return 1; sleep 3; done
}

log "installing tools"
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq qemu-system-x86 qemu-utils kmod dnsmasq iproute2 iptables curl dnsutils jq python3 isc-dhcp-client procps >/dev/null 2>&1
modprobe kvm_amd 2>/dev/null || modprobe kvm_intel 2>/dev/null || true
[ -e /dev/kvm ] || mknod /dev/kvm c 10 232
ACCEL=kvm; qemu-system-x86_64 -accel kvm -M none -display none -S -daemonize -pidfile /tmp/k.pid 2>/dev/null && kill "$(cat /tmp/k.pid)" || ACCEL=tcg

# --- firewall disk: install once per ISO (cached), then a fresh overlay per run
BASE=/work/net/base-$(basename "$ISO" .iso).qcow2
if [ ! -f "$BASE" ]; then
	log "installing the firewall from $(basename "$ISO") ($ACCEL, ~6 min)"
	rm -f /work/net/base-*.qcow2
	qemu-img create -f qcow2 "$BASE.tmp" 12G >/dev/null
	timeout 3600 qemu-system-x86_64 -machine q35 -accel $ACCEL -cpu max -m 2048 -smp 2 -display none -no-reboot \
		-device ahci,id=ahci -drive file="$BASE.tmp",if=none,id=hd -device ide-hd,drive=hd,bus=ahci.0 \
		-netdev user,id=n0 -device virtio-net-pci,netdev=n0 -netdev user,id=n1,restrict=on -device virtio-net-pci,netdev=n1 \
		-drive file="$ISO",media=cdrom,if=none,id=cd,readonly=on -device ide-cd,drive=cd,bus=ahci.1 -boot order=dc \
		-serial file:"$A/install.log"
	mv "$BASE.tmp" "$BASE"
fi
rm -f run.qcow2; qemu-img create -f qcow2 -b "$BASE" -F qcow2 run.qcow2 >/dev/null

# --- networks
log "building the networks"
sysctl -qw net.ipv4.ip_forward=1
for br in br-wan br-lan; do ip link add $br type bridge; ip link set $br up; done
for t in tap-wan tap-lan; do ip tuntap add $t mode tap; done
ip link set tap-wan master br-wan up; ip link set tap-lan master br-lan up
# Docker's bridge netfilter would run bridged frames through iptables
sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null || true

ip netns add inet
ip link add inet-br type veth peer name inet0; ip link set inet0 netns inet; ip link set inet-br master br-wan up
ip link add up-root type veth peer name up-inet; ip link set up-inet netns inet
ip addr add 10.99.0.1/30 dev up-root; ip link set up-root up
ip route add 203.0.113.0/24 via 10.99.0.2
iptables -t nat -A POSTROUTING -s 10.99.0.0/30 -j MASQUERADE
iptables -t nat -A POSTROUTING -s 203.0.113.0/24 ! -d 203.0.113.0/24 -j MASQUERADE
iptables -P FORWARD ACCEPT
IN="ip netns exec inet"
$IN ip link set lo up
$IN ip addr add 203.0.113.1/24 dev inet0
for a in 80 81 82; do $IN ip addr add 203.0.113.$a/24 dev inet0; done
$IN ip link set inet0 up
$IN ip addr add 10.99.0.2/30 dev up-inet; $IN ip link set up-inet up
$IN ip route add default via 10.99.0.1
$IN sysctl -qw net.ipv4.ip_forward=1
UPSTREAM=$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)
$IN dnsmasq --interface=inet0 --bind-interfaces --except-interface=lo \
	--dhcp-range=203.0.113.100,203.0.113.150,1h --dhcp-option=3,203.0.113.1 --dhcp-option=6,203.0.113.1 \
	--address=/www.test.example/203.0.113.80 --address=/bad.test.example/203.0.113.80 --address=/blocked.test.example/203.0.113.81 \
	--no-resolv --server="${UPSTREAM:-8.8.8.8}" --log-queries --log-dhcp --log-facility="$A/dnsmasq.log" \
	--dhcp-leasefile=/tmp/inet.leases --pid-file=/tmp/dnsmasq.pid
for a in 80 81 82; do $IN python3 /s/net-web.py 203.0.113.$a 80 internet-$a 2>>"$A/inet-web.log" & done

ip netns add client
mkdir -p /etc/netns/client; : > /etc/netns/client/resolv.conf
ip link add client-br type veth peer name lan0; ip link set lan0 netns client; ip link set client-br master br-lan up
CL="ip netns exec client"
$CL ip link set lo up; $CL ip link set lan0 up

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
	pkill -f net-web.py 2>/dev/null
	pkill dhclient 2>/dev/null
}
trap cleanup EXIT

# The LAN client gets a static address first to reach the setup API, then DHCP.
$CL ip addr add 192.168.1.250/24 dev lan0
BOX=https://192.168.1.1
retry 420 $CL curl -sk -m 5 -o /dev/null "$BOX/api/v1/setup/state" || { log "the API never came up"; tail -40 "$A/boot.log"; exit 1; }
log "API up after boot"

PASS='Goblin-Test-Passw0rd!'
$CL curl -sk -m 120 -H 'Content-Type: application/json' -d "{\"admin_password\":\"$PASS\",\"wan\":{\"device\":\"eth0\",\"mode\":\"dhcp\"},\"lan\":{\"device\":\"eth1\",\"address\":\"192.168.1.1/24\",\"dhcp_start\":\"192.168.1.100\",\"dhcp_end\":\"192.168.1.199\"},\"security_level\":\"off\"}" \
	"$BOX/api/v1/setup" | tee "$A/setup.json"; echo
CSRF=$($CL curl -sk -m 30 -c /tmp/jar -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$PASS\"}" "$BOX/api/v1/auth/login" | jq -r .csrf_token)
TOKEN=$($CL curl -sk -m 30 -b /tmp/jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{"name":"net-test"}' "$BOX/api/v1/auth/tokens" | jq -r .token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || { log "could not get an API token"; exit 1; }
api() { # api <METHOD> <path> [json body]
	$CL curl -sk -m 120 -X "$1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' ${3:+-d "$3"} "$BOX$2"
}

# --- DHCP from the box
$CL ip addr flush dev lan0
retry 60 $CL timeout 30 dhclient -1 -v -pf /tmp/dhclient.pid -lf /tmp/dhclient.leases lan0 >"$A/dhclient.log" 2>&1
CLIENT_IP=$($CL ip -4 -o addr show lan0 | awk '{print $4}' | cut -d/ -f1)
log "client address: ${CLIENT_IP:-none}"
check dhcp-lease sh -c "echo '$CLIENT_IP' | grep -Eq '^192\.168\.1\.(1[0-9][0-9])$'"
$CL python3 /s/net-web.py 0.0.0.0 80 lan-client 2>>"$A/client-web.log" &

FW_WAN=$(sed -n 's/.*DHCPACK(inet0) \([0-9.]*\) 52:54:00:00:01:01.*/\1/p' "$A/dnsmasq.log" | tail -1)
log "firewall WAN address: ${FW_WAN:-none}"

# --- one commit with everything under test
log "configuring and committing"
patch() { api PATCH /api/v1/config/candidate "{\"path\":\"$1\",\"value\":$2}" | jq -c '.validation_errors' ; }
patch dns '{"forwarders":["203.0.113.1"],"dnssec":false}'
patch addresses '[{"name":"blocked-host","type":"host","value":"203.0.113.81"}]'
patch feeds '[{"name":"testfeed","type":"ip","url":"http://203.0.113.80/feed.txt","refresh_minutes":15,"enabled":true}]'
patch profiles.dnsfilter '[{"name":"test","block_domains":["bad.test.example"]}]'
patch profiles.ips '[{"name":"test","mode":"detect"}]'
patch nat.port_forwards "[{\"name\":\"web\",\"in_intf\":\"wan1\",\"ext_port\":\"8080\",\"protocol\":\"tcp\",\"to_addr\":\"$CLIENT_IP\",\"to_port\":\"80\"}]"
patch policies '[
 {"id":2,"name":"deny-blocked","enabled":true,"src_intf":["lan"],"dst_intf":["wan1"],"src_addr":["all"],"dst_addr":["blocked-host"],"service":["all"],"action":"deny","log":"all"},
 {"id":1,"name":"lan-to-internet","enabled":true,"src_intf":["lan"],"dst_intf":["wan1"],"src_addr":["all"],"dst_addr":["all"],"service":["all"],"action":"accept","nat":true,"log":"all","dnsfilter_profile":"test","ips_profile":"test","threat_feeds":["testfeed"]}]' | tail -1
api POST /api/v1/commit '{"comment":"net-test"}' | tee "$A/commit.json"; echo

# --- checks
D="$CL dig +time=3 +tries=2 @192.168.1.1"
check dns retry 60 sh -c "$D +short www.test.example | grep -qx 203.0.113.80"
check nat-out retry 60 sh -c "$CL curl -s -m 5 http://203.0.113.80/whoami | grep -q 'sees ${FW_WAN:-x}\$'"
check port-forward retry 60 sh -c "$IN curl -s -m 5 http://${FW_WAN:-x}:8080/ | grep -q 'hello from lan-client'"
check deny-policy sh -c "! $CL curl -s -m 5 http://203.0.113.81/"
check deny-logged retry 90 sh -c "$CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' '$BOX/api/v1/logs?kind=traffic&action=deny&dst_ip=203.0.113.81' | jq -e '.events | length > 0'"
check dnsfilter-block retry 60 sh -c "$D bad.test.example | grep -q 'status: NXDOMAIN'"
check feed-loaded retry 180 sh -c "$CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/feeds | jq -e '.feeds[] | select(.name == \"testfeed\") | .count >= 1'"
check feed-block sh -c "sleep 5; ! $CL curl -s -m 5 http://203.0.113.82/"
# IPS: suricata-update downloads ET Open through the WAN, then Suricata starts;
# keep fetching the test page until an alert is logged.
check ips-alert retry 900 sh -c "$CL curl -s -m 5 -o /dev/null http://203.0.113.80/id.txt; $CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' '$BOX/api/v1/logs?kind=ips' | jq -e '[.events[] | select(.signature // \"\" | test(\"id check returned root\"))] | length > 0'"

# --- artifacts
for p in /api/v1/system/status /api/v1/feeds "/api/v1/logs?limit=200" /api/v1/config/running /api/v1/stats/policies; do
	f=$(echo "$p" | tr '/?=&' '____'); api GET "$p" >"$A/api$f.json" 2>&1
done
cp /tmp/inet.leases "$A/" 2>/dev/null

if [ $FAILS -eq 0 ]; then log "ALL PASS"; else log "$FAILS FAILED"; exit 1; fi
