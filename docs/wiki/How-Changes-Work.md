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

## Templates

**Policy & Objects → Templates** sets up common things in one go. Each template asks a few questions, shows
the changes it would make, and saves them to the candidate like any other edit; nothing goes live until you
commit.

| Template | What it creates |
|---|---|
| Guest network | A VLAN with its own DHCP pool and a policy to the internet only (optionally with a DNS filter profile). Your own networks stay out of reach. |
| IoT network | The same, plus a policy that lets one of your networks (usually `lan`) reach the IoT devices, but not the other way round. |
| Port forward for some places only | A port forward limited to the countries and networks you list, with the address objects for them. |
| Network through a VPN provider | A policy sending one network through a VPN provider tunnel, with the kill switch on. If you had no gateways yet, your WAN gets one and stays the default route, so other traffic never fails over into the tunnel. |
| Bedtime for some devices | A schedule, an address group of the devices and a deny policy at the top that cuts their internet access at night. |

A template never overwrites anything: if a name it would create already exists, it asks for another one. To
undo a template before committing, discard the candidate; after committing, roll back the revision.

Over the API: `GET /api/v1/templates` lists them with their questions,
`POST /api/v1/templates/<id>/preview {"values": {...}}` shows the changes, and `.../apply` saves them to the
candidate.
