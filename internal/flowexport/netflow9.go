package flowexport

import (
	"encoding/binary"
	"fmt"
	"time"
)

// NetFlow v9 encoder (RFC 3954).
//
// Header (20 octets, section 5.1): version 9, count, sysUptime (ms since this
// exporter started), unix_secs, sequence number (every export packet sent so
// far, template packets included) and source ID (ObservationDomain). Count is
// the number of records in the packet, template records and data records
// alike.
//
// A template flowset (flowset ID 0, section 5.2) lists template ID, field
// count and per field its type and length (2 octets each). Data flowsets
// carry the template ID as their flowset ID and are padded to 4 octets.

const v9TemplateFlowset = 0

// v9 timestamp fields (RFC 3954 section 8): milliseconds of sysUptime.
const (
	v9FieldLastSwitched  = 21
	v9FieldFirstSwitched = 22
)

var v9Templates = map[uint16][]field{
	TemplateIDIPv4: dataFields(false, field{v9FieldFirstSwitched, 4}, field{v9FieldLastSwitched, 4}),
	TemplateIDIPv6: dataFields(true, field{v9FieldFirstSwitched, 4}, field{v9FieldLastSwitched, 4}),
}

// v9TemplateBody is one template record: template ID, field count, then type
// and length per field.
func v9TemplateBody(id uint16) ([]byte, error) {
	fields, ok := v9Templates[id]
	if !ok {
		return nil, fmt.Errorf("netflow9: unknown template %d", id)
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

// v9Flowset wraps a body in a flowset header (ID, length including the
// header) and pads it to a multiple of 4 octets (section 5.3).
func v9Flowset(id uint16, body []byte) []byte {
	n := 4 + len(body)
	n += (4 - n%4) % 4
	out := make([]byte, n)
	binary.BigEndian.PutUint16(out[0:], id)
	binary.BigEndian.PutUint16(out[2:], uint16(n))
	copy(out[4:], body)
	return out
}

// v9EncodeRecords writes the records of one template as one data flowset.
func v9EncodeRecords(template uint16, bootTime time.Time, recs []Record) ([]byte, error) {
	fields, ok := v9Templates[template]
	if !ok {
		return nil, fmt.Errorf("netflow9: unknown template %d", template)
	}
	body, err := encodeRecords(fields, recs, v9Fill, bootTime)
	if err != nil {
		return nil, fmt.Errorf("netflow9: %w", err)
	}
	return v9Flowset(template, body), nil
}

// v9Fill writes FIRST_SWITCHED (the record's start) and LAST_SWITCHED (its
// end) as milliseconds since the exporter started. It returns 0 for a field
// it does not know, which the caller reports as an error.
func v9Fill(f field, r Record, bootTime time.Time, rec []byte) int {
	var t time.Time
	switch f.id {
	case v9FieldFirstSwitched:
		t = r.Start
	case v9FieldLastSwitched:
		t = r.End
	default:
		return 0
	}
	binary.BigEndian.PutUint32(rec, uptimeMillis(bootTime, t))
	return 4
}

// uptimeMillis is t as milliseconds since bootTime (0 before it), wrapping at
// 2^32 like a router's sysUptime.
func uptimeMillis(bootTime, t time.Time) uint32 {
	d := t.Sub(bootTime).Milliseconds()
	if d < 0 {
		return 0
	}
	return uint32(d)
}

// v9HeaderSize is RFC 3954 section 5.1: version 2 + count 2 + sysUptime 4 +
// unix_secs 4 + sequence 4 + source ID 4 = 20 octets.
const v9HeaderSize = 20

// v9Header is the NetFlow v9 packet header.
func v9Header(count int, bootTime, now time.Time, sequence uint32) []byte {
	h := make([]byte, v9HeaderSize)
	binary.BigEndian.PutUint16(h[0:], 9)
	binary.BigEndian.PutUint16(h[2:], uint16(count))
	binary.BigEndian.PutUint32(h[4:], uptimeMillis(bootTime, now))
	binary.BigEndian.PutUint32(h[8:], uint32(now.Unix()))
	binary.BigEndian.PutUint32(h[12:], sequence)
	binary.BigEndian.PutUint32(h[16:], ObservationDomain)
	return h
}
