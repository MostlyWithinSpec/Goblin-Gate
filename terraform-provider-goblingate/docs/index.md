---
page_title: "Provider: Goblin Gate"
description: |-
  Manage a Goblin Gate firewall's configuration as code with Terraform or OpenTofu.
---

# Goblin Gate Provider

Manages a [Goblin Gate](https://github.com/MostlyWithinSpec/Goblin-Gate) firewall through its API
(`/api/v1`): VLANs, DHCP static leases, local DNS records, address and service objects, policies and
port forwards. Data sources read the interfaces and the device inventory.

## How changes reach the gate

Goblin Gate keeps a *candidate* config that becomes the running config with a *commit*. The provider
works the same way:

- Changes Terraform makes in parallel (one step of an apply) are written to the candidate together
  and committed once, with a comment that lists them, such as
  `terraform: create address nas, create service plex`. Resources that depend on each other are
  committed one level at a time, so an apply makes a few commits, not one per resource.
- If the gate refuses the commit (for example a policy that names an address that does not exist),
  nothing is applied, the candidate is discarded, and the error lists the problems.
- If the candidate already holds changes that Terraform did not make (someone is editing in the web
  UI), the provider stops before touching anything. Commit or discard those changes, then run
  Terraform again.
- With `confirm_minutes`, every commit runs with a confirm timer and the provider confirms it once it
  can still reach the gate. A change that cuts Terraform off (a wrong firewall rule on the management
  interface) is rolled back when the timer ends.

Objects that Terraform does not manage are left alone, and fields of an object that the provider has
no attribute for (such as a VLAN's IPv6 settings) are kept when it changes the object.

## Example

```terraform
terraform {
  required_providers {
    goblingate = {
      source = "networkgoblinlabs/goblingate"
    }
  }
}

provider "goblingate" {
  url             = "https://192.168.1.1"
  ca_file         = "goblin-gate-ca.pem" # the gate's internal CA root
  confirm_minutes = 5
  # token: set GOBLINGATE_TOKEN
}

resource "goblingate_address" "nas" {
  name  = "nas"
  type  = "host"
  value = "192.168.1.10"
}
```

## Authentication

Create an API token under **System → API Tokens**. Give it the scopes `read` and `commit`, plus
`write:firewall` for objects, policies and port forwards and `write:network` for VLANs, DHCP and DNS.
Limit it to the address Terraform runs from with *Allowed from*.

## Schema

### Optional

- `url` (String) The gate's address, e.g. `https://gate.home.lan`. Default: `GOBLINGATE_URL`.
- `token` (String, Sensitive) The API token. Default: `GOBLINGATE_TOKEN`.
- `ca_file` (String) A PEM file with the CA that signed the gate's certificate, such as the gate's
  internal CA root. Default: `GOBLINGATE_CA_FILE`.
- `insecure` (Boolean) Skip the TLS certificate check (a gate with its self-signed certificate).
  Prefer `ca_file`. Default: `GOBLINGATE_INSECURE`.
- `confirm_minutes` (Number) Commit with a confirm timer of this many minutes (1-1440) and confirm
  once the gate still answers. Default: 0, no timer.
