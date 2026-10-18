# Web filter and application control

Both work by name (DNS and the TLS server name), so they also work for HTTPS without breaking encryption.
Attach a profile to a policy; it applies to that policy's source network.

## Web filter

**Security Profiles → Web Filter.** Categories from the UT1 category database (adult, gambling, malware,
phishing, social networks, ...), each set to:

- `allow` (default), `monitor` (log only), `warn` (block page with a *Continue* button) or `block`.

Plus **block domains** and **allow domains** that override the categories (allow wins). Blocked and warned
sites show a block page. The page has a lookup box: "which category is example.com?".

## Application control

**Security Profiles → Application Control.** Allow or block applications (Steam, Roblox, Netflix, TikTok,
...) or whole groups (Gaming, Streaming, Social), recognized by their domains. An app setting overrides its
group. Blocked attempts show in Log & Report with the app's name.

Use a [[schedule|Firewall-Policies]] on the policy for "no games after 22:00".
