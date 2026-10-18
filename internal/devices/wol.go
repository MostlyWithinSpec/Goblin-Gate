package devices

import (
	"fmt"
	"net"
	"net/netip"
)

// sendUDP sends a UDP datagram to addr; tests replace it.
var sendUDP = func(addr netip.AddrPort, data []byte) error {
	c, err := net.Dial("udp", addr.String())
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(data)
	return err
}

// MagicPacket builds the 102-byte Wake-on-LAN magic packet for mac:
// six 0xFF bytes followed by the MAC address repeated sixteen times.
func MagicPacket(mac net.HardwareAddr) ([]byte, error) {
	if len(mac) != 6 {
		return nil, fmt.Errorf("devices: MAC must be 6 bytes, got %d", len(mac))
	}
	pkt := make([]byte, 102)
	for i := 0; i < 6; i++ {
		pkt[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		copy(pkt[6+i*6:], mac)
	}
	return pkt, nil
}

// Wake sends a WoL magic packet to the given broadcast address on UDP port 9.
func Wake(mac net.HardwareAddr, broadcast netip.Addr) error {
	pkt, err := MagicPacket(mac)
	if err != nil {
		return err
	}
	return sendUDP(netip.AddrPortFrom(broadcast, 9), pkt)
}
