# nftables ruleset (renderer `nft`)

Package `internal/render/nft` turns the config into one file,
`/etc/nftables.d/goblin.nft` (`nft.Path`). The applier loads it with `nft -f`.
Golden examples: `internal/render/nft/testdata/default.nft` and `full.nft`.

## Layout

Each table is declared empty, deleted, then defined in full. Inside one `nft -f`
transaction that replaces the table atomically. Other tables (Docker, etc.) are not touched.

**`table inet goblin`**

| Chain | Hook / policy | Contents |
|---|---|---|
| `input` | input, **drop** | established/related accept, invalid drop, `lo`, then per enabled interface (sorted by name): `allow_access` (ping → echo-request, https → `system.admin_port`, ssh → 22), DHCP client replies on `mode=dhcp`, DHCP server (udp 67) where `dhcp[]` has the interface, DNS (tcp/udp 53) on every non-wan interface and VLAN (VLANs inherit the parent's role) |
| `forward` | forward, **drop** | established/related accept, invalid drop, `jump threat_feeds`, port-forward accepts, then **policies in config order** |
| `output` | output, accept | invalid drop. The box's own traffic is trusted |
| `threat_feeds` | regular chain | empty; reserved for task 028 |

**`table ip goblin_nat`**: `prerouting` (dstnat) jumps to `dns_redirect` (empty; reserved for task 029),
then has one DNAT rule per port forward. `postrouting` (srcnat) masquerades packets that carry the masquerade mark.

## Policies

One rule per enabled policy:
`iifname … oifname … ip saddr … ip daddr … <l4> counter name "policy_<id>" [log prefix "GG:<id>:"] [marks] accept|drop comment "<name>"`

- `all`/`any` in an interface, address or service list means "no match on that field".
- One address object: `@addr_<name>` / `@addrgrp_<name>`. Several: a per-policy set `policy_<id>_src|dst` holding their union.
- Services are concatenated sets `inet_proto . inet_service` matched with `meta l4proto . th dport @set`.
  Port-less protocols (icmp, or tcp/udp with no ports) match with `meta l4proto`. If a policy mixes both, it gets
  **two rules that share the same counter**. That is the only case with more than one rule per policy.
- `log`: `all` logs every new connection the policy matches, `deny` logs only when the action is deny.
  Kernel log lines start with `GG:<id>:` (log ingest, task 023).
- Disabled policies render as a comment, but their counter is still declared.
- `schedule` is not supported yet. A policy with a schedule fails to render, so it never fails open.

## Names

Object names may contain `.` and `-`. In set and counter names these become `_`. When two names collide
(`lan-net` / `lan_net`), the later one in sorted order gets a `_2`, `_3`… suffix. Prefixes: `addr_`, `addrgrp_`,
`svc_`, `svcgrp_`, `feed_`, `portfwd_`, `policy_<id>_src|dst|svc`. Counters: `policy_<id>`, `portfwd_<name>`.

VLAN kernel devices are named `<parent device>.<vlan id>` (for example `eth1.20`). Use `render.VLANDevice` and
`render.Devices` so every renderer agrees.

## Marks (reserved bit layout)

| Mark | Bit | Meaning |
|---|---|---|
| meta mark | `0x00000001` | masquerade: set by an accept policy with `nat: true` in forward, read in `goblin_nat` postrouting |
| ct mark | `0x00000001` | the connection matched a policy with an `ips_profile`; Suricata queueing (task 031) keys on it |
| ct mark | `0x0000ff00` | reserved for per-profile ids (web filter / app control) |

The NAT decision uses a packet mark. That works because the policy rule only ever sees the first packet of a
connection (established traffic is accepted earlier), and that same packet is the one the NAT chain sees.

## Port forwards

`iifname <wan> <proto> dport <ext> dnat to <to_addr>[:<to_port>]`, plus a forward accept
`ip daddr <to_addr> <proto> dport <port> ct status dnat counter name "portfwd_<name>"`.
If `to_port` is empty or equal to `ext_port`, the port is kept. A single `to_port` maps the whole external
port or range onto that one port. Remapping one range onto a different range is rejected. Hairpin NAT is not done yet.

## Known limits and notes for later tasks

- **IPv4 only.** IPv6 address objects are rejected. With no address match, `all` rules in the inet table
  also match IPv6, but 008 only enables IPv4 forwarding.
- **FQDN addresses** render as empty sets, so accept rules that use them match nothing. A later task must resolve
  them and add the elements. Until then, a *deny* rule that uses an FQDN does not block it.
- **Counters and runtime set elements reset on every apply**, because the table is recreated.
  Stats (024) should treat a counter that goes down as a reset. The feed manager (028) must refill `feed_*`
  sets after every apply.
- Threat feed sets are declared only for enabled `type: ip` feeds. Policies that reference feeds are checked for
  existence only; the matching rules are task 028.
- Requires nftables ≥ 0.9.7 (comments on sets/counters) and kernel ≥ 5.6 (concatenated interval sets).
  Debian 12 (nft 1.0.6, kernel 6.1) is fine.

## Syntax check on a Linux VM

On Windows with Docker Desktop running, `scripts/linux-test.ps1` does all of this in a Debian 12 container
(all Go tests on Linux, `nft -c`, and a real load in the container's own network namespace).
On a Debian VM with nftables (as root):

```sh
sudo GOBLIN_NFT_CHECK=1 go test ./internal/render/nft -run TestNftSyntax -v
# or by hand, on any rendered file:
sudo nft -c -f internal/render/nft/testdata/full.nft
```

`nft -c` parses the file and validates it against the kernel without changing anything.
The integration harness (task 039) should set `GOBLIN_NFT_CHECK=1`.
