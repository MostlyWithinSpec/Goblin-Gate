# How changes work

Goblin Gate works like enterprise firewalls: you edit a **candidate** config, review the difference, and
**commit** it. Until you commit, nothing on the network changes.

## Candidate, commit, revision

1. Every page edits the **candidate**. Saving a form or deleting an entry only changes the candidate.
2. The **Changes** button (top right) shows how many changes are waiting. Click it to see the diff.
3. **Commit** checks the whole candidate, writes the service configs, and applies them. If anything fails,
   the box goes back to the previous config on its own.
4. Each commit becomes a numbered **revision** (System → Revisions). **Rollback** on any revision loads it
   and commits it again.

## When a commit is refused

A commit is refused, and nothing is applied, when the candidate has an error. The dialog lists each one
under the change list, for example:

```
policy 14 "wifi-rule-14": unknown src_addr reference "Kids_Devices" (deleted or renamed?)
```

That one means policy 14 still uses an address that no longer exists. Fix it (edit or delete policy 14)
and commit again. Pages refuse to delete an object that something still uses and tell you what uses it,
so this mostly happens after imports or API edits.

**Discard** (API: `POST /api/v1/commit/discard`) throws the candidate away and starts again from the running
config.

## Commit with automatic rollback

For risky changes (WAN settings, policies that could cut off your own access), commit with a confirmation
window: the change is applied, and **if you don't confirm within N minutes it is rolled back on its own**.
A banner with a **Confirm** button shows while a commit waits.

From the API or CLI:

```sh
curl -k -X POST https://192.168.1.1/api/v1/commit \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"comment":"new wifi rules","confirm_minutes":5}'
# check that everything still works, then:
curl -k -X POST https://192.168.1.1/api/v1/commit/confirm -H "Authorization: Bearer $TOKEN"
```

## Revisions keep everything

The revision history keeps the full config of every commit, so "what did it look like last Tuesday" is one
click away. Backups (see [[Updates and Backups]]) include the history.
