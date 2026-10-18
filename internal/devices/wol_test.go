package devices

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
)

func TestMagicPacket(t *testing.T) {
	mac, err := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := MagicPacket(mac)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) != 102 {
		t.Fatalf("packet length = %d, want 102", len(pkt))
	}
	// Six 0xFF bytes.
	for i := 0; i < 6; i++ {
		if pkt[i] != 0xFF {
			t.Fatalf("byte %d = %#x, want 0xff", i, pkt[i])
		}
	}
	// MAC repeated sixteen times.
	want := append([]byte{}, mac...)
	for i := 0; i < 16; i++ {
		got := pkt[6+i*6 : 6+(i+1)*6]
		if !bytes.Equal(got, want) {
			t.Fatalf("repeat %d = %x, want %x", i, got, want)
		}
	}
}

func TestMagicPacketBadMAC(t *testing.T) {
	for _, n := range []int{0, 4, 5, 7} {
		if _, err := MagicPacket(make(net.HardwareAddr, n)); err == nil {
			t.Errorf("len %d: got nil error, want error", n)
		}
	}
}

func TestWake(t *testing.T) {
	mac, _ := net.ParseMAC("00:11:22:33:44:55")
	bcast := netip.MustParseAddr("192.168.1.255")

	var gotAddr netip.AddrPort
	var gotData []byte
	orig := sendUDP
	sendUDP = func(addr netip.AddrPort, data []byte) error {
		gotAddr = addr
		gotData = append([]byte{}, data...)
		return nil
	}
	defer func() { sendUDP = orig }()

	if err := Wake(mac, bcast); err != nil {
		t.Fatal(err)
	}
	wantAddr := netip.AddrPortFrom(bcast, 9)
	if gotAddr != wantAddr {
		t.Fatalf("sent to %s, want %s", gotAddr, wantAddr)
	}
	want, _ := MagicPacket(mac)
	if !bytes.Equal(gotData, want) {
		t.Fatal("payload does not match the magic packet")
	}
}

func TestWakeBadMAC(t *testing.T) {
	orig := sendUDP
	sendUDP = func(netip.AddrPort, []byte) error { t.Fatal("sendUDP must not be called"); return nil }
	defer func() { sendUDP = orig }()

	if err := Wake(make(net.HardwareAddr, 4), netip.MustParseAddr("10.0.0.255")); err == nil {
		t.Fatal("got nil error for a bad MAC")
	}
}
