# Home Assistant

The Home Assistant plugin puts the gate into [Home Assistant](https://www.home-assistant.io/) through MQTT:
the entities appear by themselves (MQTT discovery), with no custom component to install on the Home
Assistant side.

| Entity | What it shows |
|---|---|
| **WAN** *name* | connected or not, with its addresses and gateway as attributes |
| **WAN** *name* **IPv4 address** | the public address (handy for automations when it changes) |
| **Gateway** *name*, **latency**, **packet loss** | per monitored gateway ([[Multi-WAN and Routing]]) |
| **CPU**, **Memory used**, **Sessions**, **Boot time** | the box's load and when it last started |
| a tracker per device you pick | **home** while the device is on your network, **not home** after it is gone for a while |
| **Policy** *name* switches | turn the firewall policies you allow on and off, for example guest Wi-Fi internet |

The gate connects out to the broker; nothing is opened on the gate.

## Before you start

You need an MQTT broker that Home Assistant uses. Most people run the **Mosquitto broker** add-on
(Settings → Add-ons) with the **MQTT** integration (Settings → Devices & services); MQTT discovery is on by
default. Create a Home Assistant user for the gate (the add-on accepts Home Assistant users as MQTT logins),
or add a login in the broker's own configuration.

## Install

As root on the box (see [[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]):

```sh
apt-get update && apt-get install goblin-gate-plugin-homeassistant
```

Then **System → Plugins → Home Assistant**:

1. **Enable** and approve the capabilities. Each one adds a group of entities; leave one out and its
   entities stay away:
   - `stats:read`: WAN, gateway and box sensors
   - `devices:read`: device trackers
   - `policies:toggle`: policy switches
2. **Settings**: the **broker** (`192.168.1.20` or `homeassistant.home.lan:1883`), the **user name** and
   **password**, and **TLS** if your broker uses it (port 8883). The password is stored like every other
   secret: it is not shown again, and saving the form with it empty keeps it.
3. **Commit.** The device **Goblin Gate** appears in Home Assistant under the MQTT integration within a few
   seconds.

Several gates? Give each its own **base topic** (`goblin_gate_office`) and **device name**.

## Presence

Add the MAC addresses of the phones to track under **devices** (copy them from **Network → Devices**). A
device counts as home while the gate has seen it within **away minutes** (default 10). Phones sleep and
leave Wi-Fi now and then; raise the value if a tracker flips to not home while you are in.

Many phones use a random MAC address per network: keep it fixed for your home Wi-Fi in the phone's Wi-Fi
settings (Private address off, or "use device MAC"), or the tracker loses the phone when the address
changes.

## Policy switches

Pick the policies Home Assistant may switch under **Policies it may switch** in the plugin's settings. Only
those get a switch, and the gate refuses every other one. A typical use is guest Wi-Fi: switch off the policy
that lets the guest network reach the internet, and guests are offline until you switch it back on, from a
dashboard button, a schedule or an automation.

Every switch is a commit of just that change: it shows in **Revisions** as done by `plugin:homeassistant`,
can be rolled back, and leaves your own uncommitted edits alone. While a commit is waiting for you to
confirm it, switches are refused (the switch in Home Assistant flips back).

**List only policies whose both states are safe.** Whoever can use the switch in Home Assistant, or publish
to the broker as the gate's topics, can choose either. Never list a policy that opens your network when it
is on, and keep the broker locked down:

- require logins (no anonymous access)
- if the broker supports ACLs, allow only the gate's user to publish to `goblin_gate/#` and Home Assistant
  to publish to `goblin_gate/policy/+/set`

## Troubleshooting

The plugin's log: `journalctl -u goblin-plugin@homeassistant` over SSH.

| Symptom | Cause |
|---|---|
| "connection refused: bad user name or password" | the MQTT login is wrong |
| "connection refused: not authorized" or "refused a subscription" | the broker's ACL does not allow the gate's user |
| "no broker set" | enter the broker in the settings |
| entities are **unavailable** | the gate lost its broker connection (the broker marks it offline); it reconnects by itself |
| a group of entities is missing | its capability is not approved |
| a policy switch flips back | the gate refused it: a commit is waiting to be confirmed, or more than 60 switches in an hour |
