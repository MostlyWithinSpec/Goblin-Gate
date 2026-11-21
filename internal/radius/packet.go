package radius

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// RADIUS codes and attributes this package sends (RFC 2865, RFC 5176).
const (
	codeDisconnectRequest = 40
	codeDisconnectACK     = 41
	codeDisconnectNAK     = 42

	attrUserName         = 1
	attrNASIPAddress     = 4
	attrCallingStationID = 31
	attrAcctSessionID    = 44
	attrErrorCause       = 101
)

type attr struct {
	typ   byte
	value []byte
}

// encodeRequest builds a Disconnect-Request: its authenticator is
// MD5(code, id, length, 16 zero octets, attributes, secret).
func encodeRequest(code, id byte, attrs []attr, secret string) ([]byte, error) {
	var body bytes.Buffer
	for _, a := range attrs {
		if len(a.value) == 0 {
			continue
		}
		if len(a.value) > 253 {
			return nil, fmt.Errorf("radius: attribute %d too long", a.typ)
		}
		body.WriteByte(a.typ)
		body.WriteByte(byte(len(a.value) + 2))
		body.Write(a.value)
	}
	n := 20 + body.Len()
	if n > 4096 {
		return nil, errors.New("radius: packet too long")
	}
	p := make([]byte, n)
	p[0], p[1] = code, id
	binary.BigEndian.PutUint16(p[2:], uint16(n))
	copy(p[20:], body.Bytes())
	sum := md5.Sum(append(append([]byte{}, p...), secret...))
	copy(p[4:20], sum[:])
	return p, nil
}

// checkResponse verifies a reply's authenticator against the request's.
func checkResponse(resp, req []byte, secret string) bool {
	if len(resp) < 20 || int(binary.BigEndian.Uint16(resp[2:])) != len(resp) || resp[1] != req[1] {
		return false
	}
	h := md5.New()
	h.Write(resp[:4])
	h.Write(req[4:20])
	h.Write(resp[20:])
	h.Write([]byte(secret))
	return subtle.ConstantTimeCompare(h.Sum(nil), resp[4:20]) == 1
}

// errorCause reads the Error-Cause of a NAK (0 if none).
func errorCause(resp []byte) uint32 {
	for b := resp[20:]; len(b) >= 2 && int(b[1]) >= 2 && int(b[1]) <= len(b); b = b[b[1]:] {
		if b[0] == attrErrorCause && b[1] == 6 {
			return binary.BigEndian.Uint32(b[2:6])
		}
	}
	return 0
}

// DisconnectResult is what a switch or access point answered.
type DisconnectResult string

const (
	DisconnectACK     DisconnectResult = "acked"
	DisconnectNAK     DisconnectResult = "refused"
	DisconnectTimeout DisconnectResult = "no_answer"
)

// sendDisconnect sends a Disconnect-Request to addr and waits for the
// answer; three tries, one second apart.
func sendDisconnect(ctx context.Context, addr netip.AddrPort, secret string, attrs []attr) (DisconnectResult, uint32, error) {
	idb := make([]byte, 1)
	if _, err := rand.Read(idb); err != nil {
		return "", 0, err
	}
	req, err := encodeRequest(codeDisconnectRequest, idb[0], attrs, secret)
	if err != nil {
		return "", 0, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr.String())
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()
	buf := make([]byte, 4096)
	for try := 0; try < 3; try++ {
		if _, err := conn.Write(req); err != nil {
			return "", 0, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // timeout (or ICMP unreachable): try again
			}
			resp := buf[:n]
			if !checkResponse(resp, req, secret) {
				continue
			}
			switch resp[0] {
			case codeDisconnectACK:
				return DisconnectACK, 0, nil
			case codeDisconnectNAK:
				return DisconnectNAK, errorCause(resp), nil
			}
		}
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
	}
	return DisconnectTimeout, 0, nil
}
