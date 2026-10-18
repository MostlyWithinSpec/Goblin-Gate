# First setup

1. Plug a computer into the LAN port. It gets an address from the box (192.168.1.100-199 by default).
2. Open `https://192.168.1.1` (or the address the console shows). The certificate is self-signed the first
   time: accept it in your browser.
3. The **setup wizard** walks through: admin password → WAN (DHCP or a static address) → LAN (address and
   DHCP range) → security level → review, then commits the first config.
   - Security level **Off**: just a firewall and router. **Basic** (good for most homes): blocks known
     attacks and dangerous websites by name. **Strict**: Basic plus web filtering and application control
     (may block sites you want). You can change all of it later.

Coming from pfSense? Skip most of this: run the wizard, then **System → Import** (see
[[Migrating from pfSense]]).

## After the wizard: a 5-minute checklist

- **Dashboard**: the WAN shows an address, the gateway answers.
- **System → General**: hostname, time zone, NTP and DNS servers.
- **System → Admins**: add an **SSH key** if you want SSH access. Password logins over SSH are always off
  (see [[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]).
- **System → Updates**: pick the channel (stable, beta or nightly).
- **System → Notifications**: add a channel (email, Discord, ntfy, Telegram, webhook) so the box can tell
  you about WAN outages, failed logins and updates.
- **System → Backup**: download a first backup and keep it somewhere safe.
- **Security Profiles**: turn on at least a threat feed and a DNS filter
  (see [[Threat Feeds and DNS Filter]]).
