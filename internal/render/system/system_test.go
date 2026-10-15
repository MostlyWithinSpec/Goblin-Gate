package system

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func key(comment string) string {
	var b []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), make([]byte, 32)} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(part)))
		b = append(b, part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(b) + " " + comment
}

func TestRender(t *testing.T) {
	cfg := config.Default()
	cfg.System.Hostname = "gate"
	cfg.System.Timezone = ""
	cfg.System.NTPServers = []string{"0.pool.ntp.org", "192.168.1.1"}
	cfg.System.SSHKeys = []config.SSHKey{{Name: "laptop", Key: key("me@laptop")}}
	out, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		HostnamePath:  "gate\n",
		TimezonePath:  "UTC\n", // empty = UTC
		TimesyncdPath: "[Time]\nNTP=0.pool.ntp.org 192.168.1.1\n",
		SSHDPath:      "PermitRootLogin prohibit-password\n",
		RootKeysPath:  "# laptop\n" + key("me@laptop") + "\n",
		// no channel set = beta (pre-1.0); Debian security updates are on
		GoblinSourcesPath: "Suites: beta\nComponents: main\nSigned-By: " + GoblinKeyring,
		DebianSourcesPath: "Suites: bookworm-security",
	}
	for path, frag := range want {
		if !strings.Contains(string(out[path]), frag) {
			t.Errorf("%s: missing %q in:\n%s", path, frag, out[path])
		}
	}
	if len(out) != len(want) {
		t.Errorf("rendered %d files, want %d", len(out), len(want))
	}
	if !strings.Contains(string(out[SSHDPath]), "PasswordAuthentication no") {
		t.Error("password login must be off")
	}
}

func TestRenderRefusesBadKey(t *testing.T) {
	cfg := config.Default()
	cfg.System.SSHKeys = []config.SSHKey{{Name: "x", Key: "ssh-ed25519 AAAA\nPermitRootLogin yes"}}
	if _, err := (Renderer{}).Render(cfg); err == nil {
		t.Fatal("rendered a key that could inject sshd options")
	}
}
