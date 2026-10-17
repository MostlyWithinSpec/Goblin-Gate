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
