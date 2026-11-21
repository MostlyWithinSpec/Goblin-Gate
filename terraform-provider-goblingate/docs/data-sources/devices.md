---
page_title: "goblingate_devices Data Source"
description: |-
  The device inventory (Network → Devices): every device the gate has seen, sorted by MAC address.
---

# goblingate_devices (Data Source)

The device inventory (Network → Devices): every device the gate has seen, sorted by MAC address.

## Example Usage

```terraform
data "goblingate_devices" "iot" {
  interface = "iot"
}

output "iot_macs" {
  value = data.goblingate_devices.iot.devices[*].mac
}
```

## Schema

### Optional

- `interface` (String) Only devices on this interface or VLAN.

### Read-Only

- `devices` (List of Object) The devices: `mac`, `name` (given in the inventory), `hostname` (sent over DHCP), `vendor`, `ips` (newest first), `interface`, `vlan` (assigned for RADIUS), `first_seen`, `last_seen` (RFC 3339).
