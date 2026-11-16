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
- `"secret": true` on a string setting (no default) marks a password or token. It is stored apart from the
  other settings (`secrets` in the plugin's config entry) and redacted wherever configs are shown: other
  plugins, read-only users, the plugin list and the git backup never see it. The plugin gets it with its
  settings from `GET /api/v1/plugin/self`. The admin UI shows only whether it is set; saving the form with
  it empty keeps the stored value, `null` clears it.
- `min_goblind` (a Debian version) stops an old goblind from running a plugin built for a newer API.

A manifest that does not validate makes the plugin show as `invalid` with the reason; it is never started.

## Capabilities

| Capability | Allows |
|---|---|
| `events:read` | `GET /api/v1/logs` (traffic, IPS, DNS and system events) |
| `stats:read` | `GET /api/v1/stats/*`, `POST /api/v1/stats/query`, `GET /api/v1/gateways/status`, `GET /api/v1/wan/status` |
| `config:read` | `GET /api/v1/config/running` |
| `notify:send` | `POST /api/v1/plugin/notify` (an alert through the admin's notification rules) |
| `blocks:read` | `GET /api/v1/blocks` (active temporary blocks) |
| `blocks:write` | `POST /api/v1/blocks`, `DELETE /api/v1/blocks/{ip}` (see below) |
| `devices:read` | `GET /api/v1/devices` (the device inventory: MAC, addresses, names, last seen) |
| `policies:toggle` | `GET /api/v1/plugin/policies`, `PUT /api/v1/plugin/policies/{id}` (see below) |
| `objects:write` | `GET /api/v1/plugin/objects`, `PUT /api/v1/plugin/objects/{name}` (see below) |

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

### Policy switches

With `policies:toggle`, a plugin may switch on and off the policies the admin listed for it
(`"policies": [id, ...]` in its config entry, set with `PUT /api/v1/plugins/{name}/policies`) and nothing
else. `GET /api/v1/plugin/policies` returns them as running now (`{policies: [{id, name, enabled}]}`);
`PUT /api/v1/plugin/policies/{id} {enabled}` switches one and commits just that change as a new revision by
`plugin:<name>`, so it is in the history and can be rolled back. An admin's uncommitted edits in the
candidate stay and get the same switch. goblind refuses a policy that is not listed (`403`), any switch
while a commit-confirm waits (`409 confirm_pending`), and more than 60 switches per hour per plugin
(`429`); every attempt is audited (`policy_switch`). List only policies whose both states are safe: a
plugin, or whoever can talk to it, can choose either.

### Live address objects

With `objects:write`, a plugin keeps the members of the live address objects the admin gave it: address
objects of type `live` whose value is the plugin's name. The admin uses them in policies and groups like any
other address; the plugin fills them with what it finds (the VMs of a hypervisor, the containers of a
host). `GET /api/v1/plugin/objects` returns the plugin's objects (`{objects: [{name, plugin, members,
updated}]}`, members as prefixes, `/32` and `/128` for hosts); `PUT /api/v1/plugin/objects/{name}
{members: ["10.0.0.5", "fd00:1::/64"]}` replaces one object's members and answers `{name, members, added,
removed, changed}`. An empty list clears the object.

The members never enter the config and need no commit: goblind keeps them in its state directory and loads
them straight into the firewall's sets, so a policy follows within a second. A member is an address or a
prefix no wider than `/16` (IPv4) or `/48` (IPv6), at most 4096 per object; goblind refuses an object that
is not the plugin's (`403`), bad members (`400 invalid_members`) and more than 360 updates per hour per
plugin (`429`). Every change is audited (`live_object_update`, with the counts added and removed); sending
the same members again changes nothing and is not logged, so a plugin may simply send its full list on
every poll. An object starts empty and matches nothing until its plugin fills it; renaming it or giving it
to another plugin empties it. A plugin with `objects:write` decides who a policy on its objects matches, so
give it objects only in policies you would let it steer.

## Lifecycle

Config:

```json
"plugins": [
  {"name": "hello", "enabled": true, "capabilities": ["events:read"], "settings": {"greeting": "hi"},
   "secrets": {"token": "..."}, "policies": [14]}
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
| `GET /api/v1/plugins` | installed plugins: manifest, config state (`secrets_set` names the secrets that have a value, `policies` the switchable policy IDs), status (`running`, `stopped`, `failed`, `invalid`, `not_installed`) |
| `POST /api/v1/plugins/{name}/enable` `{capabilities?}` | enables it in the candidate (default: every capability it asks for) |
| `POST /api/v1/plugins/{name}/disable` | disables it in the candidate |
| `PUT /api/v1/plugins/{name}/settings` `{settings}` | validates against the schema and stores in the candidate (secrets apart) |
| `PUT /api/v1/plugins/{name}/policies` `{policies}` | the policy IDs it may switch (needs `policies:toggle` approved) |

Enable, disable and settings change the candidate; commit to apply.

A plugin can also serve its own API for the admin UI: it listens on `$GOBLIN_PLUGIN_API_SOCKET`
(`/run/goblin-plugin-<name>/api.sock`) and goblind forwards `/api/v1/plugins/<name>/api/<path>` to it
as `/<path>` after logging the admin in. Read-only admins only get GET; the plugin sees who asks in
`X-Goblin-User` and `X-Goblin-Role` and never the admin's cookies, tokens or CSRF header. Answers must be
JSON; cookies are stripped. Every change (non-GET call) is audited as `plugin_admin_call`.

## Go SDK

`github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk` is the supported way to write a plugin in Go (the
official plugins use it). A plugin can be written in any language: the SDK is a thin client for the HTTP API
above.

```go
package main

import (
	"context"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

func main() {
	pluginsdk.Run(func(ctx context.Context, p *pluginsdk.Plugin) error {
		greeting := p.Self.String("greeting", "hi")
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
				sys, err := p.Client.System(ctx)
				if pluginsdk.Denied(err) {
					p.Logger.Warn("stats:read is not approved")
					<-ctx.Done()
					return nil
				}
				if err != nil {
					p.Logger.Warn("reading the load failed", "error", err)
					continue
				}
				p.Logger.Info(greeting, "uptime_seconds", sys.UptimeSeconds, "sessions", sys.Sessions)
			}
		}
	})
}
```

- `pluginsdk.Run` sets up logging to stderr (the journal), cancels the context on SIGTERM, waits for goblind
  to answer (up to a minute) and reads the plugin's approved capabilities and settings into `p.Self`. An
  error returned from the function exits with status 1, so systemd restarts the plugin; return `nil` for
  a problem only the admin can fix (bad settings), after logging it, so the plugin does not restart over and
  over. Settings never change while a plugin runs: goblind restarts it.
- `p.Self.Has(pluginsdk.CapBlocksWrite)`, `p.Self.String/Int/Float/Bool/Strings(key, ...)` read
  capabilities and settings (secrets included).
- `p.Client` has a call per route: `Logs`, `System`, `WANs`, `Gateways`, `PolicyStats`, `FeedStats`,
  `StatsQuery`, `RunningConfig`, `Notify`, `Blocks`, `AddBlock`, `RemoveBlock`, `Devices`, `Policies`,
  `SwitchPolicy`, `LiveObjects`, `SetLiveObject`, and `Do` for anything else. Errors from goblind are `*pluginsdk.APIError` (`Status`,
  `Code`, `Message`); `pluginsdk.Denied(err)` is a capability that was not approved.
- `p.ServeAPI(ctx, handler)` serves the plugin's own API for its admin pages (see above), `p.Env.StateDir`
  is its persistent directory, `pluginsdk.InstalledVersion(name)` reads the installed manifest's version.

The SDK only adds calls and fields within one major version (`pluginsdk.Version`); its response types are
checked against goblind's in the SDK's tests.

## Write your own

`plugins/hello` is a complete, working plugin in about 80 lines: it reads one setting (`greeting`) and one
secret (`token`), asks for `stats:read`, and every minute logs the greeting with the box's uptime and
session count. It is built only on `pkg/pluginsdk`, is not built by a release and is not published — it is
here to be copied.

### 1. Copy the folder

```bash
cp -r plugins/hello plugins/myplugin
cd plugins/myplugin
```

Rename in three places, and they must all agree:

| Where | To |
|---|---|
| the folder | `plugins/myplugin` |
| `"name"` in `plugin.json` | `myplugin` — 1-32 of `a-z`, `0-9` and `-`, starting with a letter; goblind refuses a manifest whose name is not its directory |
| `goblin-gate-plugin-hello` and `hello` in `build-deb.sh` | `goblin-gate-plugin-myplugin` and `myplugin` |

`main.go`'s `main` never has to change: `pluginsdk.Run` does the startup, logging, signal handling and
waiting for goblind.

### 2. The manifest

```json
{
  "name": "hello",
  "version": "0.1.0",
  "description": "Example plugin: logs a greeting with the box's uptime and session count every minute",
  "min_goblind": "0.2.0~beta.1",
  "capabilities": ["stats:read"],
  "settings_schema": {
    "type": "object",
    "properties": {
      "greeting": {"type": "string", "default": "hello", "maxLength": 64,
                   "description": "the word to log next to the load"},
      "token": {"type": "string", "maxLength": 256, "secret": true,
                "description": "a secret, to show how secrets work; this plugin only logs whether one is set"}
    }
  }
}
```

- Ask for the **fewest capabilities** you need, and code as if the admin unchecks every one of them. A
  route you have no capability for answers `403`; `pluginsdk.Denied(err)` recognises that, and `hello`
  logs it once and stays up — goblind restarts the plugin when the admin changes its capabilities or
  settings, which is the only way settings change at all.
- `settings_schema` is a small JSON Schema subset (`string`, `integer`, `number`, `boolean`, `array` of
  strings; `enum`, `minimum`/`maximum`, `maxLength`, `default`, `required`). Settings the manifest does not
  list are rejected at commit, so the plugin can trust what it reads.
- `"secret": true` on a string property and **no default**: the value is stored apart from the other
  settings, redacted in the plugin list, in other plugins' view, in read-only hands and in the git backup.
  The plugin still gets it in `p.Self`; the UI only shows whether one is set. Log whether it is set, never
  its value — `hello` logs `token_set`.
- `min_goblind` stops an old goblind from starting a plugin built for a newer API.
- Read the manifest with the same checker goblind uses (`plugins.ParseManifest`, as `plugins/hello`'s tests
  do) so a bad manifest fails in your CI, not on someone's firewall.

### 3. The program

`plugins/hello/main.go` splits the behaviour out of `main` so it is testable:

```go
func main() { pluginsdk.Run(run) }

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	ticker := time.NewTicker(everyMinute)
	defer ticker.Stop()
	return loop(ctx, p.Logger, p.Self, p.Client, ticker.C, nil)
}
```

`loop` takes the logger, the settings, an interface with the one call it makes (`System`) and a tick
channel, so a unit test drives it with a fake — see `plugins/hello/main_test.go`, which tests the greeting,
the missing capability, a capability taken away while running, and a run against a fake goblind that serves
exactly the routes in `internal/plugins.Routes` (a URL the plugin is not allowed is a test failure).
Return `nil` after logging a problem only the admin can fix (bad settings, a capability you cannot work
without); return an error only for a real crash, because `Restart=on-failure` restarts the plugin on one.

### 4. Build the deb

`build-deb.sh` needs Linux (it runs `dpkg-deb`), so build it in a container or on the box:

```bash
bash plugins/hello/build-deb.sh dist
# dist/goblin-gate-plugin-hello_0.1.0_amd64.deb
```

It puts the manifest at `/usr/share/goblin-gate/plugins/hello/plugin.json` and the program at
`/usr/lib/goblin-gate/plugins/hello/run`, and makes the package depend on `goblin-gate (>= min_goblind)`.
`RUN_BINARY=/path/to/run` ships a program you built somewhere else; `VERSION=...` rebuilds it as part of a
release, in which case the release script sets the version, the manifest's version and `min_goblind` all to
the release's version.

Add your plugin to the release build only if it should be published — `plugins/hello` deliberately is not.

### 5. Install it on a box and watch the journal

```bash
sudo dpkg -i goblin-gate-plugin-hello_0.1.0_amd64.deb
```

goblind finds it without a restart: it reads `/usr/share/goblin-gate/plugins` every time it lists plugins.

Then in **System → Plugins** the plugin shows as `not installed` → `stopped`: press **Enable** (tick the
capabilities you grant it) and **Settings** to fill in the greeting, and **commit** — enabling and settings
change the candidate like everything else, and goblind starts the plugin as `goblin-plugin@hello.service`.
The same through the API (then commit as usual, in the GUI or with `goblin -c commit`):

```bash
curl -k -X POST -H "Authorization: Bearer $TOKEN" https://gate/api/v1/plugins/hello/enable
curl -k -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"settings":{"greeting":"hi from the workshop"}}' https://gate/api/v1/plugins/hello/settings
goblin -c "diff" && goblin -c "commit"
```

The plugin's log is in the journal, one line per record on stderr:

```bash
journalctl -u goblin-plugin@hello.service -f
#Nov 07 10:00:00 gate goblin-plugin[1234]: time=... level=INFO msg="hello: started" plugin=hello greeting=hello token_set=false interval_seconds=60
#Nov 07 10:01:00 gate goblin-plugin[1234]: time=... level=INFO msg=hello plugin=hello uptime_seconds=99 sessions=3
```

`systemctl status goblin-plugin@hello.service` shows its state; a plugin that exits with an error is
restarted, so a repeated crash means the plugin returned an error rather than a setting problem.

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
