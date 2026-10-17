# Remote access: SSH, API and CLI

## SSH: keys only

SSH logs in as **root with a key**. **Password logins over SSH are always off**, so the right password still
gives `Permission denied`. That is on purpose.

1. On your computer, make a key if you have none: `ssh-keygen -t ed25519`.
2. **System → Admins → SSH keys → Add**: paste the contents of `~/.ssh/id_ed25519.pub`, give it a name, save,
   **commit**.
3. `ssh root@192.168.1.1` (the interface you connect from needs `ssh` in its allowed access).

Locked out of the web UI? On the console, menu option 3 resets the admin password.

## The REST API

Everything the web UI does goes through `https://<box>/api/v1/...`.

- **System → API Tokens → Create**: the token is shown once. Send it as `Authorization: Bearer <token>`.
  Revoke tokens you no longer use (and any token you pasted somewhere public).
- Changing requests with a body need `Content-Type: application/json`.

Examples:

```sh
T=gg_xxxxxxxx
curl -k -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/system/status
curl -k -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/config/diff        # pending changes
curl -k -X POST -H "Authorization: Bearer $T" https://192.168.1.1/api/v1/config/validate
```

Editing goes through the candidate (`GET/PUT/PATCH /api/v1/config/candidate`) and
`POST /api/v1/commit` (with `confirm_minutes` for an automatic rollback, see [[How Changes Work]]).

## The CLI

`goblin` on the box (as root it talks to goblind over a local socket) or from another machine with
`--server https://<box> --token <token>`. FortiOS-style commands:

```
goblin -c "show system interface"
goblin -c "config system interface edit lan set address 192.168.1.1/24 next end"
goblin -c "diff"                               # pending changes
goblin -c "commit"                             # also: confirm, discard
goblin -c "history"                            # revisions
goblin -c "rollback 12"
goblin -f changes.gob                          # a script of commands
goblin import-pfsense config.xml --devices wan=eth0,lan=eth1
goblin --set-password admin                    # as root: reset a password
```

Run `goblin` without arguments for an interactive shell.
