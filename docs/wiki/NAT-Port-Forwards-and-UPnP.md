# NAT, port forwards and UPnP

## Outbound NAT

Internal networks reach the internet behind the WAN address when the policy that allows them has **NAT**
on ([[Firewall Policies]]). NAT only applies to traffic that leaves through a WAN, so turning it on for a
policy towards `all` does not translate LAN-to-LAN traffic.

## Port forwards

**Network → Port Forwards** sends connections that arrive on a WAN port to an internal host:

| Field | Example |
|---|---|
| Name | `plex` |
| Interface | the WAN that receives it (`wan`) |
| External port | `32400`, or a range `27015-27030` |
| Protocol | `tcp` or `udp` (add two forwards for both) |
| To address | `192.168.1.50` (IPv4) |
| To port | empty = same as external; a single port; or exactly the same range |

A port forward lets its traffic in by itself: no extra policy is needed.

**Forward only what you need.** A forward of every port (1-65535) exposes the whole host to the internet.
For games and consoles, UPnP below is usually the better answer; for remote access to your network, use a
[[VPN]] instead of forwarding admin ports.

## UPnP and NAT-PMP / PCP

**Network → UPnP** lets devices open forwards for themselves (game consoles, some apps):

- **Interfaces**: the internal networks allowed to ask. Enable it on the network your consoles and PCs are on,
  not on IoT or guest networks.
- **NAT-PMP / PCP**: the Apple/PCP protocols, on by default with UPnP.
- **Secure mode**: a device may only open forwards to itself (recommended).
- **Allow entries**: limit which addresses and ports may be requested.
- **Max lease**: how long a forward lives without renewal.
- **STUN**: find the public address when the WAN itself is behind another NAT (CGNAT, ISP router).

The page lists the current mappings with the device and port that asked for them.
