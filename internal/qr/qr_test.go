package qr

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// samples covers versions 1 to 20.
func samples() map[string]string {
	return map[string]string{
		"tiny":  "hi",
		"short": "https://example.com/goblin",
		"wg":    "[Interface]\nPrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=\nAddress = 10.66.0.2/32\nDNS = 10.66.0.1\n\n[Peer]\nPublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\nEndpoint = gate.example.net:51820\nAllowedIPs = 10.66.0.0/24, 192.168.1.0/24\nPersistentKeepalive = 25\n",
		"big":   strings.Repeat("goblin gate qr test 0123456789 ", 27),
	}
}

func TestEncodeSizes(t *testing.T) {
	want := map[string]int{"tiny": 1, "short": 2, "wg": 0, "big": 20}
	for name, s := range samples() {
		c, err := Encode([]byte(s))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if w := want[name]; w != 0 && c.Version != w {
			t.Errorf("%s: version %d, want %d", name, c.Version, w)
		}
		if c.Size != 17+4*c.Version || len(c.Modules) != c.Size {
			t.Errorf("%s: size %d", name, c.Size)
		}
		if !strings.HasPrefix(c.SVG(4), "<svg") {
			t.Error("svg")
		}
	}
	if _, err := Encode(make([]byte, 900)); err != ErrTooLong {
		t.Fatalf("too long: %v", err)
	}
}

func TestRSKnownVector(t *testing.T) {
	// ISO/IEC 18004 annex example: "01234567" in version 1-M has these
	// data codewords and EC codewords (EC level independent of the RS math)
	data := []byte{0x10, 0x20, 0x0c, 0x56, 0x61, 0x80, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11}
	want := []byte{0xa5, 0x24, 0xd4, 0xc1, 0xed, 0x36, 0xc7, 0x87, 0x2c, 0x55}
	got := rsRemainder(data, 10)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rs %x, want %x", got, want)
		}
	}
}

// TestWritePNGs writes the samples as PNGs for an external decoder
// (QR_PNG_DIR; scripts run zbarimg on them).
func TestWritePNGs(t *testing.T) {
	dir := os.Getenv("QR_PNG_DIR")
	if dir == "" {
		t.Skip("set QR_PNG_DIR")
	}
	for name, s := range samples() {
		c, _ := Encode([]byte(s))
		f, _ := os.Create(filepath.Join(dir, name+".png"))
		png.Encode(f, c.Image(6))
		f.Close()
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(s), 0o644)
	}
}
