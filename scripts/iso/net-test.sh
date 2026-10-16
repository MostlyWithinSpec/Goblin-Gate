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
apt-get install -y -qq qemu-system-x86 qemu-utils kmod dnsmasq iproute2 iptables curl dnsutils jq python3 isc-dhcp-client procps radvd kea-dhcp6-server iputils-ping >/dev/null 2>&1
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
for br in br-wan br-lan br-wan2; do ip link add $br type bridge; ip link set $br up; done
for t in tap-wan tap-lan tap-wan2; do ip tuntap add $t mode tap; done
ip link set tap-wan master br-wan up; ip link set tap-lan master br-lan up; ip link set tap-wan2 master br-wan2 up
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

# IPv6 (task 045): the "internet" is also 2001:db8:0:1::/64 - radvd announces
# it (SLAAC + default route), Kea hands out /56 prefixes from
# 2001:db8:100::/40 by DHCPv6 prefix delegation, web servers on ::80, ::81.
for ns in "" inet; do ${ns:+ip netns exec $ns} sysctl -qw net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0; done
$IN sysctl -qw net.ipv6.conf.inet0.disable_ipv6=0
$IN sysctl -qw net.ipv6.conf.all.forwarding=1
for a in 1 80 81; do $IN ip -6 addr add 2001:db8:0:1::$a/64 dev inet0 nodad; done
cat > /tmp/radvd.conf <<'RADVD'
interface inet0 {
	AdvSendAdvert on;
	MinRtrAdvInterval 3;
	MaxRtrAdvInterval 10;
	prefix 2001:db8:0:1::/64 { AdvOnLink on; AdvAutonomous on; };
};
RADVD
$IN radvd -C /tmp/radvd.conf -p /tmp/radvd.pid -m logfile -l "$A/radvd.log"
mkdir -p /run/kea /var/lib/kea
cat > /tmp/kea6.json <<KEA6
{"Dhcp6": {
 "interfaces-config": {"interfaces": ["inet0"], "service-sockets-max-retries": 30, "service-sockets-retry-wait-time": 2000},
 "lease-database": {"type": "memfile", "name": "/tmp/kea6.csv", "lfc-interval": 0},
 "subnet6": [{"id": 1, "subnet": "2001:db8:0:1::/64", "interface": "inet0",
   "pools": [{"pool": "2001:db8:0:1::1000-2001:db8:0:1::1fff"}],
   "pd-pools": [{"prefix": "2001:db8:100::", "prefix-len": 40, "delegated-len": 56}]}],
 "loggers": [{"name": "kea-dhcp6", "output_options": [{"output": "$A/kea6.log"}], "severity": "INFO"}]}}
KEA6
# Kea binds the link-local address: wait until it has left DAD (tentative)
retry 30 sh -c "$IN ip -6 addr show dev inet0 scope link | grep fe80 | grep -qv tentative"
$IN kea-dhcp6 -c /tmp/kea6.json >"$A/kea6.out" 2>&1 &
for a in 80 81; do $IN python3 /s/net-web.py 2001:db8:0:1::$a 80 internet6-$a 2>>"$A/inet-web.log" & done

# Multi-WAN (task 049): a second "internet" on br-wan2 (198.51.100.0/24,
# the box is .2). 192.0.2.80 and .81 exist behind BOTH WANs, each answering
# with the WAN it is behind, so the reply shows which way the box sent it.
ip netns add inet2
IN2="ip netns exec inet2"
ip link add inet2-br type veth peer name inet20; ip link set inet20 netns inet2; ip link set inet2-br master br-wan2 up
$IN2 ip link set lo up
$IN2 ip addr add 198.51.100.1/24 dev inet20; $IN2 ip link set inet20 up
for ns in inet inet2; do
	for a in 80 81; do ip netns exec $ns ip addr add 192.0.2.$a/32 dev lo; done
done
for a in 80 81; do
	$IN python3 /s/net-web.py 192.0.2.$a 80 via-wan1 2>>"$A/inet-web.log" &
	$IN2 python3 /s/net-web.py 192.0.2.$a 80 via-wan2 2>>"$A/inet-web.log" &
done

ip netns add client
mkdir -p /etc/netns/client; : > /etc/netns/client/resolv.conf
ip link add client-br type veth peer name lan0; ip link set lan0 netns client; ip link set client-br master br-lan up
CL="ip netns exec client"
$CL sysctl -qw net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.default.accept_ra=1 net.ipv6.conf.lan0.disable_ipv6=0 net.ipv6.conf.lan0.accept_ra=1 net.ipv6.conf.lan0.autoconf=1
$CL ip link set lo up; $CL ip link set lan0 up

# --- boot the firewall: eth0 = WAN (br-wan), eth1 = LAN (br-lan)
log "booting the firewall"
qemu-system-x86_64 -machine q35 -accel $ACCEL -cpu max -m 3072 -smp 2 -display none \
	-device ahci,id=ahci -drive file=run.qcow2,if=none,id=hd -device ide-hd,drive=hd,bus=ahci.0 \
	-netdev tap,id=wan,ifname=tap-wan,script=no,downscript=no -device virtio-net-pci,netdev=wan,mac=52:54:00:00:01:01 \
	-netdev tap,id=lan,ifname=tap-lan,script=no,downscript=no -device virtio-net-pci,netdev=lan,mac=52:54:00:00:01:02 \
	-netdev tap,id=wan2,ifname=tap-wan2,script=no,downscript=no -device virtio-net-pci,netdev=wan2,mac=52:54:00:00:01:03 \
	-serial file:"$A/boot.log" -daemonize -pidfile /tmp/fw.pid

cleanup() {
	kill "$(cat /tmp/fw.pid 2>/dev/null)" 2>/dev/null
	kill "$(cat /tmp/dnsmasq.pid 2>/dev/null)" 2>/dev/null
	pkill -f net-web.py 2>/dev/null
	pkill dhclient 2>/dev/null
	kill "$(cat /tmp/radvd.pid 2>/dev/null)" 2>/dev/null
	pkill kea-dhcp6 2>/dev/null
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

fwwan() { sed -n 's/.*DHCPACK(inet0) \([0-9.]*\) 52:54:00:00:01:01.*/\1/p' "$A/dnsmasq.log" | tail -1; }
retry 60 bash -c "$(declare -f fwwan); A='$A'; fwwan | grep -q ."
FW_WAN=$(fwwan)
log "firewall WAN address: ${FW_WAN:-none}"

# --- one commit with everything under test
log "configuring and committing"
patch() { api PATCH /api/v1/config/candidate "{\"path\":\"$1\",\"value\":$2}" | jq -c '.validation_errors' ; }
patch dns '{"forwarders":["203.0.113.1"],"dnssec":false}'
patch addresses '[{"name":"blocked-host","type":"host","value":"203.0.113.81"},{"name":"blocked6","type":"host","value":"2001:db8:0:1::81"}]'
patch 'interfaces[name=wan1].ipv6' '{"mode":"dhcp6"}'
patch 'interfaces[name=lan].ipv6' '{"mode":"delegated"}'
patch feeds '[{"name":"testfeed","type":"ip","url":"http://203.0.113.80/feed.txt","refresh_minutes":15,"enabled":true}]'
patch profiles.dnsfilter '[{"name":"test","block_domains":["bad.test.example"]}]'
patch profiles.ips '[{"name":"test","mode":"detect"}]'
patch nat.port_forwards "[{\"name\":\"web\",\"in_intf\":\"wan1\",\"ext_port\":\"8080\",\"protocol\":\"tcp\",\"to_addr\":\"$CLIENT_IP\",\"to_port\":\"80\"}]"
patch policies '[
 {"id":2,"name":"deny-blocked","enabled":true,"src_intf":["lan"],"dst_intf":["wan1"],"src_addr":["all"],"dst_addr":["blocked-host","blocked6"],"service":["all"],"action":"deny","log":"all"},
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

# --- IPv6 (task 045): the WAN gets a /56 by prefix delegation, the LAN its
# first /64 (box = ::1), the client an address by SLAAC; traffic leaves
# without NAT, the deny policy holds for IPv6, nothing comes in unasked.
pd() { awk -F, '$7 == 2 {print $1 "/" $9}' /tmp/kea6.csv 2>/dev/null | tail -1; }
check v6-prefix-delegated retry 120 bash -c "$(declare -f pd); test -n \"\$(pd)\""
PD=$(pd); log "delegated prefix: ${PD:-none}"
if [ -n "$PD" ]; then
	# route the delegated prefix to the box's WAN link-local address
	$IN ping -6 -c 2 -W 1 -I inet0 ff02::1 >/dev/null 2>&1
	FW_LL=$($IN ip -6 neigh show dev inet0 | awk '/52:54:00:00:01:01/ && $1 ~ /^fe80/ {print $1; exit}')
	log "firewall WAN link-local: ${FW_LL:-none}"
	$IN ip -6 route replace "$PD" via "${FW_LL:-fe80::5054:ff:fe00:101}" dev inet0
	BOX6=$(python3 -c "import ipaddress,sys; print(ipaddress.ip_network(sys.argv[1])[1])" "$PD")
fi
c6() { $CL ip -6 -o addr show dev lan0 scope global | awk '{print $4}' | cut -d/ -f1 | grep -v '^fd' | head -1; }
check v6-client-slaac retry 90 bash -c "$(declare -f c6); CL='$CL'; c6 | grep -q ."
CLIENT6=$(c6); log "client IPv6 address: ${CLIENT6:-none}"
$CL python3 /s/net-web.py :: 8081 lan-client6 2>>"$A/client-web.log" &
check v6-out retry 60 sh -c "$CL curl -6 -s -m 5 'http://[2001:db8:0:1::80]/whoami' | grep -q 'sees ${CLIENT6:-x}\$'"
check v6-deny-policy sh -c "! $CL curl -6 -s -m 5 'http://[2001:db8:0:1::81]/'"
# the box is ::1 in the LAN /64; the LAN allows https (not ping) after setup
check v6-box-https retry 30 sh -c "$CL curl -6 -sk -m 5 -o /dev/null -w %{http_code} https://[${BOX6:-::1}]/api/v1/setup/state | grep -q 200"
# the LAN has a DNS filter: DNS over IPv6 is refused, clients use IPv4
check v6-dns-refused-on-filtered-lan sh -c "! $CL dig +time=2 +tries=1 @${BOX6:-::1} +short www.test.example | grep -q 203.0.113.80"
check v6-no-inbound sh -c "! $IN curl -6 -s -m 5 'http://[${CLIENT6:-::1}]:8081/'"
# IPS: suricata-update downloads ET Open through the WAN, then Suricata starts;
# keep fetching the test page until an alert is logged.
check ips-alert retry 900 sh -c "$CL curl -s -m 5 -o /dev/null http://203.0.113.80/id.txt; $CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' '$BOX/api/v1/logs?kind=ips' | jq -e '[.events[] | select(.signature // \"\" | test(\"id check returned root\"))] | length > 0'"

# --- multi-WAN (task 049): wan2 on eth2, failover group wan1 -> wan2 as the
# default route, 192.0.2.81 pinned to wan2 by a policy; then WAN 1's cable is
# pulled (its monitor stops answering) and plugged back in.
log "multi-WAN: second WAN, failover, a policy pinned to WAN 2"
cand() { api GET /api/v1/config/candidate; }
patch interfaces "$(cand | jq -c '.interfaces + [{"name":"wan2","device":"eth2","role":"wan","mode":"static","address":"198.51.100.2/24","enabled":true}]')"
patch gateways '[{"name":"wan1","interface":"wan1","address":"dhcp","monitor":{"ip":"203.0.113.1","interval_seconds":1}},{"name":"wan2","interface":"wan2","address":"198.51.100.1","monitor":{"ip":"198.51.100.1","interval_seconds":1}}]'
patch gateway_groups '[{"name":"failover","members":[{"gateway":"wan1","tier":1},{"gateway":"wan2","tier":2}]}]'
patch default_gateway '"failover"'
patch addresses "$(cand | jq -c '.addresses + [{"name":"pin-dest","type":"host","value":"192.0.2.81"}]')"
patch policies "$(cand | jq -c '[{"id":10,"name":"pin-to-wan2","enabled":true,"src_intf":["lan"],"dst_intf":["all"],"src_addr":["all"],"dst_addr":["pin-dest"],"service":["all"],"action":"accept","nat":true,"gateway":"wan2"}] + [.policies[] | .dst_intf = ["all"]]')"
api POST /api/v1/commit '{"comment":"multi-wan"}' | tee "$A/commit-multiwan.json"; echo
W="$CL curl -s -m 5"
GW="$CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' $BOX/api/v1/gateways/status"
check mw-default-via-wan1 retry 60 sh -c "$W http://192.0.2.80/ | grep -q 'hello from via-wan1'"
check mw-pinned-via-wan2 retry 60 sh -c "$W http://192.0.2.81/ | grep -q 'hello from via-wan2'"
check mw-both-up retry 30 sh -c "$GW | jq -e '[.gateways[] | select(.state == \"up\")] | length == 2'"
log "pulling WAN 1's cable"
ip link set inet-br down
check mw-failover-to-wan2 retry 90 sh -c "$W http://192.0.2.80/ | grep -q 'hello from via-wan2'"
check mw-wan1-reported-down retry 30 sh -c "$GW | jq -e '.gateways[] | select(.name == \"wan1\") | .state == \"down\"'"
api GET /api/v1/gateways/status >"$A/gateways-failed-over.json"
log "plugging WAN 1 back in"
ip link set inet-br up
check mw-failback-to-wan1 retry 120 sh -c "$W http://192.0.2.80/ | grep -q 'hello from via-wan1'"
check mw-wan-down-logged retry 30 sh -c "$CL curl -sk -m 10 -H 'Authorization: Bearer $TOKEN' '$BOX/api/v1/logs?kind=system' | jq -e '[.events[] | select(.action == \"wan_down\")] | length > 0'"
api GET /api/v1/gateways/status >"$A/gateways-final.json"

# --- artifacts
for p in /api/v1/system/status /api/v1/feeds "/api/v1/logs?limit=200" /api/v1/config/running /api/v1/stats/policies; do
	f=$(echo "$p" | tr '/?=&' '____'); api GET "$p" >"$A/api$f.json" 2>&1
done
cp /tmp/inet.leases "$A/" 2>/dev/null

if [ $FAILS -eq 0 ]; then log "ALL PASS"; else log "$FAILS FAILED"; exit 1; fi
