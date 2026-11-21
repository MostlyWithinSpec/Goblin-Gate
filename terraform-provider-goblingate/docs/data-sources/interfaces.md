---
page_title: "goblingate_interfaces Data Source"
description: |-
  The gate's interfaces and VLANs in the running config.
---

# goblingate_interfaces (Data Source)

The gate's interfaces and VLANs in the running config.

## Example Usage

```terraform
data "goblingate_interfaces" "gate" {}

locals {
  wan = [for i in data.goblingate_interfaces.gate.interfaces : i.name if i.role == "wan"][0]
}
```

## Schema

### Read-Only

- `interfaces` (List of Object) The physical interfaces: `name` (as used in policies, e.g. `wan1`), `device` (e.g. `eth0`), `role` (`wan` or `lan`), `mode`, `address`, `enabled`, `description`.
- `vlans` (List of Object) The VLAN interfaces: `name`, `parent`, `vlan_id`, `mode`, `address`, `enabled`.
