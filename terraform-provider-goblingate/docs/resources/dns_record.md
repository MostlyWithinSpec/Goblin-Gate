---
page_title: "goblingate_dns_record Resource"
description: |-
  A local DNS name (Network → DNS → Local records) that the gate's resolver answers with an address.
---

# goblingate_dns_record (Resource)

A local DNS name (Network → DNS → Local records) that the gate's resolver answers with an address.

## Example Usage

```terraform
resource "goblingate_dns_record" "nas" {
  name = "nas.home.lan"
  ip   = "192.168.1.10"
}
```

## Schema

### Required

- `name` (String) The full name, e.g. `nas.home.lan`. Changing it replaces the record.
- `ip` (String) The IPv4 or IPv6 address.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_dns_record.example nas.home.lan
```
