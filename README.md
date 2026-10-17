# Goblin Gate

An open-source firewall by **Network Goblin Labs**: as easy as a home router, as capable as an
enterprise firewall. Debian, nftables, Suricata and Unbound underneath; a modern web UI, a REST API
and a FortiOS-style CLI on top. Built for homelabbers, tinkerers and anyone who wants to understand
and automate their network.

> **Status: early development.** The first builds install and run, and the core features pass automated
> end-to-end tests on real VMs. Expect rough edges, and please report them.

## Features

| Area | What you get |
| --- | --- |
| Firewall | Interfaces and VLANs, address and service objects, ordered policies, NAT, port forwards |
| Config you can trust | Candidate config, diff, commit with auto-revert, revision history and rollback, backups |
| Network services | DHCP (Kea), DNS resolver (Unbound) |
| Threat protection | Threat feeds, DNS filter, web filter by category, application control, IPS (Suricata with ET Open) |
| VPN | WireGuard with client QR codes, Tailscale |
| Visibility | Logs with drill-down, dashboards with widgets, a chart builder |
| Management | Web UI (dark and light), REST API, CLI, first-boot setup wizard, console menu |
| Plugins | Sandboxed plugins; Goblin Sentry turns IPS alerts into incidents with an approval queue, temporary blocks and a local decision model that learns your answers |
| Install | Offline installer ISO: bare metal (BIOS/UEFI, Secure Boot), VMware ESXi, Proxmox, VirtualBox |

## Install

1. Download the ISO from [Releases](../../releases) (or build it, below).
2. Boot it on a machine or VM with two network ports (WAN and LAN) and pick the disk.
3. On the console menu, assign the ports (and change the LAN address if it clashes with your network).
4. Open the web address the console shows from a computer on the LAN port and run the setup wizard.

Goblin Sentry is a separate package from the same APT repository: `apt install goblin-gate-plugin-sentry` on
the box, then enable it and approve what it asks for under System → Plugins.

Details: [docs/README.md](docs/README.md) and the VMware ESXi guide in
[packaging/README.md](packaging/README.md).

## Build from source

Requires Windows with PowerShell, Go 1.26+, Node.js 24+ and Docker Desktop (for Linux builds and tests).

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1      # all checks and tests
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-deb.ps1   # the .deb package
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-iso.ps1   # the installer ISO
```

Linux and VM tests: `scripts/linux-test.ps1`, `scripts/linux-e2e.ps1`, `scripts/vm-install-test.ps1`,
`scripts/vm-net-test.ps1`.

## How it works

Everything the UI does goes through goblind's REST API. Changes go to a candidate config; a commit
validates it, renders config files for each engine (pure functions, golden-file tested), applies them and
rolls back if anything fails. More in [docs/dev/conventions.md](docs/dev/conventions.md).

## Contributing

Bug reports, ideas and pull requests are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). A plugin
system for add-ins is on the roadmap.

## License

GPL-3.0-only. See [LICENSE](LICENSE).
