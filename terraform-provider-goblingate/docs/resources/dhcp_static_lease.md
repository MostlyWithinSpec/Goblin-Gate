---
page_title: "goblingate_dhcp_static_lease Resource"
description: |-
  A fixed address the DHCP server of an interface or VLAN always gives one device. The interface must have a DHCP server (Network → DHCP).
---

# goblingate_dhcp_static_lease (Resource)

A fixed address the DHCP server of an interface or VLAN always gives one device. The interface must have a DHCP server (Network → DHCP).

## Example Usage

```terraform
resource "goblingate_dhcp_static_lease" "nas" {
  interface = "lan"
  mac       = "52:54:00:12:34:56"
  ip        = "192.168.1.10"
  hostname  = "nas"
}
```

## Schema

### Required

- `interface` (String) The interface or VLAN whose DHCP server gives the address. Changing it replaces the lease.
- `mac` (String) The device's MAC address, lower case with colons. Changing it replaces the lease.
- `ip` (String) The address the device gets.

### Optional

- `hostname` (String) A host name for the device.

### Read-Only

- `id` (String) The ID: interface/mac.

## Import

Import an object that already exists on the gate with interface/mac:

```shell
terraform import goblingate_dhcp_static_lease.example lan/52:54:00:12:34:56
```
