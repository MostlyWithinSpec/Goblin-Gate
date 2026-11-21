# Terraform / OpenTofu provider for Goblin Gate

Manage a gate's VLANs, DHCP static leases, local DNS records, address and service objects, policies
and port forwards as code. The provider uses the gate's API (`/api/v1`) with a scoped API token, and
every apply step is one candidate edit and one commit. See [docs/index.md](docs/index.md) for the
provider settings and the commit model, `docs/resources/` and `docs/data-sources/` for each resource,
and `examples/terraform/homelab` in the repository for a complete example.

Resources: `goblingate_address`, `goblingate_address_group`, `goblingate_service`,
`goblingate_policy`, `goblingate_port_forward`, `goblingate_dhcp_static_lease`,
`goblingate_dns_record`, `goblingate_vlan`. Data sources: `goblingate_interfaces`,
`goblingate_devices`. Every resource can be imported.

## Installing a release build

Each Goblin Gate release has the provider for Linux, macOS and Windows (amd64 and arm64) among its
assets. Until it is in a registry, install it as a local mirror: unzip
`terraform-provider-goblingate_<version>_<os>_<arch>.zip` into

```
~/.terraform.d/plugins/registry.terraform.io/networkgoblinlabs/goblingate/<version>/<os>_<arch>/
```

(`%APPDATA%\terraform.d\plugins\...` on Windows; for OpenTofu use `registry.opentofu.org` in place of
`registry.terraform.io`), and pin that version:

```terraform
terraform {
  required_providers {
    goblingate = {
      source  = "networkgoblinlabs/goblingate"
      version = "<version>"
    }
  }
}
```

Check the zip against the release's `terraform-provider-goblingate_<version>_SHA256SUMS`.

## Developing

It is a Go module of its own, so the plugin framework never becomes a dependency of goblind.

```
go test ./...                                  # unit tests
TF_ACC=1 go test ./internal/provider -v        # acceptance tests
```

The acceptance tests build goblind from this repository and run it with `--dev` (a fake applier and
no authentication) in a temporary directory, or use the gate in `GOBLINGATE_URL` (with
`GOBLINGATE_TOKEN`) if set. They need `terraform` on PATH, `TF_ACC_TERRAFORM_PATH`, or a network
connection to download it. `GOBLINGATE_ACC_KEEP=1` keeps the dev gate's directory for a look at its
log. The registry pages in `docs/` are written by `python docs/gen.py`.
