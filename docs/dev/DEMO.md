# The public demo

`goblind --demo` runs a showcase anyone can click through: a believable home network (LAN, IoT and guest
VLANs, a NAS port forward limited to two countries, a kids' bedtime, IPS and DNS filter profiles), a device
inventory, and synthetic traffic, IPS and web filter events so the dashboards and logs have something to
show. Visitors can edit, see the diff, commit and roll back; the applier is fake, so nothing reaches a
network.

## Try it on your own computer

Every release publishes the demo as a container for PCs and ARM machines (Apple Silicon Macs, Raspberry Pi):

```sh
docker run --rm -p 127.0.0.1:8090:8090 ghcr.io/mostlywithinspec/goblin-gate-demo
```

Open <http://localhost:8090>; Ctrl+C stops it and nothing is left behind. The image is built from
`packaging/demo/Dockerfile` by the `demo-image` job in `.github/workflows/release.yml`. On your own computer it
runs until you stop it (`--demo-reset 0`); a shared demo resets every hour.

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

Two ways, the same goblind underneath. Behind either, add a Cloudflare rate limit (for example 120 requests per
minute per IP) so one visitor can't make the demo slow for everyone: every visitor shares the same state until
the next reset.

### Docker and a Cloudflare Tunnel (recommended)

`packaging/demo/` builds the demo into a small container (about 10 MB: goblind and the web UI on a distroless
base, no shell) and runs it behind a Cloudflare Tunnel. Nothing is published on the host: `cloudflared` dials
out to Cloudflare, which serves `https://demo.networkgoblin.dev` with its own certificate. The demo container
sits on an internal Docker network with no route anywhere, read-only, as an unprivileged user with no
capabilities, capped at one CPU and 512 MB (it uses about 20 MB). Any Linux box with Docker will do: a small VPS,
or a VM or LXC in a homelab; it never needs to see your LAN.

1. Cloudflare dashboard → Zero Trust → Networks → Tunnels → **Create a tunnel** (Cloudflared), name it
   `goblin-demo`, and copy the token from the install command (the long string after `--token`).
2. In the tunnel, add a **public hostname**: `demo.networkgoblin.dev`, service type `HTTP`, URL `demo:8090`.
3. On the host:

   ```sh
   git clone https://github.com/MostlyWithinSpec/Goblin-Gate && cd Goblin-Gate/packaging/demo
   cp .env.example .env        # paste the token after TUNNEL_TOKEN=
   docker compose up -d --build
   ```

4. Open `https://demo.networkgoblin.dev`. To update the demo to a new release: `git pull && docker compose up -d --build`.

goblind exits every hour on purpose; Docker's restart policy starts it again from the seed.

### systemd on a Debian VM

On a small Debian VM with the `goblin-gate` package installed but **goblind itself disabled** (the demo host
is not a firewall): copy `packaging/demo/goblin-demo.service` to `/etc/systemd/system/`, enable it, and put a
reverse proxy with TLS (or `cloudflared`) in front of `127.0.0.1:8090`.
