// Package flowexport sends the box's sampled connections to a flow collector
// (ntopng, Elastic, GoFlow2, nfdump) as NetFlow v9 (RFC 3954) or IPFIX
// (RFC 7011) over UDP (task 144).
//
// The records come from the conntrack sampler in internal/flows, which reads
// /proc/net/nf_conntrack every 3 seconds, so they are sampled, not per
// packet: a connection that starts and ends between two samples is never
// seen. Counters are the conntrack counters, reported as deltas since the
// record before them.
package flowexport

import (
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

// Template IDs. One template per address family for both protocols; the
// field lists are the same, only the timestamps differ (v9 reports
// milliseconds since the exporter started, IPFIX wall-clock milliseconds).
const (
	TemplateIDIPv4 = 256
	TemplateIDIPv6 = 257
)

// ObservationDomain is the v9 Engine ID and the IPFIX Observation Domain ID.
const ObservationDomain = 1

// Record is one exported flow record: one direction of one connection
// (not a biflow). Each connection gives up to two records, initiator ->
// responder (the conntrack original direction) and responder -> initiator
// (the reply direction), each with its own direction's octets and packets.
type Record struct {
	// Protocol is the IP protocol number (6 = TCP, 17 = UDP, 1 = ICMP).
	Protocol uint8
	Src      string // text address; its family picks the template
	SrcPort  uint16
	Dst      string
	DstPort  uint16
	Octets   uint64
	Packets  uint64
	Start    time.Time // the sample the counted interval starts at (the previous record, or the first sight)
	End      time.Time // the sample that reported the counters
}

func (r Record) v6() bool {
	for i := 0; i < len(r.Src); i++ {
		if r.Src[i] == ':' {
			return true
		}
	}
	return false
}

// templateID returns the template ID for the record's address family.
func (r Record) templateID() uint16 {
	if r.v6() {
		return TemplateIDIPv6
	}
	return TemplateIDIPv4
}

// recordsOf turns one connection's counters into its records: the initiator
// direction and the reply direction, each left out when it carried nothing.
func recordsOf(f flows.Flow, start, end time.Time) []Record {
	proto := protoNumber(f.Protocol)
	shared := Record{Protocol: proto, Start: start, End: end}
	a := shared
	a.Src, a.Dst = f.Src.String(), f.Dst.String()
	a.SrcPort, a.DstPort = uint16(f.SrcPort), uint16(f.DstPort)
	a.Octets, a.Packets = f.BytesOut, f.PacketsOut
	b := shared
	b.Src, b.Dst = f.Dst.String(), f.Src.String()
	b.SrcPort, b.DstPort = uint16(f.DstPort), uint16(f.SrcPort)
	b.Octets, b.Packets = f.BytesIn, f.PacketsIn
	out := make([]Record, 0, 2)
	for _, r := range []Record{a, b} {
		if r.Octets > 0 || r.Packets > 0 {
			out = append(out, r)
		}
	}
	return out
}

// protoNumber maps conntrack's protocol names to IP protocol numbers. An
// unknown name becomes 0: the collector sees no higher-layer protocol.
func protoNumber(name string) uint8 {
	switch name {
	case "tcp", "TCP", "6":
		return 6
	case "udp", "UDP", "17":
		return 17
	case "icmp", "ICMP", "1":
		return 1
	case "esp", "ESP", "50":
		return 50
	case "gre", "GRE", "47":
		return 47
	case "sctp", "SCTP", "132":
		return 132
	case "icmp6", "ICMPv6", "58":
		return 58
	}
	return 0
}
