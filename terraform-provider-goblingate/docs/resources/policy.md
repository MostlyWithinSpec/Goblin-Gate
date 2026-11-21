---
page_title: "goblingate_policy Resource"
description: |-
  A firewall policy (Policy & Objects → Firewall Policy). New policies are added at the end of the list, and the gate checks policies from the top: if one must come before an existing policy, move it in the web UI (the order is kept on later applies).
---

# goblingate_policy (Resource)

A firewall policy (Policy & Objects → Firewall Policy). New policies are added at the end of the list, and the gate checks policies from the top: if one must come before an existing policy, move it in the web UI (the order is kept on later applies).

## Example Usage

```terraform
resource "goblingate_policy" "iot_plex" {
  name     = "iot-to-plex"
  src_intf = ["iot"]
  dst_intf = ["lan"]
  dst_addr = [goblingate_address.nas.name]
  service  = [goblingate_service.plex.name]
  action   = "accept"
  log      = "all"
}
```

## Schema

### Required

- `name` (String) The policy name.
- `src_intf` (Set of String) Interfaces, VLANs or zones the traffic comes from.
- `dst_intf` (Set of String) Interfaces, VLANs or zones the traffic goes to.
- `action` (String) `accept` or `deny`.

### Optional

- `policy_id` (Number) The policy's number. Default: the highest in use plus one. Changing it replaces the policy.
- `enabled` (Boolean) Whether the policy is on. Default: `true`.
- `src_addr` (Set of String) Source address or group names. Default: `["all"]`.
- `dst_addr` (Set of String) Destination address or group names. Default: `["all"]`.
- `service` (Set of String) Service names. Default: `["any"]`.
- `nat` (Boolean) Masquerade the source address (traffic to the internet). Default: `false`.
- `log` (String) `off`, `all` or `deny`.
- `schedule` (String) A schedule name; unset = always.
- `ips_profile` (String) An IPS profile name.
- `webfilter_profile` (String) A web filter profile name.
- `dnsfilter_profile` (String) A DNS filter profile name.
- `appctrl_profile` (String) An application control profile name.
- `threat_feeds` (Set of String) Threat feed names to block in this policy.
- `gateway` (String) Send accepted traffic through this gateway or gateway group; unset = the default route.

### Read-Only

- `id` (String) The ID: the policy ID.

## Import

Import an object that already exists on the gate with the policy ID:

```shell
terraform import goblingate_policy.example 12
```
