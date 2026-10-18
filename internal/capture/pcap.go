package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
)

// pcap stream format (what tcpdump -w - writes): a 24-byte global header,
// then per packet a 16-byte record header and the captured bytes.

// Link types the summaries understand.
const (
	linkEthernet = 1
	linkRaw      = 101 // raw IP (tun devices: WireGuard, Tailscale)
	linkRawAlt   = 12
	linkLinuxSLL = 113
)

// maxRecord bounds one packet record (snaplen is at most 65535).
const maxRecord = 262144

type pcapReader struct {
	r        io.Reader
	order    binary.ByteOrder
	nano     bool
	linkType uint32
}

// newPcapReader reads the global header.
func newPcapReader(r io.Reader) (*pcapReader, []byte, error) {
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, nil, fmt.Errorf("pcap header: %w", err)
	}
	p := &pcapReader{r: r}
	switch {
	case binary.LittleEndian.Uint32(hdr) == 0xa1b2c3d4:
		p.order = binary.LittleEndian
	case binary.BigEndian.Uint32(hdr) == 0xa1b2c3d4:
		p.order = binary.BigEndian
	case binary.LittleEndian.Uint32(hdr) == 0xa1b23c4d:
		p.order, p.nano = binary.LittleEndian, true
	case binary.BigEndian.Uint32(hdr) == 0xa1b23c4d:
		p.order, p.nano = binary.BigEndian, true
	default:
		return nil, nil, errors.New("not a pcap stream")
	}
	p.linkType = p.order.Uint32(hdr[20:])
	return p, hdr, nil
}

// next reads one record: its raw bytes (header included, to copy into the
// file), the packet time and original length, and the captured data.
func (p *pcapReader) next() (raw []byte, at time.Time, origLen int, data []byte, err error) {
	rh := make([]byte, 16)
	if _, err = io.ReadFull(p.r, rh); err != nil {
		return
	}
	incl := p.order.Uint32(rh[8:])
	if incl > maxRecord {
		err = fmt.Errorf("pcap record of %d bytes", incl)
		return
	}
	data = make([]byte, incl)
	if _, err = io.ReadFull(p.r, data); err != nil {
		return
	}
	frac := time.Duration(p.order.Uint32(rh[4:]))
	if !p.nano {
		frac *= time.Microsecond
	}
	at = time.Unix(int64(p.order.Uint32(rh[0:])), int64(frac)).UTC()
	origLen = int(p.order.Uint32(rh[12:]))
	raw = append(rh, data...)
	return
}

// Packet is the one-line summary the live view shows.
type Packet struct {
	N        int       `json:"n"`
	Time     time.Time `json:"time"`
	Src      string    `json:"src"`
	Dst      string    `json:"dst"`
	Protocol string    `json:"protocol"`
	Length   int       `json:"length"`
	Info     string    `json:"info"`
}

// summarize decodes just enough of a packet for its summary line. It never
// trusts lengths in the packet beyond the captured bytes.
func summarize(linkType uint32, data []byte) (src, dst, proto, info string) {
	var etherType uint16
	switch linkType {
	case linkEthernet:
		if len(data) < 14 {
			return "", "", "ethernet", "truncated"
		}
		etherType, data = binary.BigEndian.Uint16(data[12:]), data[14:]
		for etherType == 0x8100 || etherType == 0x88a8 { // VLAN tags
			if len(data) < 4 {
				return "", "", "vlan", "truncated"
			}
			etherType, data = binary.BigEndian.Uint16(data[2:]), data[4:]
		}
	case linkLinuxSLL:
		if len(data) < 16 {
			return "", "", "sll", "truncated"
		}
		etherType, data = binary.BigEndian.Uint16(data[14:]), data[16:]
	case linkRaw, linkRawAlt:
		if len(data) == 0 {
			return "", "", "ip", "empty"
		}
		switch data[0] >> 4 {
		case 4:
			etherType = 0x0800
		case 6:
			etherType = 0x86dd
		}
	default:
		return "", "", fmt.Sprintf("linktype %d", linkType), ""
	}
	switch etherType {
	case 0x0806:
		return arp(data)
	case 0x0800:
		if len(data) < 20 || data[0]>>4 != 4 {
			return "", "", "ipv4", "truncated"
		}
		ihl := int(data[0]&0x0f) * 4
		if ihl < 20 || len(data) < ihl {
			return "", "", "ipv4", "bad header"
		}
		s, _ := netip.AddrFromSlice(data[12:16])
		d, _ := netip.AddrFromSlice(data[16:20])
		frag := binary.BigEndian.Uint16(data[6:]) & 0x1fff
		if frag != 0 {
			return s.String(), d.String(), "ipv4", "fragment"
		}
		p, i := l4(data[9], data[ihl:])
		return s.String(), d.String(), p, i
	case 0x86dd:
		if len(data) < 40 {
			return "", "", "ipv6", "truncated"
		}
		s, _ := netip.AddrFromSlice(data[8:24])
		d, _ := netip.AddrFromSlice(data[24:40])
		p, i := l4(data[6], data[40:])
		return s.String(), d.String(), p, i
	}
	return "", "", fmt.Sprintf("ethertype 0x%04x", etherType), ""
}

func arp(b []byte) (string, string, string, string) {
	if len(b) < 28 {
		return "", "", "arp", "truncated"
	}
	sender, _ := netip.AddrFromSlice(b[14:18])
	target, _ := netip.AddrFromSlice(b[24:28])
	switch binary.BigEndian.Uint16(b[6:]) {
	case 1:
		return sender.String(), target.String(), "arp", fmt.Sprintf("who has %s? tell %s", target, sender)
	case 2:
		return sender.String(), target.String(), "arp", fmt.Sprintf("%s is at %x", sender, b[8:14])
	}
	return sender.String(), target.String(), "arp", ""
}

func l4(proto byte, b []byte) (string, string) {
	switch proto {
	case 6:
		if len(b) < 14 {
			return "tcp", "truncated"
		}
		sp, dp := binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		var flags []string
		for i, f := range []string{"FIN", "SYN", "RST", "PSH", "ACK", "URG"} {
			if b[13]&(1<<i) != 0 {
				flags = append(flags, f)
			}
		}
		return "tcp", fmt.Sprintf("%d → %d [%s]", sp, dp, strings.Join(flags, ","))
	case 17:
		if len(b) < 8 {
			return "udp", "truncated"
		}
		sp, dp := binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		info := fmt.Sprintf("%d → %d", sp, dp)
		if sp == 53 || dp == 53 {
			info += " DNS"
		}
		return "udp", info
	case 1:
		if len(b) < 2 {
			return "icmp", "truncated"
		}
		names := map[byte]string{0: "echo reply", 3: "unreachable", 8: "echo request", 11: "time exceeded"}
		if n, ok := names[b[0]]; ok {
			return "icmp", n
		}
		return "icmp", fmt.Sprintf("type %d", b[0])
	case 58:
		if len(b) < 2 {
			return "icmp6", "truncated"
		}
		names := map[byte]string{1: "unreachable", 3: "time exceeded", 128: "echo request", 129: "echo reply", 133: "router solicitation", 134: "router advertisement", 135: "neighbor solicitation", 136: "neighbor advertisement"}
		if n, ok := names[b[0]]; ok {
			return "icmp6", n
		}
		return "icmp6", fmt.Sprintf("type %d", b[0])
	}
	return fmt.Sprintf("ip proto %d", proto), ""
}
