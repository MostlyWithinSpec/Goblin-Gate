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
