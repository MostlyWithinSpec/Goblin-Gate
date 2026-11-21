# Dynamic routing: BGP and OSPF

The box can exchange routes with other routers instead of you typing [static routes](Multi-WAN-and-Routing#static-routes):

- **OSPF** inside a site or a homelab: routers on the same networks find each other and learn every network
  behind each other, and reroute by themselves when a link goes down.
- **BGP** with a provider, a cloud (AWS, Azure, Hetzner), a lab or another site: you choose exactly what to
  announce and what to accept.

Both use [FRR](https://frrouting.org), which comes with the box. It runs only while BGP or OSPF is on.
Learned routes to networks are used at once. A learned **default route never takes your internet
traffic**: that stays with your gateways and [[Multi-WAN and Routing]].

Every commit checks FRR's configuration with FRR itself before it goes live, and applies it without
dropping the sessions that did not change.

## Router ID

Every router has a router ID that looks like an IPv4 address. The box takes the address of its first LAN
interface unless you set one under **Network → Routing**. It does not have to be one of the box's
addresses, but it must be unique among your routers.

## OSPF

**Network → Routing → OSPF**: turn it on and add an **area** (`0` is the backbone; a small network needs
only that one) with the interfaces OSPF should run on.

| Per interface | |
| --- | --- |
| Cost | Higher = less preferred. Leave empty for FRR's default (based on the link speed). |
| Passive | Announce the interface's network, but don't talk OSPF on it. Use it for networks with only clients on them (a LAN, a guest VLAN): nobody there can inject routes. |

**Redistribute connected / static** also announces the box's other networks and its static routes, without
running OSPF on them.

The interfaces need a static address. The firewall lets OSPF packets in on the non-passive interfaces only.

## BGP

**Network → Routing → BGP**: your **AS number** (for a private network, pick one from 64512-65534 or
4200000000-4294967294), and the **neighbours**:

| Per neighbour | |
| --- | --- |
| Address | The neighbour's IPv4 or IPv6 address |
| Remote AS | The neighbour's AS number: the same as yours for iBGP, another for eBGP |
| Password | TCP MD5 password, if the neighbour wants one (providers usually do) |
| Multihop | For a neighbour that isn't directly connected (across a tunnel, between loopbacks): the number of hops |
| Import | Which routes to accept from it, as a list of rules (below). Empty = everything. |
| Export | Which routes to announce to it. Empty = everything the box announces. |

**Announce** lists what the box announces: interfaces and VLANs (their network) and address objects of
type host or subnet ([[Objects]]). A prefix is only announced while the box has a route to it. **Redistribute
connected / static** announces all of them instead.

The firewall lets BGP (TCP 179) in from the listed neighbours only.

### Import and export rules

Each rule permits or denies a prefix, and with **max length** the smaller prefixes inside it up to that
length. The first matching rule wins; a route that matches no rule is refused.

Accept only private networks from a lab, up to /24:

| Action | Prefix | Max length |
| --- | --- | --- |
| permit | `10.0.0.0/8` | 24 |
| permit | `172.16.0.0/12` | 24 |
| permit | `192.168.0.0/16` | 24 |

Announce your networks to a provider, but never the management VLAN:

| Action | Prefix | Max length |
| --- | --- | --- |
| deny | `192.168.99.0/24` | |
| permit | `192.168.0.0/16` | 24 |

A list with rules for IPv4 only refuses all IPv6 routes, and the other way round.

## Status

**Network → Routing** shows each BGP neighbour (its state, how long it has been up, the prefixes received
and sent), each OSPF neighbour, and the routes learned. Over the API: `GET /api/v1/routing/status`.

| BGP state | Meaning |
| --- | --- |
| Established | The session is up. |
| Active, Connect | The box is trying to reach the neighbour: check the address, the neighbour's configuration, and that both sides use the same password. |
| Idle | The session is stopped, often right after an error. FRR tries again by itself. |

| OSPF state | Meaning |
| --- | --- |
| Full | The adjacency is up and the routes are exchanged. |
| 2-Way | Normal for two routers that are neither the DR nor the BDR on a shared network. |
| Init, ExStart | They see each other but don't agree: check the area, the MTU and the hello timers on both sides. |

Turning BGP and OSPF off stops FRR and removes the routes it learned.
