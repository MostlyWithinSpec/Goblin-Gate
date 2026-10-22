# The public demo

`goblind --demo` runs a showcase anyone can click through: a believable home network (LAN, IoT and guest
VLANs, a NAS port forward limited to two countries, a kids' bedtime, IPS and DNS filter profiles), a device
inventory, and synthetic traffic, IPS and web filter events so the dashboards and logs have something to
show. Visitors can edit, see the diff, commit and roll back; the applier is fake, so nothing reaches a
network.

## What demo mode does

- Implies `--dev`: fake applier, no login, plain HTTP (put TLS in front).
- A fresh config directory in the temp dir on every start, seeded and removed on exit.
- Exits after `--demo-reset` (default `1h`); the service manager starts it again from the seed.
- **No outside connections.** Background workers see the running config without threat feeds,
  notification channels, dynamic DNS, log export, config history pushes, CrowdSec or a UPS server, and Go's
  default HTTP transport refuses every connection besides. Visitors can still configure those features;
  nothing is sent.
- **Refused (403):** admins, API tokens, passwords, two-factor, ending sessions, restoring backups, the
  pfSense/OPNsense import (an upload would be visible to the next visitor), updates, plugins, packet capture,
  ping/traceroute/DNS lookups, Wake-on-LAN, pushing config history, test notifications, feed/GeoIP/DDNS
  refreshes, Tailscale login and WireGuard client keys. The list is `demoDenied` in `cmd/goblind/demo.go`.
- `GET /api/v1/system/status` says `"demo": true` and the UI shows a banner.

## Hosting

On a small Debian VM with the `goblin-gate` package installed but **goblind itself disabled** (the demo host
is not a firewall): copy `packaging/demo/goblin-demo.service` to `/etc/systemd/system/`, enable it, and put a
reverse proxy with TLS in front of `127.0.0.1:8090`. Behind Cloudflare, add a rate limit (for example 120
requests per minute per IP) so one visitor can't make the demo slow for everyone; every visitor shares the
same state until the next reset.
