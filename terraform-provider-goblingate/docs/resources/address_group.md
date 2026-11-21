---
page_title: "goblingate_address_group Resource"
description: |-
  An address group: one name for several address objects.
---

# goblingate_address_group (Resource)

An address group: one name for several address objects.

## Example Usage

```terraform
resource "goblingate_address_group" "servers" {
  name    = "servers"
  members = [goblingate_address.nas.name, goblingate_address.plex.name]
}
```

## Schema

### Required

- `name` (String) The group name. Changing it replaces the group.
- `members` (Set of String) The names of the addresses in the group.

### Read-Only

- `id` (String) The ID: the name.

## Import

Import an object that already exists on the gate with the name:

```shell
terraform import goblingate_address_group.example servers
```
