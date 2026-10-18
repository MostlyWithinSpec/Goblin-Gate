# Tools: devices, simulator, capture, live traffic, snapshots

Built-in tools for finding out what is on your network and what the firewall does with it.

## Devices

**Network → Devices** lists every device the box has seen: its DHCP lease or the neighbor table, with IP,
MAC, the vendor from the MAC address, the interface and when it was last seen. Search by name, IP, MAC or
vendor, or filter to one interface. **Rename** gives a device a friendly name; it's used in Log & Report and
on the Live page too. **Forget** removes a device that is gone for good.

## Policy simulator

Ask the firewall "what would happen to this connection?" without sending any traffic.
**Policy & Objects → Simulate**: pick the incoming interface, source and destination address, protocol
and port. The answer is the policy that matches (or "denied by default" when none does), with NAT and any
security profiles. It reads the rules the box actually loads, so it also explains surprises.

In **Log & Report**, a denied connection has a **Why was this denied?** button that opens the simulator
filled in with that connection.

## Packet capture

**Log & Report → Packet capture** (admins only): choose an interface and optionally a host, port and
protocol, and how many packets or seconds to capture. Packets show live as they arrive; **Download pcap**
gives you a file for Wireshark. Only one capture runs at a time.

## Live traffic

**Log & Report → Live**: the devices using the most bandwidth right now (download and upload), updated
every few seconds. Click a device to see its open connections: protocol, remote address and port, state
and rate.

## Generated config

**System → Generated config** shows the files Goblin Gate writes for the system services (nftables,
DHCP, DNS, IPS, ...), for the running config or, with the diff, for your pending changes. Secrets such as
private keys are hidden.

## Snapshots

**System → Snapshots**: save the running (or pending) config under a name, such as `before-vlan-experiment`, then
experiment. **Restore** loads the snapshot as pending changes (nothing changes until you commit), so you
can review the diff first. Snapshots are included in backups.

## Commit with automatic rollback

In the commit dialog, **Roll back automatically unless I confirm within** (1–60 minutes) applies the change and
undoes it on its own unless you click **Confirm** in time. It's ticked for you when a change touches
interfaces, VLANs, policies, the admin port, VPN or routing: if the change cuts you off, wait and the box
comes back. See [[How Changes Work]].
