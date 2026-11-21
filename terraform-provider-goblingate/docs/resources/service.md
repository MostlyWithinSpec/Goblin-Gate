---
page_title: "goblingate_service Resource"
description: |-
  A service object: a protocol and ports, for policies.
---

# goblingate_service (Resource)

A service object: a protocol and ports, for policies.

## Example Usage

```terraform
resource "goblingate_service" "plex" {
  name     = "plex"
  protocol = "tcp"
  ports    = "32400"
}
```

## Schema

### Required

- `name` (String) The service name. Changing it replaces the service.
- `protocol` (String) `tcp`, `udp`, `icmp` or `any`.

### Optional

- `ports` (String) Ports and ranges, e.g. `80,443,8000-8100`.
- `comment` (String) A comment.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_service.example plex
```
