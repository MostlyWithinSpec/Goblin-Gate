package flowexport

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// Golden packets, written by hand from the RFC layouts (RFC 3954 sections
// 5 and 8, RFC 7011 section 3 and the IANA information element registry),
// not produced by the encoder. The input: one TCP connection 192.168.1.10:51234
// -> 93.184.216.34:443 (and the same over IPv6), 900 octets, 10 packets,
// counted from 3 s to 6 s after the exporter started at 2026-11-07 12:00:00
// UTC (unix 1794052800 = 0x6aef12c0).
var (
	goldBoot = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	goldEnd  = goldBoot.Add(6 * time.Second)
	goldV4   = Record{
		Protocol: 6, Src: "192.168.1.10", SrcPort: 51234,
		Dst: "93.184.216.34", DstPort: 443,
		Octets: 900, Packets: 10,
		Start: goldBoot.Add(3 * time.Second), End: goldEnd,
	}
	goldV6 = Record{
		Protocol: 6, Src: "2001:db8::10", SrcPort: 51234,
		Dst: "2001:db8::20", DstPort: 443,
		Octets: 900, Packets: 10,
		Start: goldBoot.Add(3 * time.Second), End: goldEnd,
	}
)

// hx joins hex words (spaces and "|" are only for reading).
func hx(t *testing.T, parts ...string) []byte {
	t.Helper()
	s := strings.NewReplacer(" ", "", "|", "").Replace(strings.Join(parts, ""))
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenPackets(t *testing.T) {
	const (
		v4Fields = "0008 0004 | 000c 0004 | 0004 0001 | 0007 0002 | 000b 0002 | 0001 0008 | 0002 0008"
		v6Fields = "001b 0010 | 001c 0010 | 0004 0001 | 0007 0002 | 000b 0002 | 0001 0008 | 0002 0008"
		v9Times  = "0016 0004 | 0015 0004" // FIRST_SWITCHED 22, LAST_SWITCHED 21
		ipTimes  = "0098 0008 | 0099 0008" // flowStartMilliseconds 152, flowEndMilliseconds 153
		v4Addrs  = "c0a8010a | 5db8d822"   // 192.168.1.10, 93.184.216.34
		v6Addrs  = "20010db8000000000000000000000010 | 20010db8000000000000000000000020"
		l4       = "06 | c822 | 01bb"                    // TCP, 51234, 443
		counters = "0000000000000384 | 000000000000000a" // 900 octets, 10 packets
		v9Start  = "00000bb8 | 00001770"                 // 3000 ms, 6000 ms of uptime
		ipStart  = "000001a1b5e149b8 | 000001a1b5e15570" // epoch ms at 12:00:03 and 12:00:06
	)
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		// v9 template flowset: ID 0, length 44, template 256 with 9 fields
		{"v9 template 256", v9Flowset(v9TemplateFlowset, must(v9TemplateBody(TemplateIDIPv4))),
			hx(t, "0000 002c | 0100 0009 |", v4Fields, v9Times)},
		{"v9 template 257", v9Flowset(v9TemplateFlowset, must(v9TemplateBody(TemplateIDIPv6))),
			hx(t, "0000 002c | 0101 0009 |", v6Fields, v9Times)},
		// v9 data flowset: 4 + 37 octets, padded to 44
		{"v9 data 256", must(v9EncodeRecords(TemplateIDIPv4, goldBoot, []Record{goldV4})),
			hx(t, "0100 002c |", v4Addrs, l4, counters, v9Start, "000000")},
		// 4 + 61 octets, padded to 68
		{"v9 data 257", must(v9EncodeRecords(TemplateIDIPv6, goldBoot, []Record{goldV6})),
			hx(t, "0101 0044 |", v6Addrs, l4, counters, v9Start, "000000")},
		// v9 header: version 9, count 1, uptime 6000 ms, unix secs, sequence 0, source 1
		{"v9 header", v9Header(1, goldBoot, goldEnd, 0),
			hx(t, "0009 0001 | 00001770 | 6aef12c6 | 00000000 | 00000001")},
		// IPFIX template set: ID 2, length 44
		{"ipfix template 256", ipfixSet(ipfixTemplateSet, must(ipfixTemplateBody(TemplateIDIPv4))),
			hx(t, "0002 002c | 0100 0009 |", v4Fields, ipTimes)},
		{"ipfix template 257", ipfixSet(ipfixTemplateSet, must(ipfixTemplateBody(TemplateIDIPv6))),
			hx(t, "0002 002c | 0101 0009 |", v6Fields, ipTimes)},
		// IPFIX data set: 4 + 45 octets, no padding
		{"ipfix data 256", must(ipfixEncodeRecords(TemplateIDIPv4, goldBoot, []Record{goldV4})),
			hx(t, "0100 0031 |", v4Addrs, l4, counters, ipStart)},
		{"ipfix data 257", must(ipfixEncodeRecords(TemplateIDIPv6, goldBoot, []Record{goldV6})),
			hx(t, "0101 0049 |", v6Addrs, l4, counters, ipStart)},
		// IPFIX message: version 10, length 16 + 49 = 65, export time, sequence 7, domain 1
		{"ipfix message", must(ipfixPacket([][]byte{must(ipfixEncodeRecords(TemplateIDIPv4, goldBoot, []Record{goldV4}))}, goldEnd, 7)),
			hx(t, "000a 0041 | 6aef12c6 | 00000007 | 00000001 | 0100 0031 |", v4Addrs, l4, counters, ipStart)},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s\n got %x\nwant %x", c.name, c.got, c.want)
		}
	}
}

// must carries a two-value call into an expression.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
