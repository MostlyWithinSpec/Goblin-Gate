package apply

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// group is a set of managed files that are checked and activated together.
// Groups activate in the order of the groups slice.
type group struct {
	name   string
	prefix string   // directory the group owns; every file must be under it
	also   []string // more directories the group owns (same check and activation)
	// check validates a candidate file before anything is written; nil = no check.
	// The argument is the path of a temporary copy of the new content.
	check func(tmp string) []string
	// checkCopy turns a file into what check sees; nil = the file as is.
	checkCopy func(data []byte) ([]byte, error)
	// checkStage selects new files of the group that the check of its other
	// files reads (unbound's RPZ zone files): they are staged next to the
	// checked copy and references to their real path point there instead,
	// because nothing is written to its real place before every check passed.
	checkStage func(file string) bool
	// links returns the network devices a file needs; activate waits until
	// they exist (networkd creates VLAN devices asynchronously). nil = none.
	links func(data []byte) []string
	// activate makes the data plane pick up the written files; files lists
	// the group's current paths and may be empty. nil = nothing to run.
	activate func(files []string) [][]string
	// prepare returns a command to run before activate for one file whose
	// content goes from old to new (old nil = created, new nil = removed);
	// nil = nothing to run. Its failure is logged, not fatal.
	prepare func(file string, old, new []byte) []string
}

// groups lists every directory the applier may write to. Renderers that add
// a new output location (suricata, wireguard, ...) must add a group here;
// any other path is refused.
var groups = []group{
	{
		name:   "sysctl",
		prefix: "/etc/sysctl.d/",
		// systemd-sysctl ships with systemd; the sysctl tool (procps) may not
		// be installed.
		activate: func(files []string) [][]string {
			if len(files) == 0 {
				return nil
			}
			return [][]string{append([]string{"/usr/lib/systemd/systemd-sysctl"}, files...)}
		},
	},
	{
		name:   "networkd",
		prefix: "/etc/systemd/network/",
		// networkctl reload re-reads .network/.netdev files and reconfigures
		// every link whose file was added, changed or removed (systemd >= 248).
		activate: func([]string) [][]string { return [][]string{{"networkctl", "reload"}} },
		// networkctl reload never changes an existing netdev (a VLAN keeps its
		// old id) and never deletes one whose file is gone: delete the link.
		prepare: func(file string, old, new []byte) []string {
			if !strings.HasSuffix(file, ".netdev") || old == nil {
				return nil
			}
			o := netdevKeys(old)
			if new != nil {
				n := netdevKeys(new)
				// A WireGuard netdev is recreated on any change too: its peers
				// and port live in the netdev (a peer change drops the tunnel
				// for a moment; clients reconnect on their own).
				if o["Name"] == n["Name"] && o["Kind"] == n["Kind"] && o["Id"] == n["Id"] && o["Kind"] != "wireguard" {
					return nil
				}
			}
			if o["Name"] == "" {
				return nil
			}
			return []string{"networkctl", "delete", o["Name"]}
		},
	},
	{
		name:   "nft",
		prefix: "/etc/nftables.d/",
		check:  func(tmp string) []string { return []string{"nft", "-c", "-f", tmp} },
		// Each file replaces its own tables atomically (see internal/render/nft).
		// With no file left the loaded ruleset stays: never drop the firewall.
		activate: func(files []string) [][]string {
			var cmds [][]string
			for _, f := range files {
				cmds = append(cmds, []string{"nft", "-f", f})
			}
			return cmds
		},
	},
	{
		name:   "kea",
		prefix: "/etc/kea/",
		check:  func(tmp string) []string { return []string{"kea-dhcp4", "-t", tmp} },
		// kea-dhcp4 -t fails on interfaces that do not exist yet (a new VLAN
		// only appears after networkd's activation), so check a copy without them.
		checkCopy: keaWithoutInterfaces,
		links:     keaInterfaces,
		activate: func(files []string) [][]string {
			if len(files) == 0 {
				return nil // kea cannot run without its config file
			}
			return [][]string{{"systemctl", "reload-or-restart", "kea-dhcp4-server.service"}}
		},
	},
	{
		name:   "unbound",
		prefix: "/etc/unbound/unbound.conf.d/",
		// DNS filter zones (RPZ); unbound loads them at start.
		also: []string{"/var/lib/unbound/rpz/"},
		// Only config files are checked; zone files are generated from
		// validated names. The config names the zone files, so a new
		// profile's zone is staged for the check (unbound-checkconf opens it).
		checkStage: func(file string) bool { return strings.HasSuffix(file, ".zone") },
		check: func(tmp string) []string {
			if !strings.HasSuffix(tmp, ".conf") {
				return nil
			}
			return []string{"unbound-checkconf", tmp}
		},
		// Restart, not reload: a reload (SIGHUP) runs after unbound dropped
		// privileges, so it cannot bind port 53 on a newly added address.
		activate: func([]string) [][]string { return [][]string{{"systemctl", "restart", "unbound.service"}} },
	},
	{
		// One Suricata per IPS profile in use (task 031). Not checked with
		// suricata -T: that loads the whole rule set (tens of seconds) on
		// every commit; the rendered YAML is tested against Suricata in
		// linux-test.ps1 instead. nft queues with bypass, so traffic flows
		// (uninspected) while an instance restarts.
		name:   "suricata",
		prefix: "/etc/suricata/goblin/",
		activate: func(files []string) [][]string {
			var cmds [][]string
			for _, f := range files {
				if inst, ok := strings.CutSuffix(path.Base(f), ".yaml"); ok {
					unit := "goblin-suricata@" + inst + ".service"
					cmds = append(cmds, []string{"systemctl", "enable", unit}, []string{"systemctl", "restart", unit})
				}
			}
			return cmds
		},
		prepare: func(file string, old, new []byte) []string {
			inst, ok := strings.CutSuffix(path.Base(file), ".yaml")
			if !ok || new != nil {
				return nil
			}
			return []string{"systemctl", "disable", "--now", "goblin-suricata@" + inst + ".service"}
		},
	},
	{
		// Tailscale (task 041): the rendered desired state is pushed to
		// tailscaled through its LocalAPI; a failure (enabled but tailscaled
		// missing) rolls the commit back.
		name:     "tailscale",
		prefix:   "/etc/goblingate/tailscale/",
		activate: func([]string) [][]string { return [][]string{{"/usr/sbin/goblind", "tailscale-apply"}} },
	},
	{
		// Hostname and time zone (task 047): state files, applied with
		// hostnamectl/timedatectl when their content changes.
		name:   "system",
		prefix: "/var/lib/goblingate/system/",
		prepare: func(file string, _, new []byte) []string {
			v := strings.TrimSpace(string(new))
			if new == nil || v == "" {
				return nil
			}
			switch path.Base(file) {
			case "hostname":
				return []string{"hostnamectl", "set-hostname", v}
			case "timezone":
				return []string{"timedatectl", "set-timezone", v}
			}
			return nil
		},
	},
	{
		// NTP servers for systemd-timesyncd (task 047).
		name:   "timesync",
		prefix: "/etc/systemd/timesyncd.conf.d/",
		activate: func(files []string) [][]string {
			return [][]string{ifUnit("systemd-timesyncd.service", "try-restart")}
		},
	},
	{
		// APT sources (task 051): read by apt on the next update. The
		// installer's "deb cdrom:" line in sources.list is commented out here
		// (on a running box, never by the package: during installation the
		// remaining packages still come from the CD).
		name:   "apt",
		prefix: "/etc/apt/sources.list.d/",
		activate: func([]string) [][]string {
			return [][]string{{"sh", "-c", `[ ! -f /etc/apt/sources.list ] || sed -i 's|^deb cdrom:|# deb cdrom:|' /etc/apt/sources.list`}}
		},
	},
	{
		// SSH (task 047): key-only logins and root's keys. try-reload: a box
		// without a running sshd is not an error.
		name:     "ssh",
		prefix:   "/etc/ssh/sshd_config.d/",
		also:     []string{"/etc/ssh/goblin_authorized_keys/"},
		activate: func([]string) [][]string { return [][]string{ifUnit("ssh.service", "try-reload-or-restart")} },
	},
}

// ifUnit runs `systemctl <verb> <unit>` only when the unit is installed, so a
// box without it (no sshd, a different time daemon) can still commit.
func ifUnit(unit, verb string) []string {
	return []string{"sh", "-c", "! systemctl cat " + unit + " >/dev/null 2>&1 || systemctl " + verb + " " + unit}
}

// keaFile is the part of a Kea DHCPv4 config that names interfaces.
type keaFile struct {
	Dhcp4 struct {
		InterfacesConfig struct {
			Interfaces []string `json:"interfaces"`
		} `json:"interfaces-config"`
	} `json:"Dhcp4"`
}

func keaInterfaces(data []byte) []string {
	var f keaFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	var out []string
	for _, i := range f.Dhcp4.InterfacesConfig.Interfaces {
		if dev, _, _ := strings.Cut(i, "/"); dev != "*" && dev != "" {
			out = append(out, dev)
		}
	}
	return out
}

func keaWithoutInterfaces(data []byte) ([]byte, error) {
	var f map[string]any
	if json.Unmarshal(data, &f) != nil {
		return data, nil // let kea-dhcp4 -t report it
	}
	d, _ := f["Dhcp4"].(map[string]any)
	if d == nil {
		return data, nil
	}
	if ic, ok := d["interfaces-config"].(map[string]any); ok {
		ic["interfaces"] = []string{}
	}
	if subnets, ok := d["subnet4"].([]any); ok {
		for _, s := range subnets {
			if m, ok := s.(map[string]any); ok {
				delete(m, "interface")
			}
		}
	}
	return json.Marshal(f)
}

// groupFor returns the index of the group owning p, after checking that p is
// a clean absolute path with a file name.
func groupFor(p string) (int, error) {
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.HasSuffix(p, "/") {
		return 0, fmt.Errorf("%q: not a clean absolute file path", p)
	}
	for i, g := range groups {
		for _, dir := range append([]string{g.prefix}, g.also...) {
			if strings.HasPrefix(p, dir) && len(p) > len(dir) {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("%q: outside the directories the applier manages", p)
}

// netdevKeys returns the Key=value settings of a systemd .netdev file; the
// renderer never repeats a key, so sections can be ignored.
func netdevKeys(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && !strings.HasPrefix(k, "#") {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}
