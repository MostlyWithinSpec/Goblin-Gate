# Site-to-site VPN

Join two or more Goblin Gates (home, a relative's house, a holiday flat, a small office) into one private
network. You pair the gates with two codes you copy and paste. There is no central server, and you never
type a WireGuard key.

Each site you connect directly gets its own WireGuard tunnel, named after the site. That name is a **zone
in policies**, like a VLAN. Nothing crosses a tunnel until a policy allows it.

## Before you pair

- **The networks must not overlap.** If both houses use `192.168.1.0/24`, renumber one of them first
  (**Network → Interfaces**). The gate refuses a pairing whose networks overlap its own and tells you which
  ones clash.
- **At least one gate must be reachable from the internet.** Under **VPN → Sites → This gate**, give the
  public DNS name or address of a gate that has one (a [DDNS](DHCP-and-DNS) name works). A gate behind NAT
  without a port forward, or behind CGNAT, leaves the field empty. It then dials out to the other one and
  keeps the tunnel open.
- Choose **Networks this gate shares**: the interfaces, VLANs or address objects the other sites may route
  to. Policies on this gate still decide what they can actually reach.

## Pairing two gates

1. On the first gate, **VPN → Sites → Invite a site**. Name the other site (for example `cabin`) and make
   the code. It is valid for 15 minutes, 1 hour or 1 day, and it works only once.
2. Send the code to the other gate's admin privately (Signal, in person). Anyone who has it can join your
   network until it is used or expires.
3. On the second gate, **VPN → Sites → Join a site**. Paste the code and check the networks it brings.
   Name the first site (for example `home`) and join. The page shows a **reply code**.
4. Paste the reply code on the first gate, in the same dialog or later under **Open invitations →
   Accept a reply**.
5. **Commit on both gates.** The tunnel starts within seconds. **VPN → Sites** then shows the last
   handshake, the traffic, and whether the other gate answers a ping.

The invitation also carries a one-time secret. The reply is signed with it, so a reply cannot be changed
on the way. Both gates also derive the tunnel's WireGuard preshared key from that secret.

## Policies

Add policies under **Policy** with the site as the incoming or outgoing interface, for example:

| Name | From | To | Service | Action |
| --- | --- | --- | --- | --- |
| lan-to-cabin | lan | cabin | all | accept |
| cabin-ssh-to-nas | cabin | lan → nas | ssh | accept |

The other gate needs its own policies for the way back: each gate guards its own networks. Try a flow first
with the [policy simulator](Tools-Devices-Simulator-Capture).

## Hub and spoke

Three sites do not all need tunnels to each other. If the office is connected to the cabin, home can reach
the office **through** the cabin:

- On home, **Add a site reached through another**: name `office`, reached through `cabin`, the office
  gate's tunnel address (shown on the office's Sites page) and its networks.
- On the cabin, which carries the traffic: policies from `home` to `office` and back.
- On the office, add `home` reached through `cabin` the same way.

Traffic from a site reached through another arrives on that site's zone (`cabin` in the example).

## BGP and OSPF

With **Announce the site networks into BGP and OSPF** on, a gate that runs
[dynamic routing](Dynamic-Routing) tells its other routers about the remote sites' networks.

## Good to know

- The tunnel's UDP port (51830 and up) is opened on the WAN automatically. If the reachable gate is behind
  another router, forward that port to it.
- A site's name cannot be changed later, because it also names the tunnel's keys. To rename a site, remove it
  and pair again.
- Removing a site removes only this gate's side. Remove it on the other gate too.
- Restoring a backup on new hardware keeps the sites, but the tunnel keys stay on the old box. Remove those
  sites and pair again.
