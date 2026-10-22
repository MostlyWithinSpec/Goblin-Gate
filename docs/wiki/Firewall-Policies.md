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

## Custom nftables rules

**Policy & Objects → Custom rules**. When a policy cannot express something, write
one nftables rule and have it placed in the generated ruleset as written.

Each rule has a **hook** (`input` for traffic to the box, `forward` for traffic through it, `prerouting`
and `postrouting` for NAT) and a **position**:

- **Start** — after the built-in safety rules (temporary blocks, established/related, loopback,
  anti-spoofing) and *before* everything the box generates. Use it when the rule must win over the policies.
- **End** — after all generated rules, just before the chain's default policy. Use it for a last resort,
  like logging what the policies dropped.

The **Hits** column shows the rule's own counter, refreshed every 30 seconds. **Check** saves the candidate
and runs `nft -c` on the whole rendered ruleset, so you see nft's own error under the rule before committing.
A commit that loads a bad ruleset is refused, and a commit with auto-rollback puts the previous ruleset back
on its own — still, keep a console open: a rule can block this web UI.

A rule stays one rule: no `;`, `#` or line break outside quotes, and no `table`, `chain`, `flush`, `delete`,
`include`, `jump` and friends, so a custom rule can never rebuild or wipe what the box generates.

**Open a port for a Prometheus exporter on a LAN interface** (scrapers the policy model does not cover):

```
iifname "lan" tcp dport 9100 accept
```

hook `input`, position **Start**. Replies go out automatically (established traffic is accepted earlier in
the chain). Restrict the source, do not open it on every interface:
`iifname "lan" ip saddr 10.20.0.5/32 tcp dport 9100 accept`.

**Log and drop a protocol** you never want on the network:

```
iifname "wan1" ip protocol gre log prefix "gre-dropped: " drop
```

hook `forward`, position **End** — the last thing that sees the packet, so the log line says "nothing else
wanted it either". `position: Start` if you want it dropped before the policies are consulted at all.

**When to use a policy instead**: almost always. A policy is ordered, named, visible in the policy table,
logged per policy, tied to schedules and security profiles, and understood by the policy simulator. A custom
rule is none of those: it bypasses the policy model, does not appear in the simulator's policy list, and
nobody reviewing the table will see it. Use one only for something policies genuinely cannot do — a protocol
or match nftables knows and the UI does not, a counter for your own debugging, a match on packet metadata the
fields do not cover. If you find yourself writing a rule that duplicates a policy, delete it and add the policy.
