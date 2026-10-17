# Updates and backups

## Updates

**System → Updates.** Goblin Gate installs from its own signed package repository:

| Channel | What you get |
|---|---|
| stable | releases |
| beta | pre-releases and releases (the default before 1.0) |
| nightly | every change on the main branch: for testing |

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
