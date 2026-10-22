# Remote access: SSH, API and CLI

## SSH: keys only

SSH logs in as **root with a key**. **Password logins over SSH are always off**, so the right password still
gives `Permission denied`. That is on purpose.

1. On your computer, make a key if you have none: `ssh-keygen -t ed25519`.
2. **System → Admins → SSH keys → Add**: paste the contents of `~/.ssh/id_ed25519.pub`, give it a name, save,
   **commit**.
3. `ssh root@192.168.1.1` (the interface you connect from needs `ssh` in its allowed access).

Locked out of the web UI? On the console, menu option 3 resets the admin password.

## Two-factor login

Turn it on under **System → Admins → Two-factor authentication**: press **Set up**, scan the QR code with an
authenticator app (or enter the secret by hand), then type the 6-digit code and **Turn on**. From then on,
logging in to the web UI asks for a second step — the code from the app.

Lost your phone? Two-factor can only be turned off from the console: `goblin --set-password <user>` resets the
password and turns two-factor off, so you can sign in again and set it up with a new device.

## The REST API

Everything the web UI does goes through `https://<box>/api/v1/...`.

- **System → API Tokens → Create**: the token is shown once. Send it as `Authorization: Bearer <token>`.
  Revoke tokens you no longer use (and any token you pasted somewhere public).
- Changing requests with a body need `Content-Type: application/json`.

Examples:

```sh
T=gg_xxxxxxxx
curl -k -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/system/status
curl -k -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/config/diff        # pending changes
curl -k -X POST -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/config/validate
```

Editing goes through the candidate (`GET/PUT/PATCH /api/v1/config/candidate`) and
`POST /api/v1/commit` (with `confirm_minutes` for an automatic rollback, see [[How Changes Work]]).

## Limited tokens

A new token gets everything by default, which is rarely what an integration needs. When you create
one, tick the scopes it may use. A token with no scopes is **full**; a token with scopes can only do
what those scopes allow, and everything else is refused with `403`.

| Scope | Allows |
| --- | --- |
| `read` | Every read-only endpoint |
| `write:firewall` | Addresses, services, policies, NAT, custom rules |
| `write:network` | Interfaces, routes, DHCP, DNS, VPN |
| `write:system` | Users, tokens, settings, backup |
| `actions` | Reboot, diagnostics, feed refresh, and other one-shot actions |
| `commit` | `POST /api/v1/commit` — without it a token can edit the candidate but never apply it |

The create form offers three presets: **Read only** (`read`), **Home automation**
(`read`, `write:firewall`, `actions`, `commit`) and **Full** (no scopes). Beyond scopes, a token can
also carry:

- an **expiry date** — the token stops working at the end of that day and the list marks it red once
  it has passed;
- **allowed-from networks** — the token only works from those CIDR networks.

The list shows scopes (`full` when there are none), expiry and the last time the token was used, so
you can spot one that is stale. Scoped tokens never see secrets or admin-only routes such as user
management, whatever their scopes.

```sh
curl -k -X POST -H "Authorization: Bearer $T" -H "Content-Type: application/json" \
  -d '{"name":"home automation","scopes":["read","write:firewall","actions","commit"],
       "expires":"2027-01-01T00:00:00Z","allowed_from":["192.168.1.0/24"]}' \
  https://192.168.1.1/api/v1/auth/tokens
```

## Watching events

`GET /api/v1/events` is a Server-Sent Events stream of what happens on the box — commits, logins,
IPS blocks, feed errors, the WAN going up or down. `-N` keeps curl from buffering, so lines arrive
as they come. The web UI shows the same stream under **Log & Report → Live Events**.

```sh
curl -k -N -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/events
# event: commit
# data: {"event":"commit","title":"Config committed","message":"root applied 3 changes","time":"..."}

# only the kinds you care about
curl -k -N -H "Authorization: Bearer $T" "https://192.168.1.1/api/v1/events?types=wan_down,feed_error"
```

A browser reads it with `EventSource`, which cannot send an `Authorization` header, so the web UI
relies on its session cookie instead.

## The CLI

`goblin` on the box (as root it talks to goblind over a local socket) or from another machine with
`--server https://<box> --token <token>`. FortiOS-style commands:

```
goblin -c "show system interface"
goblin -c "config system interface edit lan set address 192.168.1.1/24 next end"
goblin -c "diff"                               # pending changes
goblin -c "commit"                             # also: confirm, discard
goblin -c "history"                            # revisions
goblin -c "rollback 12"
goblin -f changes.gob                          # a script of commands
goblin import-pfsense config.xml --devices wan=eth0,lan=eth1
goblin --set-password admin                    # as root: reset a password
```

Run `goblin` without arguments for an interactive shell.
