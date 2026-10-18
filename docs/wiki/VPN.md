# VPN

## Tailscale (easiest remote access)

**VPN → Tailscale.** Turn it on, log in with the link shown, and the box joins your tailnet. Use it to reach
the web UI from anywhere. Options: **advertise routes** (offer chosen LAN/VLAN subnets to the tailnet;
approve them in the Tailscale admin console), **exit node** (tailnet devices can send all their traffic out
through your home), **accept routes** (use other nodes' subnets). Tailnet traffic is an interface
(`tailscale`) you can use in policies.

## WireGuard (your own VPN server)

**VPN → WireGuard.** Create a server (listen port, tunnel subnet), then add **peers**: each phone or laptop
gets a config and a **QR code** to scan with the WireGuard app. Remember a policy from the WireGuard
interface to what peers may reach. The server's private key is created on the box and stays there.

## VPN providers (route traffic through a commercial VPN)

**VPN → VPN Providers.** Add a WireGuard config from your provider (Mullvad, Proton, PIA's WireGuard, ...).
Then set a policy's **gateway** to that provider to send, for example, one VLAN or one device through it.
**Kill switch**: if the tunnel is down, that traffic is dropped instead of leaking out the normal WAN.

OpenVPN is not supported: use WireGuard (most providers offer it) or Tailscale.

## Dynamic DNS

If your internet connection has a changing public IP — most home and many small-business
lines do — clients cannot reach a WireGuard server by IP. A dynamic DNS (DDNS) entry
keeps a hostname pointed at your current WAN address, so the endpoint in your client
config can be `vpn.example.com` instead of an IP that changes.

Goblin Gate updates DDNS entries every five minutes (and immediately after a commit
that changes the WAN interface). Three providers are built in:

- **Cloudflare** — needs a hostname in one of your zones, a zone ID and an API token
  with *Zone → DNS → Edit* permission. Create the token at
  [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens)
  using the "Edit zone DNS" template. The zone ID is shown on the zone's overview page.
- **DuckDNS** — free, no account needed beyond a subdomain on duckdns.org; just paste
  the token.
- **Custom URL** — any `https://` endpoint that takes the new IP in the path or query
  string; the placeholder `{ip}` is replaced with the current WAN address. Use this for
  No-IP, DynDNS or a self-hosted updater.

The Network → Dynamic DNS page shows each entry's current IP, when it last updated and
any update error, plus an *Update now* button to force a refresh.
