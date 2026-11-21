# Captive portal

A guest network can show a welcome page before a device gets online. Guests accept your terms, or they
enter a password or a voucher code. It runs on the box itself, with no cloud and no extra controller.

## Turn it on

**Network → Captive portal**: pick the interface or VLAN of the guest network (it needs a static IPv4
address, and usually a DHCP server) and a mode:

| Mode | What guests do |
| --- | --- |
| Click-through | Tick "I accept the terms" and connect. |
| Password | Enter one shared password (for example, the one on the fridge). |
| Vouchers | Enter a code from a batch you printed. Each code works for a set time and number of uses. |

| Setting | |
| --- | --- |
| Session length | How long a guest stays online (default 24 hours). A voucher's own time wins. |
| Idle timeout | Ends a session after this long without traffic (empty = never). |
| Bandwidth limit | Mbit/s per guest, download and upload. |
| Title, text, logo | The page. If there is a text, guests must accept it as the terms. |

Commit to apply. The page preview on the settings page shows what guests see.

## What a guest sees

Phones, tablets and laptops check for a portal when they join a network. With a portal on, that check is
answered by the portal, so the device opens the welcome page by itself. The network also announces the
portal through DHCP (RFC 8910) and the captive portal API (RFC 8908). If nothing pops up, opening any
`http://` site shows the page.

Until a guest is let in, the device reaches only DHCP, the box's DNS and the portal. Plain HTTP is
redirected to the portal. HTTPS is refused at once rather than intercepted, so guests never see a
certificate warning. The admin UI and SSH are not reachable from a guest that is not let in, whatever the
interface's admin access says.

Guests are recognised by their MAC and IP address. A guest that comes back with the same device within
its session stays online. Sessions survive a restart of the box.

## Vouchers

**Network → Captive portal → Vouchers**: make a batch (for example 20 codes, 4 hours each, one use per
code, valid for 30 days), then print the plain list. A code like `K7QM-2XTR` ignores case and dashes.
Revoking a batch or a single code also takes its guests offline.

## Guests

**Network → Captive portal → Guests** lists who is online, with the time left and the voucher used.
**Remove** takes a guest offline at once, open connections included. The guest sees the portal again.

## Good to know

- Use [policies](Firewall-Policies) to decide what guests may reach once let in, for example the internet
  but not your LAN. The portal only decides *whether* they get out.
- A device that randomises its MAC address per network keeps one address on your network, so that works.
  A device that changes it again later must log in again.
- IPv6 traffic from a guest that is not let in is blocked as well.
- The page is served on port 8880 of the box's address on the guest network, and only there.
