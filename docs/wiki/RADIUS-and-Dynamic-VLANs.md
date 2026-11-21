# RADIUS and Dynamic VLANs

Switches and Wi-Fi access points can ask Goblin Gate who may connect and which VLAN each device
belongs in. The gate runs [FreeRADIUS](https://www.freeradius.org/) for this and makes every
decision itself, from your configuration and the device inventory.

- **MAC authentication (MAB):** for printers, TVs and other devices without 802.1X. The switch
  sends the device's MAC address; the gate answers with the device's VLAN.
- **802.1X:** for laptops and phones. Users log in with a user name and password (PEAP-MSCHAPv2)
  or a certificate (EAP-TLS). The gate's server certificate comes from the [[Internal CA]].
- **Unknown devices** can be refused, or put in a quarantine VLAN, or let in on the port's own
  VLAN.
- **Moving a device** to another VLAN takes effect at once: the gate tells the switch or access
  point to disconnect it (RFC 5176), and it reconnects into the new VLAN.

## Install FreeRADIUS

FreeRADIUS is recommended by the package but not required. If it is missing:

```sh
sudo apt-get install freeradius freeradius-rest
```

Debian's own `freeradius.service` stays switched off (masked): Goblin Gate runs FreeRADIUS as
`goblin-radius.service` with its own configuration, under an AppArmor profile. A commit that turns
RADIUS on without FreeRADIUS installed fails and says what to install.

## Set it up

1. **VLANs.** Create the VLANs devices go into under Network → Interfaces, for example `iot`
   (20), `staff` (40) and `quarantine` (99), and commit. The switch ports or SSIDs must carry
   these VLANs as tagged VLANs, on the switch's uplink to the gate too.
2. **Internal CA** (for 802.1X only). Turn it on under System → Internal CA and commit; it makes
   its root certificate a few seconds later. Then turn on 802.1X.
3. **RADIUS settings:**
   - **Clients:** each switch or access point (or a subnet of them) with a shared secret of at
     least 8 characters, no spaces or quotes. The *Disconnect port* is where the device takes
     Disconnect requests (3799 unless your vendor says otherwise).
   - **MAC authentication** and/or **802.1X**.
   - **Unknown devices:** reject, quarantine (pick the VLAN) or accept.
   - **Groups** give their users a VLAN; **users** have a password and a group.
4. Commit. RADIUS listens on UDP 1812 (authentication) and 1813 (accounting), open to the
   configured clients only and never on the WAN.

On the switch or access point, point RADIUS authentication and accounting at the gate's LAN
address with the same secret, and turn on dynamic VLAN assignment ("RADIUS-assigned VLAN",
"dynamic VLAN"). Accounting is optional but tells the gate which port or access point each device
is on.

## Which VLAN a device gets

1. The device's own VLAN from the device inventory (Network → Devices: open the device, then **Move to VLAN**). Setting it there
   needs no commit, and a device that is connected is disconnected so it rejoins at once.
2. Otherwise, for 802.1X, the VLAN of the user's group.
3. Otherwise, for MAC authentication, the *unknown devices* choice: refused, quarantine VLAN, or
   no VLAN (the port's own).

The reply carries the standard attributes `Tunnel-Type = VLAN`, `Tunnel-Medium-Type = IEEE-802`
and `Tunnel-Private-Group-Id = <VLAN id>`, which every switch and access point with dynamic VLANs
understands.

A VLAN that is removed from the configuration counts as none: those devices are treated as
unknown until you give them another one.

## Per-device Wi-Fi passwords (PPSK)

Some access points (for example hostapd with `wpa_psk_radius`, and several enterprise brands) can
take a Wi-Fi password from RADIUS per device. Give a device its own password under Network → RADIUS →
**Wi-Fi passwords per device**; the gate sends it as `Tunnel-Password` with the MAC authentication answer. Devices
without one use the SSID's normal password. Passwords are kept on the box (not in the
configuration or backups) and only users who may change network settings can see them.

## 802.1X with certificates (EAP-TLS)

Issue a certificate with the **Certificate** button in the user's row under Network → RADIUS → Users (or `POST /api/v1/radius/users/{name}/certificate`).
You get the certificate, its private key and the CA root in PEM form; the gate does not keep the
key, so save it then. Import them on the device (most systems want a `.p12`; make one with
`openssl pkcs12 -export -in user.pem -inkey user.key -certfile ca.pem -out user.p12`).

The gate accepts only certificates it issued, for a user who is still enabled. Revoking a user's
certificates, disabling or removing the user refuses them from the next login on.

Devices must trust the internal CA's root to check the gate's server certificate: see
[[Internal CA]] for installing it.

## Watching it

- **Recent authentications:** each request with the switch or access point, the MAC, the user,
  the method, the result, the VLAN and the reason for a refusal ("unknown device", "wrong
  password", "certificate revoked or not issued by this box", ...). The last 1000 are kept in
  memory.
- **Sessions:** the devices let in, with the switch and port or the access point and SSID, from
  accounting. The device inventory shows the same as annotations.
- **Disconnect** a device from its session row to make it authenticate again.

## Troubleshooting

- *No answer at all:* the switch's address must match a client entry (the request's source
  address, not its NAS-IP-Address) and the secret must be the same on both sides. FreeRADIUS
  ignores requests from unknown addresses or with a wrong secret, and logs them:
  `journalctl -u goblin-radius`.
- *Device refused as "unknown device":* give it a VLAN in the device inventory, or set unknown
  devices to quarantine.
- *802.1X fails on the client before a password is asked:* the device does not trust the server
  certificate. Install the internal CA root, or set the network to trust it (on Android: CA
  certificate → the installed root, domain = the gate's host name).
- *Moving a device does nothing until it reconnects:* the switch did not ack the Disconnect.
  Check that it accepts Disconnect (CoA) requests from the gate and that the Disconnect port and
  secret match.

## Quarantine

With *unknown devices* set to quarantine, a device that is in the quarantine VLAN right now gets a
**Quarantine** badge under Network → Devices. **Approve** moves it to the VLAN you pick; the gate
disconnects it and it rejoins there. Network → RADIUS → **Recent authentications** shows every
answer with its reason, the switch or access point and the port, and can be filtered.
