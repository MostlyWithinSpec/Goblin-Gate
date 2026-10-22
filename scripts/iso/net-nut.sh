#!/bin/bash
# Runs ON the firewall VM (scripts/iso/net-test.sh feeds it over SSH): starts
# NUT through its real systemd units, inside Goblin Gate's sandbox drop-ins,
# with NUT's dummy driver standing in for a USB UPS, and checks that upsd
# answers and upsmon logs in. Leaves NUT off again. Prints "nut ok" on success.
set -u
umask 027
mkdir -p /run/nut-test
printf 'ups.status: OL\nbattery.charge: 100\nbattery.runtime: 1800\n' >/etc/nut/dummy.dev
printf 'MODE=standalone\n' >/etc/nut/nut.conf
printf '[ups]\n\tdriver = dummy-ups\n\tport = /etc/nut/dummy.dev\n\tdesc = "net test"\n' >/etc/nut/ups.conf
printf 'LISTEN 127.0.0.1 3493\n' >/etc/nut/upsd.conf
printf '[goblin-monitor]\n\tpassword = net-test-secret\n\tupsmon primary\n' >/etc/nut/upsd.users
printf 'MONITOR ups@localhost 1 goblin-monitor "net-test-secret" primary\nMINSUPPLIES 1\nSHUTDOWNCMD "/bin/true"\nPOWERDOWNFLAG /etc/killpower\n' >/etc/nut/upsmon.conf
chgrp nut /etc/nut/*.conf /etc/nut/upsd.users /etc/nut/dummy.dev
chmod 640 /etc/nut/*.conf /etc/nut/upsd.users /etc/nut/dummy.dev

systemctl restart nut-driver-enumerator.service 2>/dev/null
sleep 2
systemctl restart nut-server.service nut-monitor.service
ok=1
for i in $(seq 1 20); do
	if upsc ups@localhost ups.status 2>/dev/null | grep -q OL && upsc -c ups@localhost 2>/dev/null | grep -q 127.0.0.1; then
		ok=0
		break
	fi
	sleep 1
done
systemd-analyze security --no-pager nut-server nut-monitor 2>/dev/null | grep -i overall
if [ $ok -ne 0 ]; then
	journalctl -u nut-server -u nut-monitor -u 'nut-driver@*' -n 40 --no-pager
fi
systemctl stop nut-monitor.service nut-server.service 'nut-driver@*' 2>/dev/null
rm -f /etc/nut/nut.conf /etc/nut/ups.conf /etc/nut/upsd.conf /etc/nut/upsd.users /etc/nut/upsmon.conf /etc/nut/dummy.dev
[ $ok -eq 0 ] && echo "nut ok"
exit $ok
