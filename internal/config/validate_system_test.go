package config

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// sshKey builds a well-formed public key line of the given type.
func sshKey(typ, comment string) string {
	var b []byte
	for _, part := range [][]byte{[]byte(typ), make([]byte, 32)} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(part)))
		b = append(b, part...)
	}
	return strings.TrimSpace(typ + " " + base64.StdEncoding.EncodeToString(b) + " " + comment)
}

func TestValidateSystemSettings(t *testing.T) {
	good := sshKey("ssh-ed25519", "me@laptop")
	cases := []struct {
		name string
		mod  func(*Config)
		want []string // substrings expected in the errors; none = valid
	}{
		{"defaults", func(*Config) {}, nil},
		{"time zone", func(c *Config) { c.System.Timezone = "America/Chicago" }, nil},
		{"unknown time zone", func(c *Config) { c.System.Timezone = "Mars/Olympus" }, []string{"system.timezone", "unknown time zone"}},
		{"path as time zone", func(c *Config) { c.System.Timezone = "/etc/passwd" }, []string{"system.timezone"}},
		{"ntp ip and host", func(c *Config) { c.System.NTPServers = []string{"192.168.1.1", "time.cloudflare.com"} }, nil},
		{"bad ntp", func(c *Config) { c.System.NTPServers = []string{"not a host"} }, []string{"system.ntp_servers[0]"}},
		{"ssh key", func(c *Config) { c.System.SSHKeys = []SSHKey{{Name: "laptop", Key: good}} }, nil},
		{"duplicate ssh key", func(c *Config) {
			c.System.SSHKeys = []SSHKey{{Name: "a", Key: good}, {Name: "b", Key: good}}
		}, []string{"system.ssh_keys[1].key", "already listed"}},
		{"ssh key type mismatch", func(c *Config) {
			k := sshKey("ssh-ed25519", "")
			c.System.SSHKeys = []SSHKey{{Name: "x", Key: "ssh-rsa " + strings.Fields(k)[1]}}
		}, []string{"does not match its type"}},
		{"ssh key with options", func(c *Config) {
			c.System.SSHKeys = []SSHKey{{Name: "x", Key: `command="sh" ` + good}}
		}, []string{"unsupported SSH key type"}},
		{"ssh key newline", func(c *Config) {
			c.System.SSHKeys = []SSHKey{{Name: "x", Key: good + "\nssh-ed25519 AAAA"}}
		}, []string{"one line"}},
		{"bad key name", func(c *Config) { c.System.SSHKeys = []SSHKey{{Name: "my key", Key: good}} }, []string{"system.ssh_keys[0].name"}},
		{"metrics allow list", func(c *Config) {
			c.System.Metrics = Metrics{Enabled: true, Allow: []string{"192.168.1.0/24", "10.0.0.5/32", "fd00::/8"}}
		}, nil},
		{"metrics bad cidr", func(c *Config) {
			c.System.Metrics = Metrics{Enabled: true, Allow: []string{"192.168.1.0/24", "192.168.1.5"}}
		}, []string{"system.metrics.allow[1]", "invalid CIDR"}},
		{"metrics too many", func(c *Config) {
			for i := range 33 {
				c.System.Metrics.Allow = append(c.System.Metrics.Allow, fmt.Sprintf("10.0.%d.0/24", i))
			}
		}, []string{"system.metrics.allow", "at most 32"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mod(c)
			var msgs []string
			for _, e := range Validate(c) {
				msgs = append(msgs, e.Path+": "+e.Message)
			}
			all := strings.Join(msgs, "\n")
			if tc.want == nil && len(msgs) > 0 {
				t.Fatalf("unexpected errors:\n%s", all)
			}
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("missing %q in:\n%s", w, all)
				}
			}
		})
	}
}

func TestValidHostPort(t *testing.T) {
	for s, want := range map[string]bool{
		"se-sto-wg-001.example.net:51820":  true,
		"203.0.113.7:51820":                true,
		"[2001:db8::1]:51820":              true,
		"vpn.example.net:0":                false,
		"vpn.example.net:65536":            false,
		"vpn.example.net:051820":           false,
		"vpn.example.net":                  false,
		":51820":                           false,
		"vpn.example.net:51820\n[Network]": false,
		"vpn example.net:51820":            false,
		"[203.0.113.7]:51820":              false,
	} {
		if got := validHostPort(s); got != want {
			t.Errorf("validHostPort(%q) = %v, want %v", s, got, want)
		}
	}
	for s, want := range map[string]bool{"vpn.example.net": true, "vpn.example.net.": true, "203.0.113.7": true, "2001:db8::1": true, "vpn.example.net\nx": false, "a b": false, "": false} {
		if got := validHost(s); got != want {
			t.Errorf("validHost(%q) = %v, want %v", s, got, want)
		}
	}
}
