package flowexport

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

// maxUDPPayload keeps every datagram under 1400 bytes, so a flow packet never
// needs IP fragmentation on any sane link.
const maxUDPPayload = 1400

// Information element IDs (IANA "IPFIX Information Elements"; NetFlow v9,
// RFC 3954 section 8, uses the same numbers for these). The timestamps
// differ: v9 has FIRST_SWITCHED/LAST_SWITCHED in milliseconds of system
// uptime, IPFIX flowStartMilliseconds/flowEndMilliseconds since the epoch, so
// each protocol fills its own.
const (
	fieldOctets   = 1  // octetDeltaCount, v9 IN_BYTES
	fieldPackets  = 2  // packetDeltaCount, v9 IN_PKTS
	fieldProto    = 4  // protocolIdentifier, v9 PROTOCOL
	fieldSrcPort  = 7  // sourceTransportPort, v9 L4_SRC_PORT
	fieldSrcAddr  = 8  // sourceIPv4Address, v9 IPV4_SRC_ADDR
	fieldDstPort  = 11 // destinationTransportPort, v9 L4_DST_PORT
	fieldDstAddr  = 12 // destinationIPv4Address, v9 IPV4_DST_ADDR
	fieldSrcAddr6 = 27 // sourceIPv6Address, v9 IPV6_SRC_ADDR
	fieldDstAddr6 = 28 // destinationIPv6Address, v9 IPV6_DST_ADDR
)

// dataFields returns a template's shared fields for one address family, in
// wire order, followed by the protocol's two timestamp fields.
func dataFields(v6 bool, start, end field) []field {
	src, dst := field{fieldSrcAddr, 4}, field{fieldDstAddr, 4}
	if v6 {
		src, dst = field{fieldSrcAddr6, 16}, field{fieldDstAddr6, 16}
	}
	return []field{src, dst, {fieldProto, 1}, {fieldSrcPort, 2}, {fieldDstPort, 2},
		{fieldOctets, 8}, {fieldPackets, 8}, start, end}
}

// field is one template field: an information element ID (enterprise 0) and
// its length in octets.
type field struct {
	id     uint16
	length uint16
}

// sizeof is the field's encoded size. The exporter only builds records from its
// own two templates, so an unknown field is a bug in this package, not bad
// input.
func (f field) sizeof() (int, error) {
	switch f.id {
	case fieldSrcAddr, fieldDstAddr:
		return 4, nil
	case fieldSrcAddr6, fieldDstAddr6:
		return 16, nil
	case fieldProto:
		return 1, nil
	case fieldSrcPort, fieldDstPort:
		return 2, nil
	case fieldOctets, fieldPackets:
		return 8, nil
	default:
		if f.length == 0 {
			return 0, fmt.Errorf("field %d has no length", f.id)
		}
		return int(f.length), nil
	}
}

// recordSize is the encoded size of one record in a template.
func recordSize(fields []field) (int, error) {
	n := 0
	for _, f := range fields {
		s, err := f.sizeof()
		if err != nil {
			return 0, err
		}
		n += s
	}
	return n, nil
}

// writeRecord fills one record's bytes into rec, which is exactly one record
// long. The shared fields go here; fill writes the protocol's own timestamp
// fields, because v9 and IPFIX number and scale theirs differently.
func writeRecord(fields []field, r Record, bootTime time.Time, rec []byte, fill func(f field, rec []byte) int) error {
	var b [8]byte
	n := 0
	for _, f := range fields {
		switch f.id {
		case fieldSrcAddr, fieldDstAddr, fieldSrcAddr6, fieldDstAddr6:
			addr := r.Src
			if f.id == fieldDstAddr || f.id == fieldDstAddr6 {
				addr = r.Dst
			}
			a, err := netip.ParseAddr(addr)
			if err != nil {
				return fmt.Errorf("record address %q: %w", addr, err)
			}
			if a.BitLen() != int(f.length)*8 {
				return fmt.Errorf("record address %q does not fit a %d-octet field", addr, f.length)
			}
			n += copy(rec[n:], a.AsSlice())
		case fieldProto:
			rec[n] = r.Protocol
			n++
		case fieldSrcPort:
			binary.BigEndian.PutUint16(b[:2], r.SrcPort)
			n += copy(rec[n:], b[:2])
		case fieldDstPort:
			binary.BigEndian.PutUint16(b[:2], r.DstPort)
			n += copy(rec[n:], b[:2])
		case fieldOctets:
			binary.BigEndian.PutUint64(b[:8], r.Octets)
			n += copy(rec[n:], b[:8])
		case fieldPackets:
			binary.BigEndian.PutUint64(b[:8], r.Packets)
			n += copy(rec[n:], b[:8])
		default:
			written := fill(f, rec[n:])
			if written <= 0 {
				return fmt.Errorf("field %d is not filled by this exporter", f.id)
			}
			n += written
		}
	}
	if n != len(rec) {
		return fmt.Errorf("record came out %d octets, template wants %d", n, len(rec))
	}
	return nil
}

// encodeRecords writes every record of a template, each in its own bytes so a
// caller can hand the result out without aliasing.
func encodeRecords(fields []field, recs []Record, fill func(f field, r Record, bootTime time.Time, rec []byte) int, bootTime time.Time) ([]byte, error) {
	size, err := recordSize(fields)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(recs)*size)
	for _, r := range recs {
		start := len(out)
		out = append(out, make([]byte, size)...)
		// a limited slice: a fill that writes past its field cannot reach the
		// next record
		rec := out[start:len(out):len(out)]
		if err := writeRecord(fields, r, bootTime, rec, func(f field, dst []byte) int {
			return fill(f, r, bootTime, dst)
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
