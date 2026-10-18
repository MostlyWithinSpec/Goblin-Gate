package diag

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// DNSResult is the answer of Lookup.
type DNSResult struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Server  string   `json:"server"`
	RCode   string   `json:"rcode"` // NOERROR, NXDOMAIN, SERVFAIL, ...
	Ms      float64  `json:"ms"`
	Answers []string `json:"answers"`
}

var dnsTypes = map[string]dnsmessage.Type{
	"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "MX": dnsmessage.TypeMX, "TXT": dnsmessage.TypeTXT,
	"CNAME": dnsmessage.TypeCNAME, "NS": dnsmessage.TypeNS, "PTR": dnsmessage.TypePTR,
}

// dnsPort is where queries go (tests use a local fake server).
var dnsPort uint16 = 53

// BoxResolver is the box's own resolver (Unbound).
const BoxResolver = "127.0.0.1"

// Lookup asks server (an IP; "" or "box" = the box's resolver) for name.
// A PTR lookup of an IP address is turned into its reverse name.
func Lookup(ctx context.Context, name, typ, server string) (DNSResult, error) {
	if typ == "" {
		typ = "A"
	}
	typ = strings.ToUpper(typ)
	t, ok := dnsTypes[typ]
	if !ok {
		return DNSResult{}, fmt.Errorf("type must be A, AAAA, MX, TXT, CNAME, NS or PTR")
	}
	if a, err := netip.ParseAddr(name); err == nil && t == dnsmessage.TypePTR {
		name = reverseName(a)
	} else if !ValidHost(name) {
		return DNSResult{}, fmt.Errorf("name must be a DNS name")
	}
	if server == "" || server == "box" {
		server = BoxResolver
	}
	srv, err := netip.ParseAddr(server)
	if err != nil {
		return DNSResult{}, fmt.Errorf("server must be \"box\" or an IP address")
	}
	qname, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return DNSResult{}, fmt.Errorf("name: %w", err)
	}
	var idb [2]byte
	rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: qname, Type: t, Class: dnsmessage.ClassINET}},
	}
	packed, err := q.Pack()
	if err != nil {
		return DNSResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := exchange(ctx, netip.AddrPortFrom(srv, dnsPort).String(), packed, id)
	if err != nil {
		return DNSResult{}, fmt.Errorf("no answer from %s: %w", srv, err)
	}
	out := DNSResult{Name: name, Type: typ, Server: srv.String(), Ms: float64(time.Since(start).Microseconds()) / 1000, Answers: []string{}}
	out.RCode = rcodeName(resp.Header.RCode)
	for _, a := range resp.Answers {
		if s := answerText(a); s != "" {
			out.Answers = append(out.Answers, s)
		}
	}
	return out, nil
}

// exchange sends a query over UDP and retries over TCP when the answer was
// truncated.
func exchange(ctx context.Context, addr string, query []byte, id uint16) (*dnsmessage.Message, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		var m dnsmessage.Message
		if m.Unpack(buf[:n]) != nil || m.Header.ID != id {
			continue // not our answer
		}
		if !m.Header.Truncated {
			return &m, nil
		}
		return exchangeTCP(ctx, addr, query, id)
	}
}

func exchangeTCP(ctx context.Context, addr string, query []byte, id uint16) (*dnsmessage.Message, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	msg := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	if _, err := c.Write(append(msg, query...)); err != nil {
		return nil, err
	}
	var lb [2]byte
	if _, err := ioReadFull(c, lb[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(lb[:]))
	if _, err := ioReadFull(c, buf); err != nil {
		return nil, err
	}
	var m dnsmessage.Message
	if err := m.Unpack(buf); err != nil {
		return nil, err
	}
	if m.Header.ID != id {
		return nil, fmt.Errorf("answer for another query")
	}
	return &m, nil
}

func ioReadFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func rcodeName(r dnsmessage.RCode) string {
	switch r {
	case dnsmessage.RCodeSuccess:
		return "NOERROR"
	case dnsmessage.RCodeNameError:
		return "NXDOMAIN"
	case dnsmessage.RCodeServerFailure:
		return "SERVFAIL"
	case dnsmessage.RCodeRefused:
		return "REFUSED"
	case dnsmessage.RCodeFormatError:
		return "FORMERR"
	case dnsmessage.RCodeNotImplemented:
		return "NOTIMP"
	}
	return fmt.Sprintf("RCODE%d", r)
}

func answerText(a dnsmessage.Resource) string {
	switch b := a.Body.(type) {
	case *dnsmessage.AResource:
		return netip.AddrFrom4(b.A).String()
	case *dnsmessage.AAAAResource:
		return netip.AddrFrom16(b.AAAA).String()
	case *dnsmessage.CNAMEResource:
		return "CNAME " + b.CNAME.String()
	case *dnsmessage.MXResource:
		return fmt.Sprintf("%d %s", b.Pref, b.MX.String())
	case *dnsmessage.NSResource:
		return b.NS.String()
	case *dnsmessage.PTRResource:
		return b.PTR.String()
	case *dnsmessage.TXTResource:
		return `"` + strings.Join(b.TXT, `" "`) + `"`
	}
	return ""
}

// reverseName is the in-addr.arpa / ip6.arpa name of an address.
func reverseName(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])
	}
	b := a.As16()
	var sb strings.Builder
	for i := 15; i >= 0; i-- {
		fmt.Fprintf(&sb, "%x.%x.", b[i]&0x0f, b[i]>>4)
	}
	return sb.String() + "ip6.arpa"
}
