package homeassistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeBroker accepts one connection and lets the test script it.
type fakeBroker struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
	r    *bufio.Reader
}

func newFakeBroker(t *testing.T) *fakeBroker {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return &fakeBroker{t: t, ln: ln}
}

func (f *fakeBroker) accept() {
	c, err := f.ln.Accept()
	if err != nil {
		f.t.Error(err)
		return
	}
	f.conn, f.r = c, bufio.NewReader(c)
	f.t.Cleanup(func() { c.Close() })
}

func (f *fakeBroker) read() (byte, []byte) {
	f.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	typ, body, err := readPacket(f.r)
	if err != nil {
		f.t.Errorf("broker read: %v", err)
	}
	return typ, body
}

func (f *fakeBroker) send(h byte, body []byte) {
	f.conn.Write(append(append([]byte{h}, remainingLength(len(body))...), body...))
}

func str(b []byte) (string, []byte) {
	n := int(binary.BigEndian.Uint16(b))
	return string(b[2 : 2+n]), b[2+n:]
}

func TestMQTTClient(t *testing.T) {
	f := newFakeBroker(t)
	connected := make(chan struct{})
	var connect []byte
	go func() {
		f.accept()
		typ, body := f.read()
		if typ != pConnect<<4 {
			t.Errorf("first packet %x", typ)
		}
		connect = body
		f.send(pConnack<<4, []byte{0, 0})
		close(connected)
	}()
	c, err := DialMQTT(context.Background(), MQTTOptions{Addr: f.ln.Addr().String(), ClientID: "goblin-gate-1234", Username: "gate", Password: "pw",
		WillTopic: "goblin_gate/status", WillMsg: []byte("offline"), KeepAlive: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-connected
	// CONNECT: "MQTT", level 4, flags user+pass+will retain+will+clean, keepalive 20
	proto, rest := str(connect)
	if proto != "MQTT" || rest[0] != 4 || rest[1] != 0x80|0x40|0x20|0x04|0x02 || binary.BigEndian.Uint16(rest[2:]) != 20 {
		t.Fatalf("connect header: %q % x", proto, rest[:4])
	}
	rest = rest[4:]
	var id, will, msg, user, pass string
	id, rest = str(rest)
	will, rest = str(rest)
	msg, rest = str(rest)
	user, rest = str(rest)
	pass, _ = str(rest)
	if id != "goblin-gate-1234" || will != "goblin_gate/status" || msg != "offline" || user != "gate" || pass != "pw" {
		t.Fatalf("connect payload: %q %q %q %q %q", id, will, msg, user, pass)
	}

	// a retained publish with a payload over 127 bytes (two-byte length)
	payload := []byte(strings.Repeat("x", 300))
	if err := c.Publish("homeassistant/sensor/a/config", payload, true); err != nil {
		t.Fatal(err)
	}
	typ, body := f.read()
	topic, p := str(body)
	if typ != pPublish<<4|1 || topic != "homeassistant/sensor/a/config" || !bytes.Equal(p, payload) {
		t.Fatalf("publish: %x %q %d bytes", typ, topic, len(p))
	}

	if err := c.Subscribe("goblin_gate/policy/+/set", "homeassistant/status"); err != nil {
		t.Fatal(err)
	}
	typ, body = f.read()
	if typ != pSubscribe<<4|2 {
		t.Fatalf("subscribe header %x", typ)
	}
	pid := body[:2]
	f1, rest := str(body[2:])
	if f1 != "goblin_gate/policy/+/set" || rest[0] != 0 {
		t.Fatalf("subscribe: %q", f1)
	}
	f.send(pSuback<<4, append(append([]byte{}, pid...), 0, 0))

	// a QoS 1 message from the broker: delivered and acknowledged
	in := appendString(nil, "goblin_gate/policy/50/set")
	in = append(in, 0, 7)
	in = append(in, "OFF"...)
	f.send(pPublish<<4|2, in)
	select {
	case m := <-c.Messages():
		if m.Topic != "goblin_gate/policy/50/set" || string(m.Payload) != "OFF" {
			t.Fatalf("message: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}
	typ, body = f.read()
	if typ != pPuback<<4 || binary.BigEndian.Uint16(body) != 7 {
		t.Fatalf("puback: %x % x", typ, body)
	}

	// the broker drops the connection: Done closes
	f.conn.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed")
	}
}

func TestMQTTRefused(t *testing.T) {
	f := newFakeBroker(t)
	go func() {
		f.accept()
		f.read()
		f.send(pConnack<<4, []byte{0, 5})
	}()
	_, err := DialMQTT(context.Background(), MQTTOptions{Addr: f.ln.Addr().String(), ClientID: "x"})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v", err)
	}
}

func TestMQTTRefusedSubscription(t *testing.T) {
	f := newFakeBroker(t)
	go func() {
		f.accept()
		f.read()
		f.send(pConnack<<4, []byte{0, 0})
		_, body := f.read()
		f.send(pSuback<<4, append(append([]byte{}, body[:2]...), 0x80))
	}()
	c, err := DialMQTT(context.Background(), MQTTOptions{Addr: f.ln.Addr().String(), ClientID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	c.Subscribe("a/#")
	select {
	case <-c.Done():
		if !strings.Contains(c.Err().Error(), "refused a subscription") {
			t.Fatalf("err = %v", c.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a refused subscription went unnoticed")
	}
}
