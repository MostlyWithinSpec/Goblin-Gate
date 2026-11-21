---
page_title: "goblingate_address Resource"
description: |-
  An address object (Network → Addresses) for policies, groups and port forwards.
---

# goblingate_address (Resource)

An address object (Network → Addresses) for policies, groups and port forwards.

## Example Usage

```terraform
resource "goblingate_address" "nas" {
  name    = "nas"
  type    = "host"
  value   = "192.168.1.10"
  comment = "The NAS"
}
```

## Schema

### Required

- `name` (String) The address name. Changing it replaces the object.
- `type` (String) `host`, `subnet`, `range`, `fqdn` or `country`.
- `value` (String) The address: `192.168.1.10`, `192.168.1.0/24`, `192.168.1.10-192.168.1.20`, `nas.example.com`, or country codes `US,CA`.

### Optional

- `comment` (String) A comment.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_address.example nas
```
