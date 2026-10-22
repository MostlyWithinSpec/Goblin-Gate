# Migrating from pfSense

## 1. Export from pfSense

Diagnostics → Backup & Restore → **Download configuration as XML**. Keep this file private: it holds password
hashes, keys and your whole network layout.

## 2. Import

Install Goblin Gate and run the setup wizard ([[First Setup]]), then **System → Import**:

1. Upload the XML.
2. **Assign ports**: pfSense device names (`em0`, `igb1`) do not exist here; pick which port of this box
   each pfSense interface uses. VLANs follow their parent.
3. Read the **report**. Every item is *converted*, *approximated* (converted with a difference the line
   explains) or *skipped* (with what to do instead). Lines marked as a risk deserve a second look.
4. The result is loaded into the **candidate**. Review the diff and commit when it looks right.

The importer never imports password hashes, private keys or certificates: you keep the admins you created
in the wizard, and VPN keys are created fresh.

### Coming from OPNsense

The same import reads an OPNsense backup (System → Configuration → Backups → **Download configuration**;
leave *encrypt* off). OPNsense keeps much of pfSense's format and adds newer sections; the importer reads
both:

- **Firewall rules** from either place (the older rule list and the newer one), in OPNsense's order.
  *Match* rules (tagging and shaping) have no counterpart and are listed as skipped.
- **Aliases**: hosts, networks, network groups and ports as objects; URL tables as threat feeds (a download
  username/password is not imported: add it to the feed as an auth header); **GeoIP** aliases become
  country objects. MAC, ASN and dynamic IPv6 aliases are skipped.
- **Outbound NAT**: hybrid and manual rules that translate to the interface address map to the policies'
  NAT switch; rules that translate to another address are approximated.
- **Gateways**, gateway groups and static routes (disabled ones stay out).
- **DHCP** from Kea, dnsmasq or the older ISC server, with static reservations.
- **Unbound**: DNSSEC, host overrides (A/AAAA), domain overrides and its forwarding servers (DNS over TLS
  servers are used over plain DNS for now).

Other OPNsense modules (IDS, WireGuard, the traffic shaper, plugins) are listed in the report so nothing is
lost silently. As with pfSense, password hashes, API keys, two-factor seeds, certificates and private keys
are never imported.

The OPNsense import is new. If something in your backup is converted wrongly, an issue with the report
line (and the matching part of the XML, without secrets) helps a lot.

## 3. What to check after the import

These came up on real migrations:

- **Port forwards.** Check each one. pfSense configs often carry old forwards; one forwarding *every* port to
  a PC exposes it completely. Keep only what you need; UPnP covers most games.
- **pfBlockerNG lists.** Many old lists are dead, and hosts files listed as IP lists cannot work as IP
  feeds. Look at **Security Profiles → Threat Feeds**: a feed with an error blocks nothing. Replace them with
  maintained lists (see [[Threat Feeds and DNS Filter]]).
- **Rules that never matched.** An "allow all" rule high up on an interface makes the rules below it
  pointless, in pfSense and here alike. The **Hits** column shows what matches. A good moment to clean up
  and, for Wi-Fi/IoT networks, to isolate them from the LAN (recipe in [[Firewall Policies]]).
- **Traffic to the firewall itself.** pfSense rules like "Wi-Fi → firewall address" become the interface's
  allowed access here (Network → Interfaces), not policies.
- **IPv6.** If pfSense tracked a WAN that had no IPv6, IPv6 stays off. Turn it on under Interfaces if your
  ISP provides it.
- **OpenVPN** is not supported: use WireGuard or Tailscale ([[VPN]]).
- **SSH**: add a key (System → Admins); password SSH logins are off.

## 4. Switch over

Keep the pfSense box or VM **powered off but ready** for the first week or two. If something goes wrong,
plug it back in, and tell us what broke.
