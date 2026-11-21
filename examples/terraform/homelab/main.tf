# A homelab on Goblin Gate, as code: an IoT VLAN, a NAS with a fixed address
# and a DNS name, objects for it, policies, and a port forward for Plex.
#
#   export GOBLINGATE_TOKEN=...   # System → API tokens: read, commit, write:firewall, write:network
#   terraform init                # or: tofu init
#   terraform plan -var gate_url=https://192.168.1.1 -var ca_file=goblin-gate-ca.pem
#   terraform apply ...
#
# Every apply step is one candidate edit and one commit on the gate. With
# confirm_minutes the commit is rolled back unless Terraform can still reach
# the gate afterwards.

terraform {
  required_providers {
    goblingate = {
      source = "networkgoblinlabs/goblingate"
    }
  }
}

variable "gate_url" {
  description = "The gate's address."
  type        = string
  default     = "https://192.168.1.1"
}

variable "gate_token" {
  description = "An API token; leave unset to use GOBLINGATE_TOKEN."
  type        = string
  sensitive   = true
  default     = null
}

variable "ca_file" {
  description = "The gate's internal CA root (Network → Reverse Proxy → Certificate authority), to check its certificate."
  type        = string
  default     = null
}

variable "nas_mac" {
  description = "The NAS's MAC address."
  type        = string
  default     = "52:54:00:12:34:56"
}

provider "goblingate" {
  url             = var.gate_url
  token           = var.gate_token
  ca_file         = var.ca_file
  confirm_minutes = 5
}

# The WAN interface's name (wan1 on most gates), read from the gate.
data "goblingate_interfaces" "gate" {}

locals {
  wan = [for i in data.goblingate_interfaces.gate.interfaces : i.name if i.role == "wan"][0]
}

# --- network

resource "goblingate_vlan" "iot" {
  name         = "iot"
  parent       = "lan"
  vlan_id      = 20
  address      = "192.168.20.1/24"
  description  = "Smart plugs, cameras, TVs"
  allow_access = ["ping"]
}

resource "goblingate_dhcp_static_lease" "nas" {
  interface = "lan"
  mac       = var.nas_mac
  ip        = "192.168.1.10"
  hostname  = "nas"
}

resource "goblingate_dns_record" "nas" {
  name = "nas.home.lan"
  ip   = goblingate_dhcp_static_lease.nas.ip
}

# --- objects

resource "goblingate_address" "nas" {
  name    = "nas"
  type    = "host"
  value   = goblingate_dhcp_static_lease.nas.ip
  comment = "Managed by Terraform"
}

resource "goblingate_address" "iot_net" {
  name  = "iot-net"
  type  = "subnet"
  value = "192.168.20.0/24"
}

resource "goblingate_address_group" "media_servers" {
  name    = "media-servers"
  members = [goblingate_address.nas.name]
}

resource "goblingate_service" "plex" {
  name     = "plex"
  protocol = "tcp"
  ports    = "32400"
}

# --- policies

# IoT devices may reach the internet...
resource "goblingate_policy" "iot_internet" {
  name     = "iot-to-internet"
  src_intf = [goblingate_vlan.iot.name]
  dst_intf = [local.wan]
  src_addr = [goblingate_address.iot_net.name]
  action   = "accept"
  nat      = true
}

# ...and Plex on the NAS, nothing else on the LAN.
resource "goblingate_policy" "iot_plex" {
  name     = "iot-to-plex"
  src_intf = [goblingate_vlan.iot.name]
  dst_intf = ["lan"]
  dst_addr = [goblingate_address_group.media_servers.name]
  service  = [goblingate_service.plex.name]
  action   = "accept"
  log      = "all"
}

# Plex from outside.
resource "goblingate_port_forward" "plex" {
  name     = "plex"
  in_intf  = local.wan
  ext_port = "32400"
  protocol = "tcp"
  to_addr  = goblingate_address.nas.value
  to_port  = "32400"
}

output "wan_interface" {
  value = local.wan
}
