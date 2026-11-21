# Writes the resource and data source pages of the registry docs from the
# table below. Run: python docs/gen.py (from terraform-provider-goblingate).
import os

R = [
 ("address", "An address object (Network → Addresses) for policies, groups and port forwards.",
  '''resource "goblingate_address" "nas" {
  name    = "nas"
  type    = "host"
  value   = "192.168.1.10"
  comment = "The NAS"
}''',
  [("name", "String", "req", "The address name. Changing it replaces the object."),
   ("type", "String", "req", "`host`, `subnet`, `range`, `fqdn` or `country`."),
   ("value", "String", "req", 'The address: `192.168.1.10`, `192.168.1.0/24`, `192.168.1.10-192.168.1.20`, `nas.example.com`, or country codes `US,CA`.'),
   ("comment", "String", "opt", "A comment.")],
  "the name", "nas"),
 ("address_group", "An address group: one name for several address objects.",
  '''resource "goblingate_address_group" "servers" {
  name    = "servers"
  members = [goblingate_address.nas.name, goblingate_address.plex.name]
}''',
  [("name", "String", "req", "The group name. Changing it replaces the group."),
   ("members", "Set of String", "req", "The names of the addresses in the group.")],
  "the name", "servers"),
 ("service", "A service object: a protocol and ports, for policies.",
  '''resource "goblingate_service" "plex" {
  name     = "plex"
  protocol = "tcp"
  ports    = "32400"
}''',
  [("name", "String", "req", "The service name. Changing it replaces the service."),
   ("protocol", "String", "req", "`tcp`, `udp`, `icmp` or `any`."),
   ("ports", "String", "opt", "Ports and ranges, e.g. `80,443,8000-8100`."),
   ("comment", "String", "opt", "A comment.")],
  "the name", "plex"),
 ("policy", "A firewall policy (Policy & Objects → Firewall Policy). New policies are added at the end of the list, and the gate checks policies from the top: if one must come before an existing policy, move it in the web UI (the order is kept on later applies).",
  '''resource "goblingate_policy" "iot_plex" {
  name     = "iot-to-plex"
  src_intf = ["iot"]
  dst_intf = ["lan"]
  dst_addr = [goblingate_address.nas.name]
  service  = [goblingate_service.plex.name]
  action   = "accept"
  log      = "all"
}''',
  [("name", "String", "req", "The policy name."),
   ("src_intf", "Set of String", "req", "Interfaces, VLANs or zones the traffic comes from."),
   ("dst_intf", "Set of String", "req", "Interfaces, VLANs or zones the traffic goes to."),
   ("action", "String", "req", "`accept` or `deny`."),
   ("policy_id", "Number", "opt", "The policy's number. Default: the highest in use plus one. Changing it replaces the policy."),
   ("enabled", "Boolean", "opt", "Whether the policy is on. Default: `true`."),
   ("src_addr", "Set of String", "opt", 'Source address or group names. Default: `["all"]`.'),
   ("dst_addr", "Set of String", "opt", 'Destination address or group names. Default: `["all"]`.'),
   ("service", "Set of String", "opt", 'Service names. Default: `["any"]`.'),
   ("nat", "Boolean", "opt", "Masquerade the source address (traffic to the internet). Default: `false`."),
   ("log", "String", "opt", "`off`, `all` or `deny`."),
   ("schedule", "String", "opt", "A schedule name; unset = always."),
   ("ips_profile", "String", "opt", "An IPS profile name."),
   ("webfilter_profile", "String", "opt", "A web filter profile name."),
   ("dnsfilter_profile", "String", "opt", "A DNS filter profile name."),
   ("appctrl_profile", "String", "opt", "An application control profile name."),
   ("threat_feeds", "Set of String", "opt", "Threat feed names to block in this policy."),
   ("gateway", "String", "opt", "Send accepted traffic through this gateway or gateway group; unset = the default route.")],
  "the policy ID", "12"),
 ("port_forward", "A port forward (Network → Port Forwards): traffic to a port of a WAN interface goes to a host inside.",
  '''resource "goblingate_port_forward" "plex" {
  name     = "plex"
  in_intf  = "wan1"
  ext_port = "32400"
  protocol = "tcp"
  to_addr  = "192.168.1.10"
  to_port  = "32400"
}''',
  [("name", "String", "req", "The port forward name. Changing it replaces the forward."),
   ("in_intf", "String", "req", "The interface the traffic comes in on, usually `wan1`."),
   ("ext_port", "String", "req", "The outside port or range, e.g. `443` or `8000-8100`."),
   ("protocol", "String", "req", "`tcp` or `udp`."),
   ("to_addr", "String", "req", "The inside host's IP address."),
   ("to_port", "String", "req", "The inside port or range."),
   ("source", "Set of String", "opt", "Address or group names (countries too) that may use the forward; unset = anyone.")],
  "the name", "plex"),
 ("dhcp_static_lease", "A fixed address the DHCP server of an interface or VLAN always gives one device. The interface must have a DHCP server (Network → DHCP).",
  '''resource "goblingate_dhcp_static_lease" "nas" {
  interface = "lan"
  mac       = "52:54:00:12:34:56"
  ip        = "192.168.1.10"
  hostname  = "nas"
}''',
  [("interface", "String", "req", "The interface or VLAN whose DHCP server gives the address. Changing it replaces the lease."),
   ("mac", "String", "req", "The device's MAC address, lower case with colons. Changing it replaces the lease."),
   ("ip", "String", "req", "The address the device gets."),
   ("hostname", "String", "opt", "A host name for the device.")],
  "interface/mac", "lan/52:54:00:12:34:56"),
 ("dns_record", "A local DNS name (Network → DNS → Local records) that the gate's resolver answers with an address.",
  '''resource "goblingate_dns_record" "nas" {
  name = "nas.home.lan"
  ip   = "192.168.1.10"
}''',
  [("name", "String", "req", "The full name, e.g. `nas.home.lan`. Changing it replaces the record."),
   ("ip", "String", "req", "The IPv4 or IPv6 address.")],
  "the name", "nas.home.lan"),
 ("vlan", "A VLAN interface (Network → Interfaces). IPv6 settings made in the web UI are kept.",
  '''resource "goblingate_vlan" "iot" {
  name         = "iot"
  parent       = "lan"
  vlan_id      = 20
  address      = "192.168.20.1/24"
  allow_access = ["ping"]
}''',
  [("name", "String", "req", "The VLAN interface name. Changing it replaces the VLAN."),
   ("parent", "String", "req", "The physical interface the VLAN is tagged on, e.g. `lan`."),
   ("vlan_id", "Number", "req", "The VLAN tag, 1-4094."),
   ("mode", "String", "opt", "`static`, `dhcp` or `none`. Default: `static`."),
   ("address", "String", "opt", "The gate's address in the VLAN with the prefix length (mode `static`)."),
   ("gateway", "String", "opt", "An upstream gateway in the VLAN, if it is an uplink."),
   ("mtu", "Number", "opt", "The MTU, 576-9216; unset = the parent's."),
   ("enabled", "Boolean", "opt", "Whether the VLAN is up. Default: `true`."),
   ("description", "String", "opt", "A description."),
   ("allow_access", "Set of String", "opt", 'What devices in the VLAN may reach on the gate itself: `ping`, `https`, `ssh`.')],
  "the name", "iot"),
]

D = [
 ("interfaces", "The gate's interfaces and VLANs in the running config.",
  '''data "goblingate_interfaces" "gate" {}

locals {
  wan = [for i in data.goblingate_interfaces.gate.interfaces : i.name if i.role == "wan"][0]
}''',
  [],
  [("interfaces", "List of Object", "The physical interfaces: `name` (as used in policies, e.g. `wan1`), `device` (e.g. `eth0`), `role` (`wan` or `lan`), `mode`, `address`, `enabled`, `description`."),
   ("vlans", "List of Object", "The VLAN interfaces: `name`, `parent`, `vlan_id`, `mode`, `address`, `enabled`.")]),
 ("devices", "The device inventory (Network → Devices): every device the gate has seen, sorted by MAC address.",
  '''data "goblingate_devices" "iot" {
  interface = "iot"
}

output "iot_macs" {
  value = data.goblingate_devices.iot.devices[*].mac
}''',
  [("interface", "String", "Only devices on this interface or VLAN.")],
  [("devices", "List of Object", "The devices: `mac`, `name` (given in the inventory), `hostname` (sent over DHCP), `vendor`, `ips` (newest first), `interface`, `vlan` (assigned for RADIUS), `first_seen`, `last_seen` (RFC 3339).")]),
]

here = os.path.dirname(os.path.abspath(__file__))
for name, desc, ex, attrs, idwhat, idex in R:
    req = [a for a in attrs if a[2] == "req"]
    opt = [a for a in attrs if a[2] == "opt"]
    out = [f'---\npage_title: "goblingate_{name} Resource"\ndescription: |-\n  {desc}\n---\n',
           f"# goblingate_{name} (Resource)\n", desc + "\n", "## Example Usage\n", "```terraform\n" + ex + "\n```\n",
           "## Schema\n", "### Required\n"]
    out += [f"- `{n}` ({t}) {d}" for n, t, _, d in req]
    if opt:
        out += ["", "### Optional\n"] + [f"- `{n}` ({t}) {d}" for n, t, _, d in opt]
    out += ["", "### Read-Only\n", f"- `id` (String) The ID: {idwhat}.", "", "## Import\n",
            f"Import an object that already exists on the gate with {idwhat}:\n",
            f"```shell\nterraform import goblingate_{name}.example {idex}\n```\n"]
    os.makedirs(os.path.join(here, "resources"), exist_ok=True)
    open(os.path.join(here, "resources", name + ".md"), "w", encoding="utf-8", newline="\n").write("\n".join(out))
for name, desc, ex, args, attrs in D:
    out = [f'---\npage_title: "goblingate_{name} Data Source"\ndescription: |-\n  {desc}\n---\n',
           f"# goblingate_{name} (Data Source)\n", desc + "\n", "## Example Usage\n", "```terraform\n" + ex + "\n```\n",
           "## Schema\n"]
    if args:
        out += ["### Optional\n"] + [f"- `{n}` ({t}) {d}" for n, t, d in args] + [""]
    out += ["### Read-Only\n"] + [f"- `{n}` ({t}) {d}" for n, t, d in attrs] + [""]
    os.makedirs(os.path.join(here, "data-sources"), exist_ok=True)
    open(os.path.join(here, "data-sources", name + ".md"), "w", encoding="utf-8", newline="\n").write("\n".join(out))
