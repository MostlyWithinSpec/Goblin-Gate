# Objects: addresses, groups, services, schedules

Objects give a name to something you use in policies, so you change it in one place.

| Object | Where | Examples |
|---|---|---|
| Address | Network → Addresses | host `192.168.1.23`, subnet `192.168.2.0/24`, range `192.168.1.100-192.168.1.150`, FQDN `example.com`; IPv6 works the same way |
| Address group | Network → Address Groups | `kids-devices` = phone + tablet + console |
| Service | Network → Services | `tcp 443`, `udp 51820`, `tcp 8000-8100` |
| Service group | Network → Service Groups | `web` = http + https |
| Schedule | Policy & Objects → Schedules | weekdays 22:30-24:00 |

Each interface and VLAN with an address also gets an address object for its subnet automatically after
import (for example `lan-net`).

Names are 1-63 letters, digits, `.`, `_` or `-`.

## Live addresses

An address of type **live** has no addresses of its own: a plugin keeps them up to date, such as the VMs
and containers a homelab plugin finds on your hypervisor. Its value is the plugin's name, and that plugin
must have the `objects:write` capability approved (Plugins → the plugin → Enable). Use it in policies and
groups like any other address.

- The members change without a commit: the plugin sends them and the firewall uses them within a second.
  Every change is in the audit log (`live_object_update`).
- A new live address is empty and matches nothing until its plugin fills it. Renaming it or giving it to
  another plugin empties it again.
- The plugin may put in hosts and networks up to `/16` (IPv4) or `/48` (IPv6), 4096 at most; a plugin can
  keep at most 32 live addresses.
- The policy simulator matches live addresses against their current members.

A plugin decides who a policy on its live addresses matches, so use them in policies you would let it steer:
blocking or allowing its VMs is fine; an allow rule for your admin access is not.

## Deleting objects

The trash icon at the end of a row deletes an object after a confirmation. If a policy, a group or a profile
still uses it, the page refuses and says what uses it:

```
"Kids_Devices" is still used by policy 14 "wifi-rule-14", address group "family". Remove it there first.
```

Remove it from those first (or delete them), then delete the object.
