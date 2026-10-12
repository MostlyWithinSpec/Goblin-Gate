# packaging

Systemd units, default config and the ISO build (added by later tasks).

| File | Installed to |
|---|---|
| `systemd/goblind.service` | `/etc/systemd/system/goblind.service` |
| `systemd/systemd-networkd-wait-online.service.d/goblin.conf` | `/etc/systemd/system/systemd-networkd-wait-online.service.d/goblin.conf` |
| `default/goblind.json` | `/etc/goblingate/goblind.json` |

The installer (task 038) must also install the packages the applier drives —
`systemd` (with `systemd-networkd` enabled), `udev`, `nftables`, `kea-dhcp4-server`
and `unbound` — and disable ifupdown (`/etc/network/interfaces`) so it doesn't
fight systemd-networkd. Keep Debian's `nftables.service` enabled but replace
`/etc/nftables.conf` with `include "/etc/nftables.d/*.nft"`: it runs before the
network comes up, so the last applied ruleset protects the box from boot until
goblind re-applies the running config. The applier does not need `procps` (it uses
`systemd-sysctl`). `scripts/linux-e2e.ps1` boots this setup in a container.

Logs: policies with logging send packets to NFLOG group 1; ulogd2 writes them to
`/var/log/ulog/goblin.json` and goblind tails that file and Suricata's
`/var/log/suricata/eve.json` into `data_dir/logs.db`. Rotate both files with
logrotate (`create`, not `copytruncate`, works best; goblind follows renames and
truncation).

IPS (task 031): Suricata 7 is not in Debian 12 main - install `suricata` and
`suricata-update` from **bookworm-backports**, and **disable and mask the stock
`suricata.service`** (goblind runs `goblin-suricata@<profile>` instances on NFQUEUE
instead). The kernel modules `nfnetlink_queue` and `nft_queue` load on demand.
goblind runs `suricata-update` daily into /var/lib/suricata/rules (ET Open) and
builds each profile's rules in /var/lib/suricata/goblin/ (`goblind ips-rules
<instance>`, also the unit's ExecStartPre). `scripts/linux-ips-test.ps1` tests it all
with live traffic.

Web filter (task 033): goblind itself listens on udp+tcp 5300 (DNS proxy for
web-filtered subnets; nft redirects their port 53 there) and tcp 80 (block page) -
nothing else on the box may use port 80. The category lists (UT1, CC BY-SA 4.0,
~20 MB download) are fetched weekly into data_dir/webfilter only while a web filter
profile uses categories. A custom block page can be installed at
/usr/share/goblin-gate/blockpage.html (Go html/template; fields .Domain, .Category,
.Profile, .Warn, .Token, .Match, .Expires; the warn form posts to
/__goblin/continue).

Tailscale (task 041): install `tailscale` from Tailscale's Debian repository
(https://pkgs.tailscale.com/stable/debian/bookworm) and enable `tailscaled.service`.
goblind drives it through its LocalAPI (/var/run/tailscale/tailscaled.sock) with
netfilter mode off - do not run `tailscale up` by hand with other flags. Logins are
done from the GUI/API (browser URL or a one-time auth key, which is not stored).

## Installer ISO (task 038)

`scripts/build-iso.ps1` builds `dist/goblin-gate-<version>-amd64.iso` (+ `.sha256`): a
customized Debian 12 installer (simple-cdd) carrying the goblin-gate .deb, Suricata from
bookworm-backports, Tailscale and open-vm-tools, so it installs offline. It asks only for the
target disk and a confirmation, then boots into the console menu on tty1 (status and web UI
address, assign WAN/LAN ports, reset the admin password, shell, reboot). BIOS and UEFI work;
with Secure Boot the installer and installed system boot through Debian's signed shim.
`scripts/vm-install-test.ps1` installs the `-Unattended` test image in QEMU/KVM (UEFI with
Secure Boot, and BIOS) and checks the first boot.

### VMware ESXi

1. Upload the ISO to a datastore. New VM: guest OS family Linux, version **Debian GNU/Linux
   12 (64-bit)**; 2 vCPU, 2 GB RAM (4 GB with IPS), 16 GB disk.
2. Two network adapters of type **VMXNET 3**: the first on the port group facing your modem
   (WAN), the second on your LAN port group. If the LAN side uses VLAN trunking, allow the
   VLANs on that port group (VLAN ID 4095) and add VLANs in Goblin Gate.
3. VM Options → Boot Options: **EFI**, and tick **Secure Boot** if you want it.
4. Boot the ISO, pick the disk, wait for the reboot, disconnect the ISO.
5. On the console choose **1) Assign interfaces**: the vmxnet3 ports are named like `ens192`
   (WAN) and `ens224` (LAN); the menu shows their MAC addresses to match ESXi's adapter list.
6. From a computer on the LAN port group open the web UI address the console shows
   (https://192.168.1.1/ by default) and run the setup wizard.

Running IPS (Suricata inline) or the web filter on ESXi needs nothing special; for the WAN
port group, promiscuous mode is not needed.
