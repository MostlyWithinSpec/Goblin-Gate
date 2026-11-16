package flowexport

import (
	"fmt"
	"time"
)

// Protocol constants of the flow wire format.
const (
	// ProtocolIPFIX is RFC 7011 (version 10). It is the default.
	ProtocolIPFIX = "ipfix"
	// ProtocolNetFlow9 is RFC 3954 (version 9).
	ProtocolNetFlow9 = "netflow9"
)

// Template resend policy. A UDP collector restarts (or loses a datagram), so
// templates cannot be sent once: they go out with the first packet, then every
// resendPackets data packets and every resendEvery.
const (
	resendPackets = 20
	resendEvery   = 60 * time.Second
)

// packetBuilder turns records into datagrams of at most maxUDPPayload bytes,
// keeping one template per packet and counting what each protocol's sequence
// number counts.
type packetBuilder struct {
	protocol string
	bootTime time.Time

	// v9Packets counts every v9 export packet sent (its sequence number) and
	// dataRecords counts IPFIX data records (its sequence number).
	v9Packets   uint32
	dataRecords uint32
	// packetsSinceTemplate counts data packets since the last template packet,
	// and lastTemplates the time of the last one.
	packetsSinceTemplate int
	lastTemplates        time.Time
	// sentTemplates is false until the first template packet leaves, so a fresh
	// exporter always starts with its templates.
	sentTemplates bool
}

func newPacketBuilder(protocol string, bootTime time.Time) *packetBuilder {
	return &packetBuilder{protocol: protocol, bootTime: bootTime}
}

// recordSize is one record's encoded size in this protocol's template.
func (b *packetBuilder) recordSize(template uint16) (int, error) {
	fields, ok := b.templates()[template]
	if !ok {
		return 0, fmt.Errorf("unknown template %d", template)
	}
	return recordSize(fields)
}

func (b *packetBuilder) templates() map[uint16][]field {
	if b.protocol == ProtocolNetFlow9 {
		return v9Templates
	}
	return ipfixTemplates
}

// encodeSet writes the records of one template as one set (a v9 record
// flowset or an IPFIX data set).
func (b *packetBuilder) encodeSet(template uint16, recs []Record) ([]byte, error) {
	if b.protocol == ProtocolNetFlow9 {
		return v9EncodeRecords(template, b.bootTime, recs)
	}
	return ipfixEncodeRecords(template, b.bootTime, recs)
}

// templateSets returns the template sets a template packet carries: one per
// address family.
func (b *packetBuilder) templateSets() ([][]byte, error) {
	t4, err := b.templateBody(TemplateIDIPv4)
	if err != nil {
		return nil, err
	}
	t6, err := b.templateBody(TemplateIDIPv6)
	if err != nil {
		return nil, err
	}
	if b.protocol == ProtocolNetFlow9 {
		return [][]byte{v9Flowset(v9TemplateFlowset, t4), v9Flowset(v9TemplateFlowset, t6)}, nil
	}
	return [][]byte{ipfixSet(ipfixTemplateSet, t4), ipfixSet(ipfixTemplateSet, t6)}, nil
}

func (b *packetBuilder) templateBody(template uint16) ([]byte, error) {
	if b.protocol == ProtocolNetFlow9 {
		return v9TemplateBody(template)
	}
	return ipfixTemplateBody(template)
}

// wantTemplates reports whether the next batch must start with the templates:
// never sent, or the packet or time policy is due. A batch with no records too
// still sends them — a collector that restarted in the night must not wait for
// traffic to get the templates back.
func (b *packetBuilder) wantTemplates(at time.Time) bool {
	return !b.sentTemplates || b.packetsSinceTemplate >= resendPackets || at.Sub(b.lastTemplates) >= resendEvery
}

// batch is one flush's worth of packets, ready to send in order.
type batch struct {
	packets [][]byte
	// records[i] is the flow records packet i carries: 0 for a template packet,
	// which is what each protocol's sequence number counts differently.
	records []int
}

func (b *batch) addPacket(p []byte, records int) {
	b.packets = append(b.packets, p)
	b.records = append(b.records, records)
}

// add turns recs into a batch of datagrams, splitting them so no datagram
// passes maxUDPPayload. When the resend policy is due, every packet of the
// batch carries both templates in front of its data: collectors decode
// datagrams on several workers, so a data packet can overtake a separate
// template packet and be dropped. With no records, the templates go alone.
func (b *packetBuilder) add(at time.Time, recs []Record) (batch, error) {
	var out batch
	var tmpl [][]byte
	reserve := 0
	if b.wantTemplates(at) {
		sets, err := b.templateSets()
		if err != nil {
			return out, err
		}
		tmpl = sets
		for _, s := range sets {
			reserve += len(s)
		}
		b.packetsSinceTemplate = 0
		b.lastTemplates = at
		b.sentTemplates = true
	}
	// group by family: one data template per packet
	var v4, v6 []Record
	for _, r := range recs {
		if r.v6() {
			v6 = append(v6, r)
		} else {
			v4 = append(v4, r)
		}
	}
	for _, g := range []struct {
		template uint16
		recs     []Record
	}{{TemplateIDIPv4, v4}, {TemplateIDIPv6, v6}} {
		if len(g.recs) == 0 {
			continue
		}
		size, err := b.recordSize(g.template)
		if err != nil {
			return out, err
		}
		// header, templates, set header and up to 3 octets of v9 padding
		perPacket := (maxUDPPayload - b.headerSize() - reserve - 8) / size
		if perPacket < 1 {
			return out, fmt.Errorf("template %d record of %d octets does not fit a %d-byte packet",
				g.template, size, maxUDPPayload)
		}
		for start := 0; start < len(g.recs); start += perPacket {
			end := min(start+perPacket, len(g.recs))
			set, err := b.encodeSet(g.template, g.recs[start:end])
			if err != nil {
				return out, err
			}
			sets := append(append([][]byte{}, tmpl...), set)
			p, err := b.packet(sets, end-start, len(tmpl), at)
			if err != nil {
				return out, err
			}
			out.addPacket(p, end-start)
			b.packetsSinceTemplate++
		}
	}
	if tmpl != nil && len(out.packets) == 0 {
		p, err := b.packet(tmpl, 0, len(tmpl), at)
		if err != nil {
			return out, err
		}
		out.addPacket(p, 0)
	}
	return out, nil
}

// packet builds one datagram from its sets and moves the sequence counters.
// records counts the data records, templates the template records: v9's
// header count is both together and its sequence number counts packets;
// IPFIX's sequence number counts data records sent before this message.
func (b *packetBuilder) packet(sets [][]byte, records, templates int, at time.Time) ([]byte, error) {
	if b.protocol == ProtocolNetFlow9 {
		p := v9Header(records+templates, b.bootTime, at, b.v9Packets)
		for _, s := range sets {
			p = append(p, s...)
		}
		b.v9Packets++
		return p, nil
	}
	p, err := ipfixPacket(sets, at, b.dataRecords)
	if err != nil {
		return nil, err
	}
	b.dataRecords += uint32(records)
	return p, nil
}

func (b *packetBuilder) headerSize() int {
	if b.protocol == ProtocolNetFlow9 {
		return v9HeaderSize
	}
	return ipfixHeaderSize
}
