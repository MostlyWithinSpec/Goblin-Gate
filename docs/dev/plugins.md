# Plugins

Optional features ship as plugins: separate packages that goblind discovers, starts and gives a narrow,
audited API. A plugin never runs as root, never gets a shell, and can only do what the admin approved.

## Packaging

A plugin is a Debian package `goblin-gate-plugin-<name>` from the same APT repository and channels as
Goblin Gate itself, so it updates the same way. It installs:

| Path | What |
|---|---|
| `/usr/share/goblin-gate/plugins/<name>/plugin.json` | the manifest |
| `/usr/lib/goblin-gate/plugins/<name>/run` | the plugin's program (any language) |
| `/usr/share/goblin-gate/plugins/<name>/ui/` | optional static web pages |

The package must not start anything itself, must not ship systemd units, and must not touch `/etc`.
goblind decides when the plugin runs.

`<name>`: 1-32 characters of `a-z`, `0-9` and `-`, starting with a letter.

## Manifest

```json
{
  "name": "hello",
  "version": "1.0.0",
  "description": "Example plugin",
  "min_goblind": "0.1.0~alpha.3",
  "capabilities": ["events:read", "notify:send"],
  "settings_schema": {
    "type": "object",
    "properties": {
      "greeting": {"type": "string", "default": "hi", "maxLength": 64},
      "interval_seconds": {"type": "integer", "minimum": 10, "maximum": 3600}
    },
    "required": ["greeting"]
  },
  "ui": {"pages": [{"path": "status", "title": "Hello status"}]}
}
```

- `capabilities` is what the plugin asks for. The admin approves some or all of them when enabling the
  plugin; the plugin gets only the approved ones.
- `settings_schema` is a small subset of JSON Schema: an object whose properties have `type` (`string`,
  `integer`, `number`, `boolean`, or `array` of strings), optional `enum`, `minimum`/`maximum`,
  `maxLength`, `default`; plus `required`. Unknown settings are rejected.
- `min_goblind` (a Debian version) stops an old goblind from running a plugin built for a newer API.

A manifest that does not validate makes the plugin show as `invalid` with the reason; it is never started.

## Capabilities

| Capability | Allows |
|---|---|
| `events:read` | `GET /api/v1/logs` (traffic, IPS, DNS and system events) |
| `stats:read` | `GET /api/v1/stats/*`, `POST /api/v1/stats/query` |
| `config:read` | `GET /api/v1/config/running` |
| `notify:send` | `POST /api/v1/plugin/notify` (an alert through the admin's notification rules) |
| `blocks:read` | `GET /api/v1/blocks` (active temporary blocks) |
| `blocks:write` | `POST /api/v1/blocks`, `DELETE /api/v1/blocks/{ip}` (see below) |

Every plugin may call `GET /api/v1/plugin/self` (its own name, approved capabilities and settings)
without a capability. Every other endpoint is refused with `403 scope_denied`, including everything that
changes the config, commits, admins and tokens. The table is in `internal/plugins/scopes.go`; an endpoint
is reachable by plugins only when it is listed there.

### Temporary blocks

`POST /api/v1/blocks {ip, minutes (1-10080), reason}` drops all traffic from and to one address until the
time runs out; the kernel expires it by itself. It is the only way a plugin can cut traffic, and goblind
refuses protected targets: the box's own addresses, its gateways, the DNS and NTP servers it uses, and
every address inside its own networks (LAN side, VPNs). A plugin may add 30 blocks per hour, may lift only
its own blocks, and every attempt is audited (`block_add`, `block_remove`) and sent as the `block_added`
notification. Admins use the same endpoints and may block an internal address with `allow_internal: true`.

## Lifecycle

Config:

```json
"plugins": [
  {"name": "hello", "enabled": true, "capabilities": ["events:read"], "settings": {"greeting": "hi"}}
]
```

Like everything else, this is part of the candidate config and takes effect on commit. After each
commit (and at boot) goblind's plugin manager compares the installed plugins with the config:

- enabled, installed and valid: when its settings or capabilities changed or it is not running,
  `systemctl restart goblin-plugin@<name>` (a plugin reads its settings once, at start);
- anything else: `systemctl stop goblin-plugin@<name>`.

A config that no longer fits the installed plugin (after a package upgrade or a rollback) does not
break anything: the plugin is started only with the capabilities its manifest still asks for, and not
at all when its settings no longer match the schema.

Validation refuses a commit that enables a plugin which is not installed or invalid, approves a
capability the manifest does not ask for, or has settings that do not match the schema.

`goblin-plugin@.service` runs `/usr/lib/goblin-gate/plugins/%i/run` with `DynamicUser=yes` (a throwaway
unprivileged user), no capabilities, `NoNewPrivileges`, a read-only system, a private `/tmp`, its own
state directory (`/var/lib/goblin-plugin-<name>`, `$STATE_DIRECTORY`) and `Restart=on-failure`.
`GOBLIN_PLUGIN_SOCKET` names the socket and `GOBLIN_PLUGIN_NAME` the plugin.

API (admin):

| Endpoint | |
|---|---|
| `GET /api/v1/plugins` | installed plugins: manifest, config state, status (`running`, `stopped`, `failed`, `invalid`, `not_installed`) |
| `POST /api/v1/plugins/{name}/enable` `{capabilities?}` | enables it in the candidate (default: every capability it asks for) |
| `POST /api/v1/plugins/{name}/disable` | disables it in the candidate |
| `PUT /api/v1/plugins/{name}/settings` `{settings}` | validates against the schema and stores in the candidate |

Enable, disable and settings change the candidate; commit to apply.

A plugin can also serve its own API for the admin UI: it listens on `$GOBLIN_PLUGIN_API_SOCKET`
(`/run/goblin-plugin-<name>/api.sock`) and goblind forwards `/api/v1/plugins/<name>/api/<path>` to it
as `/<path>` after logging the admin in. Read-only admins only get GET; the plugin sees who asks in
`X-Goblin-User` and `X-Goblin-Role` and never the admin's cookies, tokens or CSRF header. Answers must be
JSON; cookies are stripped. Every change (non-GET call) is audited as `plugin_admin_call`.

## Trust boundary

Plugins talk to goblind only over `/run/goblin-plugins/api.sock` (mode 0666: plugin users are dynamic).
This socket is separate from the root CLI socket (`/run/goblind/api.sock`, mode 0600, which acts as
root) and serves only the plugin routes. goblind identifies the caller by the connecting process
(`SO_PEERCRED`) and its cgroup: it must belong to the unit `goblin-plugin@<name>.service` of a plugin
goblind started. Only systemd places processes in a unit's cgroup, so a plugin cannot act as another
one, and there is no secret to leak or hand over: a plugin sends plain HTTP requests without any
`Authorization` header. Anything else that connects gets `401`. goblind then checks the route's
capability and audits every call (`plugin_call` with the plugin, method, path and result).

A plugin is still code running on the firewall: install only plugins you trust. The sandbox limits what
a buggy plugin can break; it is not designed to contain a hostile one that also has network access.

## Web UI

Plugin pages are served at `/plugin-ui/<name>/<path>` from the plugin's `ui/` directory (a path ending
in `/` serves `index.html`), and the main web UI shows the pages the manifest lists in an iframe. They
are sent with `Content-Security-Policy: sandbox allow-scripts allow-forms; ... connect-src 'none'`: the
page gets an opaque origin, so plugin JavaScript cannot read the admin's session, call the API or fetch
anything. A page is static; one that needs live data is bundled into the main web UI after review
instead. Goblin Sentry's pages are bundled this way and appear only while the plugin is installed.

Goblin Sentry's decision model and its training, versions and comparison: [goblin.md](goblin.md).
