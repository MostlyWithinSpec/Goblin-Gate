# UniFi

The UniFi plugin reads what your UniFi Network application knows about the devices on your network and
brings it to the gate:

- **Where each device is connected**, shown next to it under **Devices**: the switch and port, or the access
  point, Wi-Fi network (SSID) and band, plus its UniFi network and VLAN.
- **Firewall objects that follow your UniFi networks.** Make a **live address** ([[Objects]]) and the
  plugin keeps it filled with the addresses of the clients on a UniFi network, on a Wi-Fi network, or
  marked with a note. Use it in policies like any address; no commit is needed when clients come and go.

The plugin only reads. It never changes anything on your controller, and if the controller can't be
reached, the addresses and device details stay as they were until it's back.

It works with a UniFi OS console (Cloud Key Gen2, Dream Machine, Cloud Gateway) and with a self-hosted
UniFi Network application, version 9 or later.

## Install

As root on the box (see [[Remote Access: SSH, API and CLI|Remote-Access-SSH-API-CLI]]):

```sh
apt-get update && apt-get install goblin-gate-plugin-unifi
```

## Make an API key

The key has to come from the console itself:

1. Open the console's own address in a browser (such as `https://192.168.1.2`), not unifi.ui.com.
2. Open **UniFi Network → Settings → Control Plane → Integrations**, and create an API key.

A key made on unifi.ui.com (the Site Manager) only works with Ubiquiti's cloud. The console refuses it, and
the plugin's log says so.

## Set up the plugin

**System → Plugins → UniFi → Enable**, and approve the capabilities:

- `devices:annotate`: show where devices are connected
- `objects:write`: fill live addresses

Approve one or both. Then fill in the **Settings**:

| Setting | Example |
| --- | --- |
| url | `https://192.168.1.2` for a console, `https://unifi.lan:8443` for a self-hosted application |
| api key | the key from above |
| fingerprint | The console's certificate is self-signed: pin its SHA-256 fingerprint (your browser shows it under the padlock → certificate details), and the gate trusts that certificate only. |
| ca | or, for a certificate from your own CA: that CA's certificate. Leave both empty for a public CA. |
| site | `default` unless you run several sites: the short name in the console's address bar (`/manage/<site>/`) |
| network objects | `iot=IoT`: the clients on the UniFi network **IoT** go into the live address **iot** |
| ssid objects | `guests=Home Guest`: the clients on the Wi-Fi network **Home Guest** go into **guests** |
| interval seconds | how often it reads the controller, 60 by default |

Commit. Within a minute **Devices** shows where your devices are connected.

## Live addresses

1. **Network → Addresses → Add**: type **live**, plugin **unifi**, a name such as `iot`.
2. Tell the plugin which clients belong in it, in any mix of:
   - a **network object** or **SSID object** setting, as above;
   - a **note** on the client in UniFi (Client → Settings → Note) containing `gg-iot`. A note may name
     several addresses: `gg-iot gg-cameras`.
3. Use the address in policies and commit.

Only connected clients count: a client that leaves drops out at the next poll. The plugin uses each
client's IPv4 address and any global IPv6 addresses UniFi reports.

## Troubleshooting

The plugin's log is under **System → Plugins → UniFi → Log**.

| Log message | What to do |
| --- | --- |
| "the console refused the API key" | Make the key on the console itself (see above), not on unifi.ui.com. |
| "server certificate fingerprint … is not the one in the settings" | The console has a new certificate (after an update or a reset). Pin the new fingerprint. |
| "not found (is site … right?)" | Check the site name in the console's address bar. |
| "not started: approve objects:write, devices:annotate or both" | Approve at least one capability under Enable. |
| "no live objects yet" | Fine if you only want device details. Otherwise add a live address with plugin **unifi**. |
