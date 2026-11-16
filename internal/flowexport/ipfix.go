package flowexport

import (
	"encoding/binary"
	"fmt"
	"time"
)

// IPFIX encoder (RFC 7011).
//
// Message header (16 octets, section 3.1): version 10, length of the whole
// message, export time (seconds since the epoch), sequence number (data
// records sent before this message) and observation domain ID.
//
// A template set (set ID 2, section 3.4.1) holds template records: template
// ID, field count, then per field its information element ID and length.
// Data sets carry the template ID as their set ID.

const ipfixTemplateSet = 2

// IPFIX timestamp fields: dateTimeMilliseconds since the epoch.
const (
	fieldFlowStartMillis = 152 // flowStartMilliseconds
	fieldFlowEndMillis   = 153 // flowEndMilliseconds
)

var ipfixTemplates = map[uint16][]field{
	TemplateIDIPv4: dataFields(false, field{fieldFlowStartMillis, 8}, field{fieldFlowEndMillis, 8}),
	TemplateIDIPv6: dataFields(true, field{fieldFlowStartMillis, 8}, field{fieldFlowEndMillis, 8}),
}

// ipfixTemplateBody is one template record.
func ipfixTemplateBody(id uint16) ([]byte, error) {
	fields, ok := ipfixTemplates[id]
	if !ok {
		return nil, fmt.Errorf("ipfix: unknown template %d", id)
	}
	out := make([]byte, 4+len(fields)*4)
	binary.BigEndian.PutUint16(out[0:], id)
	binary.BigEndian.PutUint16(out[2:], uint16(len(fields)))
	for i, f := range fields {
		binary.BigEndian.PutUint16(out[4+i*4:], f.id)
		binary.BigEndian.PutUint16(out[6+i*4:], f.length)
	}
	return out, nil
}

// ipfixSet wraps a body in a set header: set ID and length (header + body).
func ipfixSet(id uint16, body []byte) []byte {
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint16(out[0:], id)
	binary.BigEndian.PutUint16(out[2:], uint16(4+len(body)))
	copy(out[4:], body)
	return out
}

// ipfixEncodeRecords writes the records of one template as one data set.
func ipfixEncodeRecords(template uint16, bootTime time.Time, recs []Record) ([]byte, error) {
	fields, ok := ipfixTemplates[template]
	if !ok {
		return nil, fmt.Errorf("ipfix: unknown template %d", template)
	}
	body, err := encodeRecords(fields, recs, ipfixFill, bootTime)
	if err != nil {
		return nil, fmt.Errorf("ipfix: %w", err)
	}
	return ipfixSet(template, body), nil
}

// ipfixFill writes flowStartMilliseconds (the record's start) and
// flowEndMilliseconds (its end). It returns 0 for a field it does not know,
// which the caller reports as an error.
func ipfixFill(f field, r Record, _ time.Time, rec []byte) int {
	var t time.Time
	switch f.id {
	case fieldFlowStartMillis:
		t = r.Start
	case fieldFlowEndMillis:
		t = r.End
	default:
		return 0
	}
	binary.BigEndian.PutUint64(rec, uint64(t.UnixMilli()))
	return 8
}

// ipfixHeaderSize is RFC 7011 section 3.1: 16 octets.
const ipfixHeaderSize = 16

// ipfixPacket writes a whole message: header plus the sets in order.
func ipfixPacket(sets [][]byte, now time.Time, sequence uint32) ([]byte, error) {
	body := 0
	for _, s := range sets {
		body += len(s)
	}
	if ipfixHeaderSize+body > 0xffff {
		return nil, fmt.Errorf("ipfix: message of %d octets", ipfixHeaderSize+body)
	}
	out := make([]byte, ipfixHeaderSize+body)
	binary.BigEndian.PutUint16(out[0:], 10)
	binary.BigEndian.PutUint16(out[2:], uint16(ipfixHeaderSize+body))
	binary.BigEndian.PutUint32(out[4:], uint32(now.Unix()))
	binary.BigEndian.PutUint32(out[8:], sequence)
	binary.BigEndian.PutUint32(out[12:], ObservationDomain)
	p := ipfixHeaderSize
	for _, s := range sets {
		p += copy(out[p:], s)
	}
	return out, nil
}
