# Interfaces and VLANs

**Network → Interfaces.**

## Interfaces

Each physical port you use is an interface with a **role**:

- `wan`: an internet uplink. Address by DHCP or static. Traffic leaving a WAN can be masqueraded (NAT).
- `lan`, `dmz`, `guest`, `mgmt`: internal networks with a static address (the box is their gateway).

**Allowed access** says what the box itself answers on that interface: `https` (web UI and API), `ssh`,
`ping`. Keep `https` and `ssh` on a trusted network only. DNS and DHCP are opened automatically where the
box serves them.

Which port is which? The console menu option 1 assigns WAN and LAN ports; Network → Interfaces shows the
kernel device (for example `ens192`) and its link state.

## VLANs

A VLAN is a tagged network on a parent interface, for example Wi-Fi or IoT on VLAN 2 of the LAN port. Give it
a name, the parent, the VLAN ID (1-4094) and an address such as `192.168.2.1/24`. Your switch or access point
must tag that VLAN on the port to the firewall.

A new network needs three more things to be useful:

1. **DHCP** for it (Network → DHCP).
2. **Policies** for its traffic (traffic that matches no policy is denied).
3. Allowed access if its clients should reach the web UI (usually not for guest/IoT).

## IPv6

Per interface: `none`, `dhcp6` (WAN: address plus prefix delegation from the ISP), `static`, or `delegated`
(LAN side: takes a /64 out of the prefix the WAN received and announces it with router advertisements).

If your ISP offers no IPv6, leave IPv6 off: a `delegated` interface without a prefix keeps waiting for one.
