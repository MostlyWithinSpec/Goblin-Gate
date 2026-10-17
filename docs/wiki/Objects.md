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

## Deleting objects

The trash icon at the end of a row deletes an object after a confirmation. If a policy, a group or a profile
still uses it, the page refuses and says what uses it:

```
"Kids_Devices" is still used by policy 14 "wifi-rule-14", address group "family". Remove it there first.
```

Remove it from those first (or delete them), then delete the object.
