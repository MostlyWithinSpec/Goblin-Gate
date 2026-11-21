# Automation

Everything the web UI does goes through the gate's REST API (`/api/v1`), so a gate can be set up and
kept in shape from a script, from Terraform or OpenTofu, or from Ansible. This page shows the same
homelab three ways: an IoT VLAN, a NAS with a fixed address and a DNS name, address and service
objects, a policy that lets IoT devices reach Plex on the NAS, and a port forward.

## 1. A token for automation

Under **System → API Tokens → Create**, make a token just for this:

- **Scopes:** `read` and `commit`, plus `write:firewall` (objects, policies, port forwards) and
  `write:network` (VLANs, DHCP, DNS). Leave out what your automation does not change.
- **Allowed from:** the address of the machine that runs it, e.g. `192.168.1.20/32`.
- **Expires:** optional, but a date makes you revisit it.

The token is shown once. Send it as `Authorization: Bearer <token>`. Scoped tokens never see secrets
(passwords, keys) and cannot reach admin-only routes such as accounts and updates; see
[Remote Access: SSH, API and CLI](Remote-Access-SSH-API-CLI) for the scopes and for creating a
token with the API itself.

Check the gate's certificate instead of turning the check off: download the internal CA root under
**Network → Reverse Proxy → Certificate authority** (see [[Internal CA]]) and give it to your tool
(`curl --cacert`, `ca_file` in Terraform, `ca_path` in Ansible). The examples below use
`goblin-gate-ca.pem`.

## 2. The commit model, for scripts

Changes go to the **candidate** config and take effect with a **commit**, the same as in the web UI
(see [[How Changes Work]]):

| Step | Request |
|---|---|
| Are there uncommitted changes? | `GET /api/v1/config/diff` → `{"count": 0, "changes": []}` |
| Change the candidate | `PATCH /api/v1/config/candidate` `{"path": "...", "value": ...}` or `{"path": "...", "delete": true}` |
| Commit | `POST /api/v1/commit` `{"comment": "...", "confirm_minutes": 5}` |
| Keep it | `POST /api/v1/commit/confirm` |
| Throw the candidate away | `POST /api/v1/commit/discard` |
| Read what is running | `GET /api/v1/config/running` |

Good habits for automation:

- **Start clean.** If `config/diff` is not empty, someone is editing in the web UI. Stop rather than
  commit their half-finished change along with yours.
- **One change, one commit.** Make all the edits of one change, then commit once with a comment
  that says what it was. The comment is in **System → Revisions**, and a revision can be rolled back
  there.
- **Commit with a confirm timer** (`confirm_minutes`), then confirm once you can still reach the gate.
  If a change cuts you off (a wrong rule on the management interface), the gate rolls back by itself
  when the timer ends.
- **On any error, discard.** A `PATCH` stores the change even when the config is not valid yet (the
  answer lists `validation_errors`); the commit refuses an invalid config with `422` and the problems
  in `error.details`, and nothing is applied.

Paths pick an object by its key: `addresses[name=nas]`, `policies[id=20]`,
`dhcp[interface=lan].static_leases[mac=52:54:00:12:34:56]`, `dns.local_records.[nas.home.lan]`.
Setting a path that matches nothing adds the object; deleting a path that matches nothing is an
error. Every route, with its request and answer, is in the OpenAPI document (see
[Remote Access: SSH, API and CLI](Remote-Access-SSH-API-CLI)).

## 3. Terraform or OpenTofu

The Goblin Gate provider (`networkgoblinlabs/goblingate`) manages VLANs, DHCP static leases, local
DNS records, addresses, address groups, services, policies and port forwards, and reads the
interfaces and the device inventory. It follows the habits above for you: the changes of one apply
step become one candidate edit and one commit with a comment such as
`terraform: create address nas, create service plex`; it refuses to start when the candidate holds
someone else's changes; with `confirm_minutes` it commits with a timer and confirms. Anything
Terraform does not manage is left alone.

**Install it.** Every release carries `terraform-provider-goblingate_<version>_<os>_<arch>.zip` for
Linux, macOS and Windows. Unzip it into
`~/.terraform.d/plugins/registry.terraform.io/networkgoblinlabs/goblingate/<version>/<os>_<arch>/`
(for OpenTofu, `registry.opentofu.org` in place of `registry.terraform.io`) and pin that version in
`required_providers`. The provider's README in the repository has the details.

**The homelab.** The complete file is `examples/terraform/homelab/main.tf` in the repository; the
heart of it:

```terraform
terraform {
  required_providers {
    goblingate = { source = "networkgoblinlabs/goblingate" }
  }
}

provider "goblingate" {
  url             = "https://192.168.1.1"
  ca_file         = "goblin-gate-ca.pem"
  confirm_minutes = 5
  # token: export GOBLINGATE_TOKEN=...
}

data "goblingate_interfaces" "gate" {}

locals {
  wan = [for i in data.goblingate_interfaces.gate.interfaces : i.name if i.role == "wan"][0]
}

resource "goblingate_vlan" "iot" {
  name         = "iot"
  parent       = "lan"
  vlan_id      = 20
  address      = "192.168.20.1/24"
  allow_access = ["ping"]
}

resource "goblingate_dhcp_static_lease" "nas" {
  interface = "lan"
  mac       = "52:54:00:12:34:56"
  ip        = "192.168.1.10"
  hostname  = "nas"
}

resource "goblingate_dns_record" "nas" {
  name = "nas.home.lan"
  ip   = goblingate_dhcp_static_lease.nas.ip
}

resource "goblingate_address" "nas" {
  name  = "nas"
  type  = "host"
  value = goblingate_dhcp_static_lease.nas.ip
}

resource "goblingate_service" "plex" {
  name     = "plex"
  protocol = "tcp"
  ports    = "32400"
}

resource "goblingate_policy" "iot_plex" {
  name     = "iot-to-plex"
  src_intf = [goblingate_vlan.iot.name]
  dst_intf = ["lan"]
  dst_addr = [goblingate_address.nas.name]
  service  = [goblingate_service.plex.name]
  action   = "accept"
}

resource "goblingate_port_forward" "plex" {
  name     = "plex"
  in_intf  = local.wan
  ext_port = "32400"
  protocol = "tcp"
  to_addr  = goblingate_address.nas.value
  to_port  = "32400"
}
```

Then `terraform init`, `terraform plan`, `terraform apply` (or the same with `tofu`).

**Things to know:**

- **Existing objects:** bring them under Terraform with `terraform import`, e.g.
  `terraform import goblingate_address.nas nas`, `terraform import goblingate_policy.iot_plex 20`
  (the policy number) or `terraform import goblingate_dhcp_static_lease.nas lan/52:54:00:12:34:56`.
  Creating an object that already exists is refused.
- **Policy order:** new policies go to the end of the list, and the gate checks policies from the
  top. If one must come before an existing policy, move it under **Policy & Objects → Firewall Policy**; later
  applies keep the position.
- **Static leases** need a DHCP server on the interface (**Network → DHCP**).
- **Changes made in the web UI** to Terraform's objects show up in the next `terraform plan`.

## 4. The same with curl

```sh
GATE=https://192.168.1.1
export TOKEN=...   # the token from step 1
gg() { curl -sS --fail-with-body --cacert goblin-gate-ca.pem \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
set_() { gg -X PATCH "$GATE/api/v1/config/candidate" -d "{\"path\": \"$1\", \"value\": $2}" >/dev/null; }

# start clean
[ "$(gg "$GATE/api/v1/config/diff" | jq .count)" = 0 ] || { echo "uncommitted changes on the gate"; exit 1; }

set -e
trap 'gg -X POST "$GATE/api/v1/commit/discard" >/dev/null' ERR
set_ 'vlans[name=iot]' '{"name": "iot", "parent": "lan", "vlan_id": 20, "mode": "static",
  "address": "192.168.20.1/24", "enabled": true, "allow_access": ["ping"]}'
set_ 'dhcp[interface=lan].static_leases[mac=52:54:00:12:34:56]' \
  '{"mac": "52:54:00:12:34:56", "ip": "192.168.1.10", "hostname": "nas"}'
set_ 'dns.local_records.[nas.home.lan]' '"192.168.1.10"'
set_ 'addresses[name=nas]' '{"name": "nas", "type": "host", "value": "192.168.1.10"}'
set_ 'services[name=plex]' '{"name": "plex", "protocol": "tcp", "ports": "32400"}'
set_ 'policies[id=20]' '{"id": 20, "name": "iot-to-plex", "enabled": true, "src_intf": ["iot"],
  "dst_intf": ["lan"], "src_addr": ["all"], "dst_addr": ["nas"], "service": ["plex"], "action": "accept"}'
set_ 'nat.port_forwards[name=plex]' '{"name": "plex", "in_intf": "wan1", "ext_port": "32400",
  "protocol": "tcp", "to_addr": "192.168.1.10", "to_port": "32400"}'

# one commit, with a 5-minute safety net; then keep it (nothing to do when nothing changed)
if [ "$(gg "$GATE/api/v1/config/diff" | jq .count)" != 0 ]; then
  gg -X POST "$GATE/api/v1/commit" -d '{"comment": "homelab: IoT VLAN, NAS, Plex", "confirm_minutes": 5}'
  gg -X POST "$GATE/api/v1/commit/confirm"
fi
```

Pick a policy number that is free (`gg "$GATE/api/v1/config/running" | jq '[.policies[].id] | max'`),
and use your WAN interface's name in the port forward (`jq '.interfaces[] | select(.role=="wan") |
.name'`). Running the script again changes nothing: setting an object to what it already is leaves
the candidate as it was, so `config/diff` is empty and the script does not commit. (A commit always
makes a revision, even an empty one.)

## 5. Ansible

The `ansible.builtin.uri` module is all it takes; no collection to install.

```yaml
# homelab.yml: ansible-playbook homelab.yml -e gate_token=...
- name: Goblin Gate homelab
  hosts: localhost
  gather_facts: false
  vars:
    gate: https://192.168.1.1
    headers:
      Authorization: "Bearer {{ gate_token }}"
    objects:
      - path: "vlans[name=iot]"
        value: {name: iot, parent: lan, vlan_id: 20, mode: static, address: 192.168.20.1/24,
                enabled: true, allow_access: [ping]}
      - path: "dhcp[interface=lan].static_leases[mac=52:54:00:12:34:56]"
        value: {mac: "52:54:00:12:34:56", ip: 192.168.1.10, hostname: nas}
      - path: "dns.local_records.[nas.home.lan]"
        value: 192.168.1.10
      - path: "addresses[name=nas]"
        value: {name: nas, type: host, value: 192.168.1.10}
      - path: "services[name=plex]"
        value: {name: plex, protocol: tcp, ports: "32400"}
      - path: "policies[id=20]"
        value: {id: 20, name: iot-to-plex, enabled: true, src_intf: [iot], dst_intf: [lan],
                src_addr: [all], dst_addr: [nas], service: [plex], action: accept}
      - path: "nat.port_forwards[name=plex]"
        value: {name: plex, in_intf: wan1, ext_port: "32400", protocol: tcp,
                to_addr: 192.168.1.10, to_port: "32400"}
  module_defaults:
    ansible.builtin.uri:
      headers: "{{ headers }}"
      ca_path: goblin-gate-ca.pem
      body_format: json
  tasks:
    - name: Nobody else has uncommitted changes
      ansible.builtin.uri:
        url: "{{ gate }}/api/v1/config/diff"
      register: diff
      failed_when: diff.json.count != 0

    - name: Change, commit and confirm
      block:
        - name: Set the objects in the candidate
          ansible.builtin.uri:
            url: "{{ gate }}/api/v1/config/candidate"
            method: PATCH
            body: {path: "{{ item.path }}", value: "{{ item.value }}"}
          loop: "{{ objects }}"
          loop_control:
            label: "{{ item.path }}"

        - name: What will change
          ansible.builtin.uri:
            url: "{{ gate }}/api/v1/config/diff"
          register: pending

        - name: Commit with a 5-minute confirm timer
          ansible.builtin.uri:
            url: "{{ gate }}/api/v1/commit"
            method: POST
            body: {comment: "ansible: homelab", confirm_minutes: 5}
          when: pending.json.count > 0

        - name: Confirm (the gate still answers)
          ansible.builtin.uri:
            url: "{{ gate }}/api/v1/commit/confirm"
            method: POST
          when: pending.json.count > 0
      rescue:
        - name: Throw the candidate away
          ansible.builtin.uri:
            url: "{{ gate }}/api/v1/commit/discard"
            method: POST
        - name: Stop
          ansible.builtin.fail:
            msg: "{{ ansible_failed_result.json.error | default(ansible_failed_result.msg) }}"
```

The play only commits when the candidate differs from the running config, so it reports no
changes on a second run. Keep the token in Ansible Vault rather than on the command line.

## See also

- [[How Changes Work]]: candidate, diff, commit, confirm and rollback.
- [Remote Access: SSH, API and CLI](Remote-Access-SSH-API-CLI): tokens, scopes, the OpenAPI document
  and the `goblin` CLI.
- [[Objects]] and [[Firewall Policies]]: what the objects and policies mean.
