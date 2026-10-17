# Multi-WAN and routing

All under **Network**.

## Gateways

A gateway is the next hop of a WAN (from DHCP, or a fixed address). Each is **monitored**: the box pings a
monitor IP (for example `1.1.1.1`) every few seconds and marks the gateway down above a loss or latency
limit you set. The dashboard and **Network → Gateways** show status, latency and loss; notifications can
tell you on `wan_down` / `wan_up`.

## Gateway groups: failover and load balancing

A group lists gateways with a **tier** and a **weight**:

- Tier 1 is used first; tier 2 only when every tier-1 gateway is down (**failover**).
- Within a tier, connections are spread by weight (**load balancing**): weight 3 and 1 send about 75% / 25%.

## Policy routing

Set a policy's **gateway** to a gateway, a group or a VPN provider: the traffic that policy accepts leaves
that way. Example: the TV VLAN through the cable WAN, everything else through the fibre WAN with the cable
WAN as failover.

## Static routes

**Network → Static Routes**: a destination subnet via a gateway, for networks behind another router (a lab
router, a site-to-site link).
