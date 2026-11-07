# Troubleshooting

## The commit button does nothing / the commit is refused

Open **Changes**: the errors are listed in red under the change list, each naming what is wrong, for example
`policy 14 "wifi-rule-14": unknown src_addr reference "Kids_Devices"`. Fix that item and commit again. The
API shows the same list: `POST /api/v1/config/validate`.

## I can't find how to delete something

- Policies: the **⋮** button at the right end of the row, or right-click the row → Delete.
- Objects, profiles, feeds: the trash icon at the end of the row. If it refuses, the message says what still
  uses the object.

## SSH says "Permission denied"

Password logins over SSH are always off. Add a key under System → Admins → SSH keys and commit (see
[[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]).

## A device can't reach something

1. **Log & Report**: filter by the device's IP. A denied connection shows which policy denied it (or none:
   no policy matched, so it was denied by default).
2. Give the deny policy `log: deny` if it is not logged yet.
3. Remember: policies match top to bottom, first match wins.
4. Traffic to the box itself (web UI, SSH) depends on the interface's allowed access, not policies.

## A website is blocked that shouldn't be

- Log & Report shows DNS filter, web filter and app control blocks with the list or category.
- Add the domain to the profile's **allow domains** (DNS filter or web filter), commit.
- The threat feed **lookup** box tells you which feed lists an address or domain.

## A threat feed shows an error

- `download: HTTP 404/403` or a timeout: the list is gone or blocks automated downloads. Remove it.
- `rejected: the update has no valid entries`: wrong type or format, or an empty list.
- `this is a domain list`: change the feed type to `domain` and use it in a DNS filter profile.
- `rejected: ... more than 90% fewer`: the download looked broken; the last good copy is still in use.

## Wi-Fi devices lost access after isolating the VLAN

Look for denies of your isolation policy in Log & Report (filter by its ID) and add an exception policy
above it for what the device needs (for example Wi-Fi → Home Assistant).

## iCloud Private Relay says it is not available

On networks with a DNS filter, the box asks Private Relay to turn off so the filter can't be bypassed. Remove
the DNS filter profile from that network's policy if you want Private Relay there.

## The box is unreachable after a change

If you committed with a confirmation window, wait: it rolls back on its own. Otherwise, from the console:
menu option 4 (shell), then `goblin -c "rollback <revision>"` with the last good revision (System →
Revisions lists them; `goblin -c "history"` on the shell).

## Diagnostics

System → Diagnostics runs checks from the box itself (admins only):

- **Ping**: reachability and latency to a host, optionally out of a specific interface or VLAN. Useful when
  one network works but another does not — ping the same host from each source interface.
- **Traceroute**: which hop stops answering on the way to a host. A `*` row is a hop that did not answer
  (often a router that blocks probes); look for where real hops end.
- **DNS lookup**: what the resolver returns for a name or IP (PTR). Ask "this firewall" to test Unbound, or
  an upstream IP to compare — different answers point at the box's DNS configuration.

At most two diagnostics run at once; if one is already busy you get "Another diagnostic is running; try
again in a moment." Traceroute can take up to a minute — the button stays disabled with a spinner until it
finishes.

### Throughput test

**Throughput test** measures how fast traffic gets between a computer and the box. Pick a LAN-side network
(an interface or VLAN with a static IPv4 address; never a WAN) and how many minutes to stay open (1–15), then
Start. The box runs an iperf3 server on that network's address, port 5201, open only on that network and
only while the test runs. On a computer in that network, run the command the page shows:

- `iperf3 -c <address>`: upload (computer → box)
- `iperf3 -c <address> -R`: download (box → computer)

Each run shows up in the results with its speed in Mbit/s (and retransmits, or loss and jitter for UDP).
Stop closes the port early. Use a wired computer: Wi-Fi usually measures the Wi-Fi, not the box. This tests
the box's own network stack, not routing between two networks.

### Network cards

**Network cards** lists each interface's driver, link speed and duplex, receive/send queues and offloads,
with hints when something is likely to slow it down: no link, a link slower than the card can do, half
duplex, offloads that are off, a slow driver, or a single receive queue on a fast link.

Offloads can be overridden per interface under Network → Interfaces → *Network card offloads (advanced)*
(each one: driver default, on or off). The defaults are right for almost every card; change them only to
work around a known problem, and run the throughput test before and after. Going back to the driver
default fully applies after the next restart.

## Reporting a bug

Open an issue with: the Goblin Gate version (System → About), what you did, what you expected, and the
relevant Log & Report lines or `journalctl -u goblind` output. Never post backups, tokens or config files
without removing secrets.
