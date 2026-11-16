# Homelab

The Homelab plugin turns the VMs and containers of your Proxmox VE cluster and Docker hosts into firewall
objects. You make a **live address** ([[Objects]]), use it in policies like any address, and the plugin
keeps it filled with the addresses of the guests you marked, as they start, stop and change address. No
commit is needed for that; every change is in the audit log.

You mark a guest for the live address `web` in one of two ways:

- a tag `gg-web` on the Proxmox VM or container (Datacenter → the guest → Summary → Tags), or a label
  `gg-web` on the Docker container (`docker run --label gg-web ...`, or `labels:` in Compose)
- a **name pattern** in the plugin settings: `web=web-*` puts every guest whose name starts with `web-`
  into `web`

Only running guests count. A guest with tags for several live addresses is in all of them.

## Install

As root on the box (see [[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]):

```sh
apt-get update && apt-get install goblin-gate-plugin-homelab
```

Then:

1. **System → Plugins → Homelab → Enable**, and approve the capabilities:
   - `objects:write` (needed): fill its live addresses
   - `devices:read`: find the addresses of guests that do not report them, by their MAC in the gate's
     DHCP leases (see below)
2. **Settings**: Proxmox, Docker or both (below).
3. **Network → Addresses → Add**: type **live**, plugin **homelab**, a name such as `web`. Use it in
   policies.
4. **Commit.** Within a minute the address holds the guests' addresses.

## Proxmox VE

The plugin needs a read-only API token. In the Proxmox web UI:

1. **Datacenter → Permissions → Users → Add**: user `goblin`, realm **Proxmox VE authentication server**
   (no password needed).
2. **Datacenter → Permissions → API Tokens → Add**: user `goblin@pve`, token ID `gate`, **Privilege
   Separation** off. Copy the secret: Proxmox shows it once.
3. **Datacenter → Permissions → Add → User Permission**: path `/vms`, user `goblin@pve`, role
   **PVEAuditor**. For the addresses of VMs from the QEMU guest agent, also a role with
   **VM.GuestAgent.Audit** (Proxmox VE 9) or **VM.Monitor** (Proxmox VE 8) on `/vms`.

In the plugin settings:

| Setting | Example |
|---|---|
| proxmox url | `https://pve.home.lan:8006` (one node of the cluster is enough) |
| proxmox token id | `goblin@pve!gate` |
| proxmox token secret | the secret from step 2 |
| proxmox fingerprint | Proxmox's own certificate is self-signed: copy its SHA-256 fingerprint from the node → System → Certificates (`pve-ssl.pem`), and the gate trusts that certificate only |

If your Proxmox has a certificate from a CA instead, leave the fingerprint empty and paste the CA's
certificate into **proxmox ca**, or leave both empty for a public CA (Let's Encrypt).

Where the addresses come from:

- **VMs**: the QEMU guest agent (install `qemu-guest-agent` in the VM and tick **QEMU Guest Agent** in its
  Options). Without the agent, the VM's MAC is looked up in the gate's DHCP leases.
- **Containers (LXC)**: Proxmox reports their addresses itself.

## Docker

The plugin talks to the Docker API over TLS with a client certificate. Never expose the Docker API
without one: anyone who can reach it is root on that host. Docker's guide
[Protect the Docker daemon socket](https://docs.docker.com/engine/security/protect-access/) shows how to
make the CA, the server certificate and a client certificate, and to start dockerd with `--tlsverify` on
port 2376.

| Setting | Example |
|---|---|
| docker url | `tcp://docker.home.lan:2376` |
| docker ca | the CA certificate (`ca.pem`) |
| docker cert | the client certificate (`cert.pem`) |
| docker key | the client key (`key.pem`), stored as a secret |
| docker host address | the Docker host's LAN address, if `docker url` is a name that does not resolve to it |

Paste the whole PEM text, `-----BEGIN ...-----` lines included; the form keeps it on one line, which is
fine.

Which address a container gets:

- on a **macvlan** or **ipvlan** network: its own LAN address
- on a **bridge** network (the default) or the **host** network: the Docker host's address, because that is
  where its traffic comes from. All such containers on one host share it, so a policy on one of them
  applies to the whole host.

## Settings for all sources

| Setting | What it does |
|---|---|
| name patterns | `object=pattern` per line, `*` and `?` as wildcards, case does not matter: `web=web-*`, `db=db?` |
| networks | use only addresses in these networks, such as `192.168.20.0/24`. Set it to your server networks: guests also report addresses that mean nothing to the gate (Docker's `172.17.0.1` inside a VM) |
| interval seconds | how often the guests are read, default 60 |

Loopback and link-local addresses are always left out.

## Safety

- The plugin decides who your policies on its live addresses match. Use them where that is fine (block or
  allow traffic of your VMs), not for access to the gate itself.
- If Proxmox or Docker cannot be reached, the live addresses keep their members until it works again: an
  outage does not empty them.
- The token and the client certificate are read-only for Proxmox, and for Docker the plugin only lists
  containers and networks; still, a Docker client certificate can do everything on that host, so keep it
  for the gate alone.

## Troubleshooting

The plugin's log: `journalctl -u goblin-plugin@homelab` over SSH. **Network → Addresses** shows each live
address's current members.

| Symptom | Cause |
|---|---|
| "not started: objects:write is not approved" | approve it under Enable |
| "nothing to watch" | set proxmox url or docker url |
| "HTTP 401 ... check proxmox_token_id" | the token ID or secret is wrong |
| "server certificate fingerprint ... is not the one in the settings" | copy the fingerprint again; Proxmox makes a new certificate when the node is renamed or the certificate is renewed |
| "tag gg-x names no live object of this plugin" | there is no live address `x` with plugin homelab, or it is not committed yet |
| "guest has no usable address yet" | a VM without guest agent and without a DHCP lease on the gate, or its addresses are outside **networks** |
| "sync failed, the objects keep their members" | Proxmox or Docker could not be read; the message says why |
