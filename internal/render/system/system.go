// Package system renders the box-level settings (task 047): hostname, time
// zone, NTP servers and SSH access. Hostname and time zone are small state
// files; the applier's "system" group applies them with hostnamectl and
// timedatectl when they change.
package system

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Output paths. Each lies in a directory an applier group owns.
const (
	StateDir         = "/var/lib/goblingate/system/"
	HostnamePath     = StateDir + "hostname"
	TimezonePath     = StateDir + "timezone"
	TimesyncdPath    = "/etc/systemd/timesyncd.conf.d/goblin.conf"
	SSHDPath         = "/etc/ssh/sshd_config.d/goblin.conf"
	AuthorizedKeyDir = "/etc/ssh/goblin_authorized_keys/"
	RootKeysPath     = AuthorizedKeyDir + "root"
	// APT sources (task 051): Debian with security updates and backports
	// (the offline installer configures none), Tailscale, and Goblin Gate's
	// own signed repository on the chosen channel.
	DebianSourcesPath = "/etc/apt/sources.list.d/goblin-debian.sources"
	GoblinSourcesPath = "/etc/apt/sources.list.d/goblin-gate.sources"
	// SecurityUpdatesPath configures unattended-upgrades (apt-daily-upgrade
	// timer): Debian security updates only, never Goblin Gate's packages.
	SecurityUpdatesPath = "/etc/apt/apt.conf.d/52goblin-security-updates"
	// RepoURL is Goblin Gate's APT repository (GitHub Pages of the public repo).
	RepoURL = "https://mostlywithinspec.github.io/Goblin-Gate/apt"
	// GoblinKeyring and TailscaleKeyring ship in the goblin-gate package.
	GoblinKeyring    = "/usr/share/keyrings/goblin-gate-archive.gpg"
	TailscaleKeyring = "/usr/share/keyrings/tailscale-archive-keyring.gpg"
)

// DefaultChannel is the update channel when system.update_channel is "".
const DefaultChannel = "beta"

const debianSources = header + `Types: deb
URIs: http://deb.debian.org/debian
Suites: bookworm bookworm-updates bookworm-backports
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://security.debian.org/debian-security
Suites: bookworm-security
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: https://pkgs.tailscale.com/stable/debian
Suites: bookworm
Components: main
Signed-By: ` + TailscaleKeyring + `
`

// securityUpdates renders the unattended-upgrades settings. "#clear" drops
// Debian's default origins (which add the main archive's point releases):
// only the security archive installs by itself. Goblin Gate's packages move
// only through the updater (snapshot and rollback). No automatic reboot: a
// kernel update shows "restart needed" on the dashboard.
func securityUpdates(mode string) string {
	on := "1"
	if mode == "manual" {
		on = "0"
	}
	return header + `APT::Periodic::Update-Package-Lists "` + on + `";
APT::Periodic::Unattended-Upgrade "` + on + `";
#clear Unattended-Upgrade::Origins-Pattern;
Unattended-Upgrade::Origins-Pattern {
	"origin=Debian,codename=${distro_codename}-security,label=Debian-Security";
};
Unattended-Upgrade::Package-Blacklist {
	"goblin-gate";
};
Unattended-Upgrade::Automatic-Reboot "false";
Unattended-Upgrade::OnlyOnACPower "false";
Dpkg::Options {
	"--force-confdef";
	"--force-confold";
};
`
}

const header = "# Managed by Goblin Gate (goblind): changes here are overwritten on the next commit.\n"

// sshdConfig: key-only logins; root may log in with a listed key. Debian's
// sshd_config includes sshd_config.d first, so these values win. TCP
// forwarding is local only and only to the web UI on the box itself (ssh -L
// to the admin port keeps working; the box is no stepping stone to other
// hosts, and nothing is forwarded back in). MaxAuthTries stays at the
// default, since a lower one locks out agents holding many keys. No SHA-1
// or 64-bit-tag MACs.
func sshdConfig(adminPort int) string {
	if adminPort == 0 {
		adminPort = 443
	}
	p := strconv.Itoa(adminPort)
	return sshdBase + "AllowTcpForwarding local\nPermitOpen localhost:" + p + " 127.0.0.1:" + p + " [::1]:" + p +
		"\nAllowStreamLocalForwarding no\nPermitTunnel no\n"
}

const sshdBase = header + `PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
X11Forwarding no
AllowAgentForwarding no
LoginGraceTime 30
MACs hmac-sha2-256-etm@openssh.com,hmac-sha2-512-etm@openssh.com,umac-128-etm@openssh.com,hmac-sha2-256,hmac-sha2-512,umac-128@openssh.com
AuthorizedKeysFile .ssh/authorized_keys ` + AuthorizedKeyDir + `%u
`

// Renderer implements commit.Renderer.
type Renderer struct{}

// Name returns "system".
func (Renderer) Name() string { return "system" }

// Render writes every file on every commit, so a removed setting is applied
// too (an empty NTP list falls back to the distribution's servers).
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	s := cfg.System
	tz := s.Timezone
	if tz == "" {
		tz = "UTC"
	}
	out := map[string][]byte{
		HostnamePath:  []byte(s.Hostname + "\n"),
		TimezonePath:  []byte(tz + "\n"),
		TimesyncdPath: []byte(header + "[Time]\nNTP=" + strings.Join(s.NTPServers, " ") + "\n"),
		SSHDPath:      []byte(sshdConfig(s.AdminPort)),
	}
	var keys strings.Builder
	keys.WriteString(header)
	for _, k := range s.SSHKeys {
		if err := config.CheckSSHKey(k.Key); err != nil {
			// Validate rejects these; never write one sshd could misread.
			return nil, fmt.Errorf("render system: ssh key %q: %w", k.Name, err)
		}
		fmt.Fprintf(&keys, "# %s\n%s\n", k.Name, strings.TrimSpace(k.Key))
	}
	out[RootKeysPath] = []byte(keys.String())
	channel := s.UpdateChannel
	if channel == "" {
		channel = DefaultChannel
	}
	out[DebianSourcesPath] = []byte(debianSources)
	out[SecurityUpdatesPath] = []byte(securityUpdates(s.SecurityUpdates))
	repo := RepoURL
	if s.UpdateURL != "" {
		repo = strings.TrimSuffix(s.UpdateURL, "/")
	}
	out[GoblinSourcesPath] = []byte(header + `Types: deb
URIs: ` + repo + `
Suites: ` + channel + `
Components: main
Signed-By: ` + GoblinKeyring + `
`)
	return out, nil
}
