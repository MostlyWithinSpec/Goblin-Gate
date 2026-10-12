package webfilter

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

// Minimal DNS message handling: the proxy only needs the question of a
// query and to build "blocked" answers; everything else is forwarded to
// Unbound unchanged.

// Query types the proxy answers itself when blocking.
const (
	typeA    = 1
	typeAAAA = 28
	classIN  = 1
)

var errBadMessage = errors.New("webfilter: malformed DNS message")

// question is the first question of a DNS query.
type question struct {
	name  string // lowercase, without the trailing dot
	qtype uint16
	end   int // offset after the question section
}

// parseQuestion reads the header and first question of a query.
func parseQuestion(msg []byte) (question, error) {
	if len(msg) < 12 {
		return question{}, errBadMessage
	}
	if msg[2]&0x80 != 0 || binary.BigEndian.Uint16(msg[4:6]) != 1 { // a response, or not exactly one question
		return question{}, errBadMessage
	}
	var labels []string
	i := 12
	for {
		if i >= len(msg) {
			return question{}, errBadMessage
		}
		l := int(msg[i])
		i++
		if l == 0 {
			break
		}
		if l&0xc0 != 0 || i+l > len(msg) { // queries do not compress the question
			return question{}, errBadMessage
		}
		labels = append(labels, strings.ToLower(string(msg[i:i+l])))
		i += l
	}
	if i+4 > len(msg) {
		return question{}, errBadMessage
	}
	return question{name: strings.Join(labels, "."), qtype: binary.BigEndian.Uint16(msg[i : i+2]), end: i + 4}, nil
}

// blockedAnswer answers query (whose question ends at q.end) with addr for
// A queries and an empty NOERROR answer for every other type, so browsers
// land on the block page.
func blockedAnswer(query []byte, q question, addr netip.Addr) []byte {
	out := make([]byte, 0, q.end+16)
	out = append(out, query[:q.end]...)
	// header: QR, keep opcode and RD, set RA; RCODE 0; one question
	out[2] = 0x80 | query[2]&0x79
	out[3] = 0x80
	binary.BigEndian.PutUint16(out[4:6], 1)
	binary.BigEndian.PutUint16(out[8:10], 0)  // NSCOUNT
	binary.BigEndian.PutUint16(out[10:12], 0) // ARCOUNT (the client's OPT is dropped)
	if q.qtype == typeA && addr.Is4() {
		binary.BigEndian.PutUint16(out[6:8], 1)
		a := addr.As4()
		out = append(out,
			0xc0, 12, // name: pointer to the question
			0, typeA, 0, classIN,
			0, 0, 0, 60, // TTL 60 s: a lifted block takes effect soon
			0, 4, a[0], a[1], a[2], a[3])
	} else {
		binary.BigEndian.PutUint16(out[6:8], 0)
	}
	return out
}
