---
page_title: "goblingate_vlan Resource"
description: |-
  A VLAN interface (Network → Interfaces). IPv6 settings made in the web UI are kept.
---

# goblingate_vlan (Resource)

A VLAN interface (Network → Interfaces). IPv6 settings made in the web UI are kept.

## Example Usage

```terraform
resource "goblingate_vlan" "iot" {
  name         = "iot"
  parent       = "lan"
  vlan_id      = 20
  address      = "192.168.20.1/24"
  allow_access = ["ping"]
}
```

## Schema

### Required

- `name` (String) The VLAN interface name. Changing it replaces the VLAN.
- `parent` (String) The physical interface the VLAN is tagged on, e.g. `lan`.
- `vlan_id` (Number) The VLAN tag, 1-4094.

### Optional

- `mode` (String) `static`, `dhcp` or `none`. Default: `static`.
- `address` (String) The gate's address in the VLAN with the prefix length (mode `static`).
- `gateway` (String) An upstream gateway in the VLAN, if it is an uplink.
- `mtu` (Number) The MTU, 576-9216; unset = the parent's.
- `enabled` (Boolean) Whether the VLAN is up. Default: `true`.
- `description` (String) A description.
- `allow_access` (Set of String) What devices in the VLAN may reach on the gate itself: `ping`, `https`, `ssh`.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_vlan.example iot
```
