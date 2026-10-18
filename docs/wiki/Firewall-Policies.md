# Firewall policies

**Policy & Objects → Firewall Policy.** Policies decide which traffic may pass *through* the box between
networks (LAN to internet, Wi-Fi VLAN to LAN, ...).

## How matching works

- Policies are checked **top to bottom; the first one that matches wins**. Drag rows by the grip on the left
  to reorder them.
- Traffic that matches no policy is **denied**.
- A policy matches when all of these match: source interface, destination interface, source address,
  destination address and service. `all` (addresses, interfaces) and `any` (services) match everything.
- Replies to an accepted connection are allowed automatically: you only write the policy for the side that
  starts the connection.

**Traffic to the firewall itself** (its web UI, SSH, ping) is *not* controlled by policies. Each interface
has **allowed access** (Network → Interfaces): `https`, `ssh`, `ping`. DNS and DHCP are opened
automatically on networks the box serves. So a policy "Wi-Fi → 192.168.1.1" does not let Wi-Fi clients
reach the web UI; adding `https` to the Wi-Fi VLAN's allowed access does.

## A policy's fields

| Field | Meaning |
|---|---|
| From → To | Source and destination interfaces or VLANs (`all` = any) |
| Source / Destination | Addresses or address groups (see [[Objects]]) |
| Service | Services or service groups (`any` = all ports and protocols) |
| Action | ACCEPT or DENY |
| NAT | Masquerade behind the WAN address. Only applies to traffic that **leaves through a WAN**, so it is harmless on LAN-to-LAN policies. Internet policies need it. |
| Log | `off`, `all`, or `deny` (only denied traffic). Logged traffic shows in Log & Report. |
| Schedule | Only active during a schedule (Policy & Objects → Schedules) |
| Security | IPS, web filter, DNS filter and app control profiles, threat feeds (see the Security pages) |
| Gateway | Send this traffic out a specific WAN or VPN provider ([[Multi-WAN and Routing]]) |

The **Hits** column counts connections per policy since the last commit; a policy stuck at 0 may be
shadowed by one above it.

## Adding, editing, deleting

- **New policy** (top right) adds one at the end. **Insert above / below** from the row menu puts it in
  place.
- Click a policy's **name** to edit it.
- The **⋮** button at the right end of each row (or right-click the row) has **Clone, Insert above, Insert
  below, Disable/Enable, Delete**. The table scrolls sideways on narrow screens; the ⋮ column stays visible.
- The **On** checkbox disables a policy without deleting it.
- Remember to **commit** ([[How Changes Work]]).

## Recipes

**Guest/IoT VLAN with internet only, plus one server on the LAN**

1. `iot → all, source iot-net, destination ha-server, service any, ACCEPT` (exceptions first)
2. `iot → all, source iot-net, destination lan-net, service any, DENY, log deny` (block the rest of the LAN)
3. `iot → all, source iot-net, destination all, service any, ACCEPT, NAT` (internet)

The LAN can still reach the IoT devices (for casting, for example) if a LAN policy allows it: replies flow
back automatically.

**See what a deny policy blocks**: set its Log to `deny`, then filter Log & Report by that policy's ID.

**Time limits for a device**: create a schedule, then a DENY policy with that schedule above the allow
policy.

## GeoIP country policies

Country address objects match traffic by country. Create an **Address** of type *Country* (Network →
Addresses), pick one or more countries, and use it like any other address: in policies, address groups and
a port forward's allowed sources.

The country database ([DB-IP](https://db-ip.com) Lite, CC BY 4.0) is downloaded the first time a commit uses
a country, then refreshed monthly. Its status shows above the address list. If it cannot be downloaded, the
commit is refused rather than leaving country rules empty.

**Only some countries may reach a port forward** (e.g. your Plex server from the US and Canada):

1. Create a country address `home-countries` with United States and Canada.
2. Edit the port forward (Network → Port Forwards) and tick `home-countries` under **Allowed sources**.

Connections from anywhere else are dropped.

**Block a country from the LAN** (outbound): a DENY policy `lan → wan, destination <country object>` above
your internet policy.

**Caveat**: country detection is based on the IP's registered allocation. A VPN or mobile-phone carrier
can make a connection appear to come from a different country than the user's physical location. Treat
country rules as a coarse filter, not a precise geolocation check.
