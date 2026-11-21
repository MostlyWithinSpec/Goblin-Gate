---
page_title: "goblingate_port_forward Resource"
description: |-
  A port forward (Network → Port Forwards): traffic to a port of a WAN interface goes to a host inside.
---

# goblingate_port_forward (Resource)

A port forward (Network → Port Forwards): traffic to a port of a WAN interface goes to a host inside.

## Example Usage

```terraform
resource "goblingate_port_forward" "plex" {
  name     = "plex"
  in_intf  = "wan1"
  ext_port = "32400"
  protocol = "tcp"
  to_addr  = "192.168.1.10"
  to_port  = "32400"
}
```

## Schema

### Required

- `name` (String) The port forward name. Changing it replaces the forward.
- `in_intf` (String) The interface the traffic comes in on, usually `wan1`.
- `ext_port` (String) The outside port or range, e.g. `443` or `8000-8100`.
- `protocol` (String) `tcp` or `udp`.
- `to_addr` (String) The inside host's IP address.
- `to_port` (String) The inside port or range.

### Optional

- `source` (Set of String) Address or group names (countries too) that may use the forward; unset = anyone.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_port_forward.example plex
```
