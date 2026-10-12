package config

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
	"time"
	_ "time/tzdata" // time zone names are checked the same way on every OS
)

// sshKeyTypes are the OpenSSH public key types sshd accepts for root login.
var sshKeyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true, "ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true, "sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// checkSystemSettings validates the box-level settings applied by the
// system renderer (task 047): time zone, NTP servers and SSH keys.
func (v *validator) checkSystemSettings() {
	s := v.cfg.System
	if s.Timezone != "" {
		if strings.ContainsAny(s.Timezone, " \t\n") || strings.HasPrefix(s.Timezone, "/") {
			v.add("system.timezone", "invalid time zone %q", s.Timezone)
		} else if _, err := time.LoadLocation(s.Timezone); err != nil {
			v.add("system.timezone", "unknown time zone %q: use an IANA name such as Europe/Berlin or America/Chicago", s.Timezone)
		}
	}
	for i, n := range s.NTPServers {
		if _, err := netip.ParseAddr(n); err != nil && !validHostname(n) {
			v.add(fmt.Sprintf("system.ntp_servers[%d]", i), "invalid NTP server %q: want a hostname or IP address", n)
		}
	}
	seen := map[string]bool{}
	for i, k := range s.SSHKeys {
		p := fmt.Sprintf("system.ssh_keys[%d]", i)
		if !nameRe.MatchString(k.Name) {
			v.add(p+".name", "invalid key name %q", k.Name)
		}
		if err := CheckSSHKey(k.Key); err != nil {
			v.add(p+".key", "%v", err)
			continue
		}
		body := strings.Fields(k.Key)[1]
		if seen[body] {
			v.add(p+".key", "this key is already listed")
		}
		seen[body] = true
	}
}

// CheckSSHKey checks an OpenSSH public key line: a known type, a base64 body
// whose embedded type matches, an optional comment, and nothing that could
// add sshd options (one line, no leading options field).
func CheckSSHKey(key string) error {
	if strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("an SSH key must be one line")
	}
	f := strings.Fields(key)
	if len(f) < 2 {
		return fmt.Errorf("not an SSH public key: want \"<type> <base64> [comment]\"")
	}
	if !sshKeyTypes[f[0]] {
		return fmt.Errorf("unsupported SSH key type %q", f[0])
	}
	raw, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(raw) < 4 {
		return fmt.Errorf("the SSH key is not valid base64")
	}
	n := binary.BigEndian.Uint32(raw[:4])
	if int(n) > len(raw)-4 || !bytes.Equal(raw[4:4+n], []byte(f[0])) {
		return fmt.Errorf("the SSH key body does not match its type %s", f[0])
	}
	return nil
}
