# Updates and backups

## Updates

**System → Updates.** Goblin Gate installs from its own signed package repository:

| Channel | What you get |
|---|---|
| stable | releases: tested longest, changes least |
| beta | the feature channel: new features as soon as they are released, plus every stable release (the default before 1.0) |

Every build in either channel went through the full test run first: there are no untested nightly builds.
A box that was on the old nightly channel follows beta.

The box checks every 6 hours; an `update_available` notification can tell you (System → Notifications).
**Install** saves the current version first; if the new one does not start properly, the box **reinstalls
the saved version on its own**.

On the box as root, the same thing: `apt-get update && apt-get install goblin-gate` (plus any
`goblin-gate-plugin-*` you use).

## Backups

**System → Backup.**

- **Download backup**: a `.ggbackup` file (a zip) with the running config and the full revision history.
  Keep it private: it is your whole network layout. Private keys of WireGuard and other secrets created
  on the box are not in it; after a restore on new hardware, recreate WireGuard peers.
- **Restore**: upload a backup. It is loaded into the **candidate** and the diff is shown; nothing changes
  until you commit.

## Revisions

**System → Revisions**: every commit, with who, when and a comment. **Rollback** brings back any earlier
revision (it is committed as a new revision, so you can undo the rollback too).

## UPS (power failures)

With a UPS, Goblin Gate tells you when the power goes and shuts down cleanly before the battery is empty, so
the disk and the config are never cut off mid-write. Set it up under **System → UPS**.

- **UPS on USB**: plug the UPS's USB cable into the box and pick *USB*. Most UPSes that speak the USB HID
  power device standard work (APC Back-UPS, CyberPower, Eaton and many others). The UPS is watched by NUT
  (Network UPS Tools) on the box; it is only reachable from the box itself.
- **UPS watched by another machine**: if a NAS or server already runs a NUT server for the UPS, pick
  *Remote* and enter its address, the UPS's name there (`upsc -l <server>` lists them) and a NUT user with
  its password. On a Synology NAS, enable *Network UPS server* and use the user `monuser` with password
  `secret`; add the firewall's address to the NAS's list of permitted devices.

**When to shut down**: by default the box shuts down when the UPS itself reports a low battery. You can set
your own limit instead, as a battery percentage or the runtime left (whichever comes first). With a remote
server, the box shuts down at your limit even if the server keeps running.

The status page and the **UPS** dashboard widget show whether the box runs on mains or battery, the charge,
the runtime left and the load. Notification rules can send `ups_on_battery`, `ups_back` and `ups_low` to any
channel (System → Notifications). Keep in mind that a notification only gets out if your modem is on the UPS
too.

The UPS password is stored like other secrets: hidden from read-only users and limited tokens, and left out
of backups shared through config history.
