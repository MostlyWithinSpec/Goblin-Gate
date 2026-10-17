# DHCP and DNS

## DHCP

**Network → DHCP**: one DHCP server per internal interface or VLAN.

- **Range**: the addresses handed out (keep static devices outside it).
- **DNS**: the DNS servers given to clients. Leave it at the box's own address to get DNS filtering and
  local names.
- **Lease time**, **gateway** (default: the box).
- **Static leases**: pin a MAC address to an IP, with an optional hostname.

A network can also have no DHCP on the box (another DHCP server, or only static devices).

## DNS

**Network → DNS**. The box runs a validating resolver (Unbound) for its networks.

- **Forwarders**: send queries to upstream servers (for example `1.1.1.1`, `9.9.9.9`); empty = resolve from
  the root servers directly.
- **DNSSEC**: validate answers.
- **Local domain** (for example `home.arpa`): DHCP clients and static leases get names in it, so
  `nas.home.arpa` just works. Turn off "register DHCP client names" to only use static names.
- **Local records**: fixed names, for example `printer.home.arpa → 192.168.1.40`, or an override of a public
  name.
- **Domain forwards** (split DNS): send one domain to another server, for example `corp.example` to your
  work VPN's DNS server or `ad.home.arpa` to a domain controller.

Blocking ads and malware by name is the DNS filter: see [[Threat Feeds and DNS Filter]].
