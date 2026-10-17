# Troubleshooting

## The commit button does nothing / the commit is refused

Open **Changes**: the errors are listed in red under the change list, each naming what is wrong, for example
`policy 14 "wifi-rule-14": unknown src_addr reference "Kids_Devices"`. Fix that item and commit again. The
API shows the same list: `POST /api/v1/config/validate`.

## I can't find how to delete something

- Policies: the **⋮** button at the right end of the row, or right-click the row → Delete.
- Objects, profiles, feeds: the trash icon at the end of the row. If it refuses, the message says what still
  uses the object.

## SSH says "Permission denied"

Password logins over SSH are always off. Add a key under System → Admins → SSH keys and commit (see
[[Remote Access: SSH, API and CLI]]).

## A device can't reach something

1. **Log & Report**: filter by the device's IP. A denied connection shows which policy denied it (or none:
   no policy matched, so it was denied by default).
2. Give the deny policy `log: deny` if it is not logged yet.
3. Remember: policies match top to bottom, first match wins.
4. Traffic to the box itself (web UI, SSH) depends on the interface's allowed access, not policies.

## A website is blocked that shouldn't be

- Log & Report shows DNS filter, web filter and app control blocks with the list or category.
- Add the domain to the profile's **allow domains** (DNS filter or web filter), commit.
- The threat feed **lookup** box tells you which feed lists an address or domain.

## A threat feed shows an error

- `download: HTTP 404/403` or a timeout: the list is gone or blocks automated downloads. Remove it.
- `rejected: the update has no valid entries`: wrong type or format, or an empty list.
- `this is a domain list`: change the feed type to `domain` and use it in a DNS filter profile.
- `rejected: ... more than 90% fewer`: the download looked broken; the last good copy is still in use.

## Wi-Fi devices lost access after isolating the VLAN

Look for denies of your isolation policy in Log & Report (filter by its ID) and add an exception policy
above it for what the device needs (for example Wi-Fi → Home Assistant).

## iCloud Private Relay says it is not available

On networks with a DNS filter, the box asks Private Relay to turn off so the filter can't be bypassed. Remove
the DNS filter profile from that network's policy if you want Private Relay there.

## The box is unreachable after a change

If you committed with a confirmation window, wait: it rolls back on its own. Otherwise, from the console:
menu option 4 (shell), then `goblin -c "rollback <revision>"` with the last good revision (System →
Revisions lists them; `goblin -c "history"` on the shell).

## Reporting a bug

Open an issue with: the Goblin Gate version (System → About), what you did, what you expected, and the
relevant Log & Report lines or `journalctl -u goblind` output. Never post backups, tokens or config files
without removing secrets.
