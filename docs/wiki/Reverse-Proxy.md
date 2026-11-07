# Reverse proxy

**Network → Reverse Proxy** publishes web services from your network on public hostnames, with
certificates from [Let's Encrypt](https://letsencrypt.org/) that renew by themselves. Your Jellyfin,
Home Assistant or NAS becomes `https://media.example.com` without a port forward per service and without
setting up certificates on each box. It runs [Caddy](https://caddyserver.com/) (installed with Goblin Gate)
under a throwaway system user with no privileges.

## Before you start

- **A domain you control**, with a DNS name (an `A` record) for every service pointing at the gate's WAN
  address. If the WAN address changes, use your DNS provider's dynamic DNS.
- **A public IPv4 address on the WAN.** Behind CGNAT or an ISP router that does not forward ports 80 and 443
  to the gate, Let's Encrypt cannot reach the proxy and no certificate is issued.
- **Ports 80 and 443 on the WAN are free.** The proxy takes them, so the gate refuses the change while:
  - the admin page is allowed on a WAN (**HTTPS** in its allowed access) on port 443. Move the admin page to
    another port under **System → Settings**, or better, reach it over a [[VPN]].
  - a TCP port forward on a WAN covers port 80 or 443. Publish that service through the proxy instead.

IPv6 is not supported yet: the proxy answers on the WAN's IPv4 address.

## Publishing a service

Turn the proxy on, enter an **email** (Let's Encrypt sends expiry warnings there if renewing keeps
failing), and add a host per service:

| Field | Example |
|---|---|
| Name | `jellyfin` |
| Domains | `media.example.com` (one or more exact names; no wildcards) |
| Upstream | `http://192.168.1.10:8096`, or `https://nas.home.lan:5001` |
| Skip certificate check | for an `https` upstream with a self-signed certificate (many NAS boxes and hypervisors) |
| Allowed sources | address objects (host or subnet) or addresses such as `203.0.113.0/24`; empty = everyone |

After **Commit**, the proxy asks Let's Encrypt for a certificate for each domain. That usually takes a few
seconds; the page shows each certificate's expiry, or the error if it failed. Certificates renew about a
month before they expire.

The upstream must be another device: the gate refuses its own addresses (its admin page would be on the
internet).

**Allowed sources** answer everyone else with `403 Forbidden`. Use it for services only you should reach,
such as the office or your phone carrier's range, or keep the service LAN-only by allowing just your LAN
subnet.

## Testing a setup

Let's Encrypt limits how many certificates you can get per week. While you try things out, turn on
**Staging**: certificates come from Let's Encrypt's test CA, so browsers warn about them, but there are no
limits that matter. Turn it off when everything works and the proxy gets real certificates.

## From inside the network

With **LAN access** on (the default), devices on your LANs that open a public name reach the proxy too,
even though the name resolves to the WAN address. The admin page keeps working on the gate's LAN addresses
(`https://192.168.1.1`). Turn it off if you rather resolve the names to the services directly with
local DNS records ([[DHCP and DNS]]).

## How it works

Connections to ports 80 and 443 on a WAN are redirected to the proxy (it listens on 10080 and 10443, which
are not open by themselves). Port 80 answers with a redirect to HTTPS and handles Let's Encrypt's checks.
On 443 the proxy picks the host by name, checks the allowed sources and passes the request on, with the
client's address in `X-Forwarded-For`.

## Troubleshooting

| Symptom | Cause |
|---|---|
| "Timeout during connect" or "Connection refused" | Let's Encrypt cannot reach port 80/443: CGNAT, an ISP router in front, or the name points at another address |
| "DNS problem: NXDOMAIN" | the name has no DNS record (yet) |
| "too many certificates already issued" | the weekly limit: wait, and use **Staging** while testing |
| `502 Bad Gateway` | the upstream is down or the address/port is wrong; for `https` upstreams with a self-signed certificate, tick **Skip certificate check** |
| `403 Forbidden` | the client is not in **Allowed sources** |

The full log: `journalctl -u goblin-caddy` over SSH ([[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]).
