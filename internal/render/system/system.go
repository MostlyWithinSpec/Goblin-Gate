// Package system renders the box-level settings (task 047): hostname, time
// zone, NTP servers and SSH access. Hostname and time zone are small state
// files; the applier's "system" group applies them with hostnamectl and
// timedatectl when they change.
package system

import (
	"fmt"
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
)

const header = "# Managed by Goblin Gate (goblind): changes here are overwritten on the next commit.\n"

// sshdConfig: key-only logins; root may log in with a listed key. Debian's
// sshd_config includes sshd_config.d first, so these values win.
const sshdConfig = header + `PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
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
		SSHDPath:      []byte(sshdConfig),
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
	return out, nil
}
