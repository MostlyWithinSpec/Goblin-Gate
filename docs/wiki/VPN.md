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
