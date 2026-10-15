<p align="center">
  <img src="web/public/goblin-512.png" width="160" alt="Network Goblin">
</p>

<h1 align="center">Goblin Gate</h1>

<p align="center"><b>A goblin guards your front door, and you can watch everything it does.</b><br>
<i>An open-source firewall · Debian · nftables · Suricata · Unbound · Kea · WireGuard</i></p>

<p align="center">
  <a href="../../releases"><b>💿 Download the ISO</b></a> ·
  <a href="https://networkgoblin.dev/gate/"><b>🌐 Project page</b></a> ·
  <a href="../../discussions"><b>💬 Discussions</b></a> ·
  <a href="https://networkgoblin.dev/">🧌 Network Goblin Labs</a>
</p>

---

## What is this?

Goblin Gate is a firewall for people who want to **understand and automate their network**: homelabbers,
self-hosters, tinkerers, students learning networking, and small offices that want enterprise features without
enterprise licensing.

It's as easy as a home router (a setup wizard gets you online in minutes) and as capable as the expensive
boxes: policies with security profiles, intrusion prevention, a web filter, threat feeds, real dashboards, and
a config you can diff, commit and roll back. All of it is open, all of it has an API, and nothing is hidden.
Bring your own x86-64 box or a VM; there's no goblin hardware to buy.

> 🧪 **Alpha.** It installs, it routes, and the core features pass automated end-to-end tests on real VMs.
> It is still test software: run it in a VM or on a spare box, not as your only firewall yet. Found a rough
> edge? [Tell the goblin](../../issues/new/choose).

## The goblin's powers

| | |
|---|---|
| 🧱 **A proper firewall** | Interfaces and VLANs, address and service objects, ordered policies (first match wins, everything else is denied), NAT and port forwards. IPv4 and IPv6 alike. |
| 🧩 **Security profiles per policy** | Build an IPS, web filter, DNS filter or app control profile once, attach it to any policy. The kids' Wi-Fi and your lab VLAN can play by different rules. |
| 📜 **Config you can trust** | Changes pile up in a candidate config. See the diff, commit, and if the change locks you out, it **reverts itself**. Every commit is a revision you can roll back to, and backups are one click. |
| 🛡️ **Intrusion prevention** | Suricata inline with the ET Open rules: bad traffic gets blocked, not just written down. |
| 🚦 **Web and DNS filtering** | Block by category, filter DNS, and decide which apps get through (application control). |
| 🗑️ **Threat feeds** | Known-bad IP and domain lists, kept fresh automatically. |
| 🌐 **IPv6, done properly** | DHCPv6 with prefix delegation on the WAN; each LAN and VLAN gets its own /64 with router advertisements. |
| 🏠 **Network services** | DHCP (Kea) and a DNS resolver (Unbound) built in. |
| 🔐 **VPN** | WireGuard with a QR code per phone or laptop, and Tailscale. |
| 📊 **Eyes everywhere** | Logs you can drill into, dashboards built from widgets, and a chart builder for the questions nobody thought of. |
| 🔔 **It tells you things** | Email, webhooks, Discord, ntfy or Telegram when something happens. |
| 🔄 **Updates that undo themselves** | Signed packages in stable, beta and nightly channels. If an update doesn't come back healthy within 5 minutes, the box quietly reinstalls the previous version. |
| 🖥️ **Three ways in, one config** | A web UI (dark and light), a REST API for everything, and a CLI. They all edit the same config, so whatever you can click, you can script. |
| 💿 **Installs anywhere** | An offline installer ISO for bare metal (BIOS or UEFI, Secure Boot works), VMware ESXi, Proxmox and VirtualBox. A console menu handles the basics. |

## Summoning a goblin

1. Download the ISO from [Releases](../../releases) (or build it, below).
2. Boot it on a machine or VM with **two network ports** (one for WAN, one for LAN) and pick the disk.
3. On the console menu, assign the ports (and change the LAN address if it clashes with your network).
4. From a computer on the LAN port, open the web address the console shows and run the setup wizard.

More detail in [docs/README.md](docs/README.md), and the VMware ESXi guide in
[packaging/README.md](packaging/README.md).

## Talking to the goblin (CLI)

The `goblin` shell edits the same candidate config as the web UI and the API. Over SSH or the console:

```text
config system interface
  edit lan
    set address 10.0.0.1/24
  next
end
diff                  # what's about to change
commit confirm 5      # apply it, and roll back in 5 minutes unless you confirm
confirm               # still connected? keep it
```

Also there: `show`, `history`, `rollback N`, `discard`, `get system status`, `execute backup` and
`execute restore`. Type `help` for the full list.

## How it works

Everything goes through **goblind**'s REST API, the web UI included. Changes land in a candidate config; a
commit validates it, renders a config file for each engine (nftables, Suricata, Unbound, Kea and friends;
pure functions with golden-file tests), applies them, and rolls back if anything fails. More in
[docs/dev/conventions.md](docs/dev/conventions.md).

## Build from source

Requires Windows with PowerShell, Go 1.26+, Node.js 24+ and Docker Desktop (for the Linux builds and tests).

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1      # all checks and tests
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-deb.ps1   # the .deb package
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-iso.ps1   # the installer ISO
```

Linux and VM tests: `scripts/linux-test.ps1`, `scripts/linux-e2e.ps1`, `scripts/vm-install-test.ps1`,
`scripts/vm-net-test.ps1`.

## What's next

- **Stabilize:** daily use as a home firewall, more installs on real hardware, the first stable release.
- **Ready to switch:** multi-WAN failover, policy-based routing, the last settings into the web UI.
- **The tinkerer's firewall:** a **plugin system** anyone can build on, packet capture in the browser, a policy
  simulator ("would this reach that?"), git-backed config, Prometheus and Grafana, device inventory.
- **Enterprise for free:** high availability, BGP and OSPF, single sign-on, a captive portal.

The full roadmap lives on the [project page](https://networkgoblin.dev/gate/).

## Join the hoard

Bug reports, ideas and pull requests are all welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). Questions and
wild ideas go in [Discussions](../../discussions).

## License

GPL-3.0-only. See [LICENSE](LICENSE). The core stays free; no feature that's free today gets moved behind a
paywall later.
