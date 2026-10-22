# IPS (intrusion detection and prevention)

**Security Profiles → IPS.** Goblin Gate runs Suricata inline with the free Emerging Threats Open rules.

## Profiles

An IPS profile is attached to policies (field *IPS*): only traffic those policies accept is inspected.

| Setting | Meaning |
|---|---|
| Mode | `prevent` (drop matching traffic) or `detect` (alert only) |
| Block severity | in prevent mode, drop rules of severity 1 (high only), 2 (high and medium, default) or 3 (all) |
| Categories | limit the rules to ET categories (MALWARE, SCAN, EXPLOIT, ...); empty = the default set |
| Suppress | silence one rule (by SID) everywhere or only for one source or destination address |

**Start in detect mode**, watch the alerts for a few days, suppress the false positives, then switch to
prevent. Or keep detect mode and let [[Goblin Sentry]] turn alerts into temporary blocks you approve.

## Good to know

- Each profile in use runs its own Suricata, with one queue per CPU core (at most 4).
- If Suricata stops or falls behind, traffic **keeps flowing uninspected** (fail-open): the IPS never takes
  your internet down.
- Alerts and drops show in **Log & Report** (kind `ips`); from an alert you can suppress its rule.
- The rule set updates daily.
- IPS costs CPU: on a 2-core box, inspect the internet policies, not LAN-to-LAN traffic.

## ET Pro rules

By default the IPS uses the free **ET Open** rules. If you have an **ET Pro** subscription from Proofpoint,
paste its code under **Security → IPS → Rules** and commit: the rules download again right away from ET Pro,
which includes ET Open plus the paid rules. *Remove code* (and commit) goes back to ET Open.

The code is a secret: read-only users and limited API tokens can't see it, it never appears on a command
line, in logs or in error messages, and the file suricata-update reads it from is readable by root only.
The page shows which rule set is in use, how many rules it has, and why the last download failed if it did
(a wrong or expired code shows up there as a download error).
