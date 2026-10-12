package config

import (
	"encoding/base64"
	"encoding/binary"
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
