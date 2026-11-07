# Plugins

Plugins add features without touching the core. Each is a Debian package named
`goblin-gate-plugin-<name>`, installed with `apt-get install` as root. The official ones:
[[Goblin Sentry]] and [[Home Assistant]].

## Safety

- Each plugin runs as its own throwaway unprivileged user, with no capabilities, a read-only system and a cap
  of one CPU core and 512 MB.
- It can only use the API calls whose **capabilities** you approve in **System → Plugins** (for example
  `events:read`, `blocks:write`). Every call is audited.
- Enabling, disabling and capability changes go through the candidate and a commit, like everything else.
- Plugins run code on your firewall: install only plugins you trust.

## Managing plugins

**System → Plugins** lists the installed plugins with their state (running, stopped, failed, invalid, not
installed). **Enable** shows the capabilities a plugin asks for; uncheck any you do not want to grant.
**Settings** opens the plugin's own settings form. Passwords and tokens in it are stored like every other
secret: the form only says whether one is set, and saving it with the field empty keeps it. A plugin with
the `policies:toggle` capability also gets **Policies it may switch**: it can turn those policies on and off
and no others, and each switch is a commit you find in **Revisions**.

## Writing a plugin

See the developer guide in the repository: `docs/dev/plugins.md`, with a minimal example in
`examples/plugins/hello`.
