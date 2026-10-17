# Goblin Sentry and G.O.B.L.I.N.

Goblin Sentry is the first official plugin: it watches IPS alerts, groups them into **incidents**, and
answers with a small set of actions (temporary block, watch, notify, ignore, escalate). It never runs
commands on the box.

## Install

As root on the box (see [[Remote Access: SSH, API and CLI]]):

```sh
apt-get update && apt-get install goblin-gate-plugin-sentry
```

Then **System → Plugins → Sentry → Enable**, approve the capabilities it asks for, and commit. Sentry needs
the IPS turned on (an IPS profile on your internet policies, detect mode is enough) to have alerts to work
with. The **Goblin SOC** section appears in the sidebar.

## Modes

| Mode | What happens |
|---|---|
| off | nothing |
| shadow | records what it would do, does nothing |
| **approve** (default) | blocks wait in the **Queue** for your answer |
| auto | rules act; blocks happen on their own only where you clicked "Always do this" |

Your own hosts are never blocked, nor the box, its gateways, DNS and NTP servers, or the address you are
administering from. Blocks are temporary (1 minute to 1 week) and lifted by the kernel when they expire.

## Pages

- **Queue**: incidents waiting for you. **Approve** (block for a while), **Ignore** (false positive), or
  **Always do this** for this kind of alert.
- **Incidents**: everything, with the "why" for each decision (direction, which of your hosts, by name,
  whether the address was seen or watched before).
- **Rules**: what Sentry does for which signatures, severities or categories.
- **Blocks**: active temporary blocks; add or lift one by hand.
- **Learning**: G.O.B.L.I.N., below.

## G.O.B.L.I.N. (the local decision model)

G.O.B.L.I.N. learns from **your own answers** how you handle the incidents the rules would ask you about. It
runs on the box (no GPU, no cloud) and never changes itself:

- After about 40 answers, **Train** builds a numbered version with an **evaluation report**: accuracy per
  action, and how often it would have ignored something you blocked.
- A version is used only after you **Accept** it; **Roll back** returns to the previous one.
- It is asked only when the rules would ask you, and it **never blocks on its own**. Set to *suggest*, it
  shows its advice in the queue; set to *act*, it may ignore, watch or notify above confidence thresholds you
  choose.
- Wrong lessons can be removed from the learned list.
