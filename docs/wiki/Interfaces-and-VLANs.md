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

## Smart queue

When the line is busy — a big download, a backup running — normal buffering lets packets pile up in the
WAN port's buffer. The throughput looks fine, but every voice call, game packet or video frame waits behind
the backlog: that is **bufferbloat**, and it shows up as lag exactly when you are doing something else on
the same line.

Smart queue (CAKE) fixes this by shaping the link to its real speed: traffic above the limit is queued in
small, fair slices instead of dumped into the buffer, so latency stays flat even at full load. Enable it on
a WAN interface; the list shows an **SQM** badge and whether it is currently active.

How to pick the numbers:

1. With smart queue **off**, run a speed test (for example fast.com or speedtest.net) close to the box.
2. Enter about **90%** of the measured download and upload in Mbit/s. Leaving 10% headroom keeps the shaper
   from cutting into real throughput when the line fluctuates.

How to test it:

- Run a bufferbloat test (for example `bufferbloat.com` or the speedtest.net "Jitter" section) with
  another device saturating the line — for example a big download on a second machine.
- **Before** enabling smart queue, expect latency to climb into the hundreds of milliseconds under load.
- **After** enabling it and committing, the same test should hold near your baseline (a few ms over the
  unloaded number) while the other device still gets nearly full speed.

The speeds are stored with the interface even when smart queue is disabled, so turning it back on later does
not require typing them again.
