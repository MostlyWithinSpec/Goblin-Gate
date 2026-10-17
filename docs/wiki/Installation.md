# Installation

## What you need

- A 64-bit x86 machine or VM with **two network ports** (WAN and LAN; VLANs can add more networks on the LAN
  port). 2 CPU cores and 2 GB RAM run a home network; give it **4 GB or more** if you use the IPS and a large
  DNS blocklist (a list of ~850 000 domains takes about 1 GB in the DNS resolver).
- 16 GB of disk or more.
- BIOS or UEFI. Secure Boot works (the installer uses Debian's signed boot chain).

Tested on QEMU/KVM, VirtualBox and VMware ESXi (use VMXNET 3 ports on ESXi).

## Install

1. Download `goblin-gate-<version>-amd64.iso` and its `.sha256` from the
   [releases page](https://github.com/MostlyWithinSpec/Goblin-Gate/releases) and check the checksum.
2. Boot the ISO (write it to a USB stick with Rufus, balenaEtcher or `dd` for a physical box).
3. Pick the disk. **Everything on it is erased.**
4. When it reboots, the console shows a menu and the web address of the box.

## The console menu

On the box's screen (or the VM console) you get:

```
 1) Assign interfaces (WAN/LAN ports)
 2) Set the LAN IP address
 3) Reset the admin password
 4) Shell
 5) Reboot
 6) Power off
 0) Refresh
```

- **1** tells Goblin Gate which port is the WAN and which is the LAN. Tip: unplug and replug a cable, then
  choose 0 to see which port shows link up.
- **2** if `192.168.1.1` clashes with your network.
- **3** if you are locked out of the web UI.

Then continue with [[First Setup]].

## Updating instead of reinstalling

Installed boxes update from the package repository: see [[Updates and Backups]]. You never need the ISO
again for an update.
