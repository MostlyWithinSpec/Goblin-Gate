# Goblin Checks

Goblin Checks looks at your running configuration and the state of the box and lists what is risky or
forgotten, most important first. Each finding explains why it matters and links to the page where you fix
it. Nothing is changed for you.

Open it from **Goblin Checks** in the menu, or ask the API: `GET /api/v1/checks`.

## The score

The score starts at 100 and only real findings take points off: 20 for a high finding, 8 for a medium one,
3 for a low one. Information findings take nothing. A box with nothing to fix scores 100; there are no
points for features you happen to have turned on.

## Dismissing a finding

If a finding is a deliberate choice (you do want UPnP for the game console), dismiss it. It moves to the
*Dismissed* list and no longer counts against the score. Restore it from there at any time. Dismissing is not
a config change, so there is nothing to commit. Over the API: `POST /api/v1/checks/dismiss {"key": "upnp:on"}`,
and `DELETE /api/v1/checks/dismiss/upnp:on` to restore it (needs the `write:system` scope on a limited token).

A finding is matched by its key, e.g. `wan-admin:wan1:https` or `wan-accept:12` (the policy ID), so a
dismissal applies to that one thing only. Open the same service on another interface and it shows up again.

## The checks

| Check | Severity | What it looks for | How to fix it |
|---|---|---|---|
| Admin access from the internet | high | `https` or `ssh` in *allowed access* on a WAN interface (or a VLAN on one) | Turn it off; manage the box from outside over Tailscale or WireGuard |
| Port forwards open to everyone | medium, high for risky ports | a port forward with no allowed sources; high when it exposes SSH, Telnet, FTP, SMB, Remote Desktop, VNC or a database port | Limit it to your own addresses or countries, or use a VPN instead |
| UPnP | low | UPnP / NAT-PMP is on | Limit it to the networks that need it, or turn it off |
| Policies that let the internet in | high | an enabled accept policy from a WAN interface with any source, any destination and any service | Narrow it to the service and server that must be reachable |
| Inbound traffic without IPS | medium | an accept policy from a WAN interface without an IPS profile | Pick an IPS profile on the policy |
| Guest networks reaching your LAN | medium | an accept policy for every service from a `guest` interface to a `lan` or `mgmt` one | Allow only the service guests need |
| Guest networks without a DNS filter | low | a guest-to-internet policy without a DNS filter profile | Pick a DNS filter profile on the policy |
| Two-factor login | medium | an admin account without two-factor login | System → Admins → two-factor login |
| Config history off the box | low / medium | config history in git is off (low), its pushes fail (medium) or the last push is over 30 days old (low) | System → Backup |
| Automatic security updates | medium | Debian security updates set to manual | System → Updates |
| Pending restart | low | a kernel or library update waits for a restart | Restart at a quiet moment |
| Goblin Gate update | low | a newer Goblin Gate is available in your channel | System → Updates |
| Custom nftables rules | info | custom rules are active; Goblin Checks can't tell what they allow | Review them now and then |

Goblin Checks can't see everything: what a custom rule opens, what your devices themselves expose, or a weak
password. A high score is a good sign, not a guarantee.
