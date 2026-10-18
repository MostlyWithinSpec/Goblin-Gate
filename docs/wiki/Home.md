# Goblin Gate wiki

Goblin Gate is a Linux firewall for homelabs, self-hosters and small offices: an enterprise-style config
model (candidate, diff, commit, rollback), a modern web UI and a REST API for everything.

This wiki is the user guide. Each page answers "where do I find it, what does it do, what should I watch
out for". If something here does not match what you see, please open an issue.

## Start here

1. [[Installation]]: download the ISO, install it on a VM or a box.
2. [[First Setup]]: the setup wizard and the first things to check.
3. [[How Changes Work]]: **read this one.** Nothing you change in the UI takes effect until you commit,
   and every commit can be undone.

## Everyday tasks

| I want to... | Page |
|---|---|
| Allow or block traffic between networks | [[Firewall Policies]] |
| Name a device or a network once and reuse it | [[Objects]] |
| Add a VLAN or change an interface | [[Interfaces and VLANs]] |
| Open a port to a server at home | [NAT, Port Forwards and UPnP](NAT-Port-Forwards-and-UPnP) |
| Give out addresses, name devices, local DNS | [[DHCP and DNS]] |
| Block ads, malware and phishing | [[Threat Feeds and DNS Filter]] |
| Detect and stop attacks | [[IPS]] and [[Goblin Sentry]] |
| Filter websites or apps for a network | [[Web Filter and App Control]] |
| Reach home from outside | [[VPN]] |
| Use two internet connections | [[Multi-WAN and Routing]] |
| Update, back up, restore | [[Updates and Backups]] |
| Log in over SSH, use the API or CLI | [Remote Access: SSH, API and CLI](Remote-Access-SSH-API-CLI) |
| See devices, test a rule, capture packets, take snapshots | [Tools: Devices, Simulator, Capture](Tools-Devices-Simulator-Capture) |
| Graph the box in Grafana | [Prometheus and Grafana](Prometheus-and-Grafana) |
| Move from pfSense | [[Migrating from pfSense]] |
| Something does not work | [[Troubleshooting]] |

## Finding your way in the web UI

The sidebar groups pages like this (a page with tabs lists them in brackets):

- **Dashboard**
- **Network** (Interfaces, Addresses, Address Groups, Services, Service Groups, Port Forwards, UPnP, DHCP,
  DNS, Gateways, Gateway Groups, Static Routes)
- **Policy & Objects**: Firewall Policy, Schedules
- **Security Profiles**: Threat Feeds, DNS Filter, Web Filter, Application Control, IPS
- **VPN**: Tailscale, WireGuard, VPN Providers
- **Log & Report**
- **System** (General, Admins, API Tokens, Backup, Import, About), Revisions, Notifications, Plugins, Updates
- **Goblin SOC** (only with the Goblin Sentry plugin): Queue, Incidents, Rules, Blocks, Learning

The **Changes** button at the top right shows how many changes wait to be committed.

> Wide tables (Firewall Policy especially) scroll sideways. The row actions (⋮) stay pinned at the right
> edge; right-clicking a policy row opens the same menu.
