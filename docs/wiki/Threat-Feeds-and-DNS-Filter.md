# Threat feeds and DNS filter

Two ways to block known-bad destinations:

- **IP threat feeds** block addresses in the firewall (fast, works for any protocol).
- **DNS filtering** answers "does not exist" for bad domain names (ads, malware, phishing, trackers).

## Threat feeds

**Security Profiles → Threat Feeds.** A feed is a list downloaded on a schedule:

| Field | Meaning |
|---|---|
| Type | `ip` (addresses and networks), `domain` (names, for DNS filter profiles) or `url` |
| URL | where to download it (http/https) |
| Format | `plain` (one entry per line; `#`/`;` comments; hosts files work for domain feeds), `csv:<column>`, `json:<path>` |
| Refresh | minutes between downloads (at least 15) |
| Block on WAN | drop the feed's addresses arriving on every WAN (ip feeds) |

An ip feed blocks outbound traffic to its addresses on every policy that lists it under **threat feeds**,
and inbound on WANs with **Block on WAN**.

A download that looks broken is **rejected and the last good copy kept**: empty, shrinks by more than 90%, or
grows more than 10x. The page shows each feed's entries and last error, and a **lookup** box tells you which
feed lists an address or domain.

### Good feeds to start with

| Feed | Type | URL |
|---|---|---|
| Spamhaus DROP | ip | `https://www.spamhaus.org/drop/drop.txt` |
| Spamhaus DROPv6 | ip | `https://www.spamhaus.org/drop/dropv6.txt` |
| Emerging Threats compromised hosts | ip | `https://rules.emergingthreats.net/blockrules/compromised-ips.txt` |
| Hagezi Threat Intelligence (medium) | domain | `https://raw.githubusercontent.com/hagezi/dns-blocklists/main/wildcard/tif.medium-onlydomains.txt` |
| Hagezi Pro (ads, trackers) | domain | `https://raw.githubusercontent.com/hagezi/dns-blocklists/main/wildcard/pro-onlydomains.txt` |

Avoid IP lists that include private ranges (`10.0.0.0/8`, `192.168.0.0/16`, ...) on internal policies: they
would block your own networks. Many older pfBlockerNG lists (hosts-file.net, malwaredomains, ...) are dead;
a feed with errors blocks nothing.

If a feed fails with *"this is a domain list"*, it is a hosts file or domain list set up as an ip feed:
change its type to `domain` and use it in a DNS filter profile.

## DNS filter

**Security Profiles → DNS Filter.** A profile combines:

- **Block feeds**: domain feeds (a name also blocks its subdomains).
- **Block domains** / **Allow domains**: your own lists; allow always wins.
- **Safe search**: force Google, Bing, DuckDuckGo and YouTube safe search.
- **Action**: `nxdomain` (the name "does not exist", default) or the block page.

Attach the profile to a policy (field *DNS filter*): it applies to the clients of that policy's source
interface. The first accept policy with a DNS filter profile per interface decides.

On filtered networks the box also:

- redirects DNS queries to other servers (port 53) to itself, so devices with hard-coded DNS are filtered;
- answers the "is this network filtered" checks of Firefox (DNS over HTTPS) and **iCloud Private Relay** so
  they turn themselves off. Private Relay will say it is not available on that network: that is expected.

Memory: a large list costs memory in the resolver (about 1 GB for ~850 000 domains). Watch the dashboard's
memory after adding big lists.

## CrowdSec

[CrowdSec](https://www.crowdsec.net) watches logs (web servers, SSH, your reverse proxy) on another machine,
decides which addresses are attacking, and shares a community blocklist. Goblin Gate can act as its
*bouncer*: every address CrowdSec bans is dropped at the firewall within seconds, and released when the ban
ends.

1. Install CrowdSec on a host or in a container on your network and note its Local API address (port 8080 by
   default, e.g. `http://192.168.1.20:8080`). If it listens only on 127.0.0.1, set `listen_uri` to
   `0.0.0.0:8080` in its `config.yaml`.
2. On that host, register the firewall: `cscli bouncers add goblin-gate`. Copy the key it prints.
3. In Goblin Gate, enable CrowdSec with the Local API URL and the key, then commit.

**Scope**: *WAN* drops banned addresses arriving from the internet (the usual choice). *All* also drops them on
internal interfaces and as destinations, so nothing on your network can reach them either.

The status shows whether the Local API answers, how many bans are active and how many packets they dropped.
Use HTTPS for a Local API on another network: the key is sent with every request.
