package homeassistant

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// A small MQTT 3.1.1 client: what the bridge needs and nothing more.
// Publishes are QoS 0 (retained where Home Assistant expects it), the
// subscriptions are QoS 0, and a last will marks the box offline when the
// connection drops.

// Message is a PUBLISH received from the broker.
type Message struct {
	Topic   string
	Payload []byte
}

// MQTTOptions configure a connection.
type MQTTOptions struct {
	Addr      string      // host:port
	TLS       *tls.Config // nil = plain TCP
	ClientID  string
	Username  string
	Password  string
	WillTopic string // "" = no last will
	WillMsg   []byte
	KeepAlive time.Duration // 0 = 30 s
}

// MQTT is one broker connection. After Done is closed, Err says why.
type MQTT struct {
	conn net.Conn
	wmu  sync.Mutex
	msgs chan Message
	done chan struct{}
	once sync.Once
	err  error
	pid  uint16
	seen chan struct{} // any packet from the broker (keepalive)
}

const (
	pConnect    = 1
	pConnack    = 2
	pPublish    = 3
	pPuback     = 4
	pSubscribe  = 8
	pSuback     = 9
	pPingreq    = 12
	pPingresp   = 13
	pDisconnect = 14
)

var connackErrors = map[byte]string{
	1: "unacceptable protocol version",
	2: "client identifier rejected",
	3: "broker unavailable",
	4: "bad user name or password",
	5: "not authorized",
}

// DialMQTT connects and logs in.
func DialMQTT(ctx context.Context, o MQTTOptions) (*MQTT, error) {
	if o.KeepAlive == 0 {
		o.KeepAlive = 30 * time.Second
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if o.TLS != nil {
		conn, err = (&tls.Dialer{NetDialer: d, Config: o.TLS}).DialContext(ctx, "tcp", o.Addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", o.Addr)
	}
	if err != nil {
		return nil, err
	}
	c := &MQTT{conn: conn, msgs: make(chan Message, 64), done: make(chan struct{}), seen: make(chan struct{}, 1)}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := c.write(pConnect<<4, connectBody(o)); err != nil {
		conn.Close()
		return nil, err
	}
	r := bufio.NewReader(conn)
	typ, body, err := readPacket(r)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mqtt: no answer to connect: %w", err)
	}
	if typ>>4 != pConnack || len(body) != 2 {
		conn.Close()
		return nil, errors.New("mqtt: the broker did not acknowledge the connection")
	}
	if code := body[1]; code != 0 {
		conn.Close()
		msg := connackErrors[code]
		if msg == "" {
			msg = fmt.Sprintf("code %d", code)
		}
		return nil, fmt.Errorf("mqtt: connection refused: %s", msg)
	}
	conn.SetDeadline(time.Time{})
	go c.readLoop(r)
	go c.keepAlive(o.KeepAlive)
	return c, nil
}

func connectBody(o MQTTOptions) []byte {
	var b []byte
	b = appendString(b, "MQTT")
	b = append(b, 4)    // 3.1.1
	flags := byte(0x02) // clean session
	if o.WillTopic != "" {
		flags |= 0x04 | 0x20 // will, retained, QoS 0
	}
	if o.Username != "" {
		flags |= 0x80
		if o.Password != "" {
			flags |= 0x40
		}
	}
	b = append(b, flags)
	b = binary.BigEndian.AppendUint16(b, uint16(o.KeepAlive/time.Second))
	b = appendString(b, o.ClientID)
	if o.WillTopic != "" {
		b = appendString(b, o.WillTopic)
		b = appendBytes(b, o.WillMsg)
	}
	if o.Username != "" {
		b = appendString(b, o.Username)
		if o.Password != "" {
			b = appendString(b, o.Password)
		}
	}
	return b
}

// Publish sends a QoS 0 message.
func (c *MQTT) Publish(topic string, payload []byte, retain bool) error {
	h := byte(pPublish << 4)
	if retain {
		h |= 1
	}
	body := appendString(nil, topic) // QoS 0: no packet id; the payload is the rest
	return c.write(h, append(body, payload...))
}

// Subscribe asks for the topic filters at QoS 0.
func (c *MQTT) Subscribe(filters ...string) error {
	c.wmu.Lock()
	c.pid++
	if c.pid == 0 {
		c.pid = 1
	}
	b := binary.BigEndian.AppendUint16(nil, c.pid)
	c.wmu.Unlock()
	for _, f := range filters {
		b = appendString(b, f)
		b = append(b, 0)
	}
	return c.write(pSubscribe<<4|2, b)
}

// Messages delivers what the broker sends for the subscriptions.
func (c *MQTT) Messages() <-chan Message { return c.msgs }

// Done is closed when the connection is gone.
func (c *MQTT) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (after Done).
func (c *MQTT) Err() error { return c.err }

// Close disconnects cleanly (the broker does not send the last will).
func (c *MQTT) Close() error {
	_ = c.write(pDisconnect<<4, nil)
	c.fail(errors.New("mqtt: closed"))
	return nil
}

func (c *MQTT) fail(err error) {
	c.once.Do(func() {
		c.err = err
		c.conn.Close()
		close(c.done)
	})
}

func (c *MQTT) write(header byte, body []byte) error {
	select {
	case <-c.done:
		return c.err
	default:
	}
	pkt := append([]byte{header}, remainingLength(len(body))...)
	pkt = append(pkt, body...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := c.conn.Write(pkt); err != nil {
		c.fail(fmt.Errorf("mqtt: write: %w", err))
		return err
	}
	return nil
}

func (c *MQTT) readLoop(r *bufio.Reader) {
	defer close(c.msgs)
	for {
		typ, body, err := readPacket(r)
		if err != nil {
			c.fail(fmt.Errorf("mqtt: read: %w", err))
			return
		}
		select {
		case c.seen <- struct{}{}:
		default:
		}
		switch typ >> 4 {
		case pPublish:
			m, pid, qos, err := parsePublish(typ, body)
			if err != nil {
				c.fail(err)
				return
			}
			if qos == 1 {
				_ = c.write(pPuback<<4, binary.BigEndian.AppendUint16(nil, pid))
			}
			select {
			case c.msgs <- m:
			case <-c.done:
				return
			}
		case pSuback:
			for _, rc := range body[min(2, len(body)):] {
				if rc == 0x80 {
					c.fail(errors.New("mqtt: the broker refused a subscription (check the user's ACL)"))
					return
				}
			}
		case pPingresp, pPuback:
		}
	}
}

// keepAlive pings at half the interval and gives up when the broker has
// been silent for one and a half.
func (c *MQTT) keepAlive(every time.Duration) {
	ping := time.NewTicker(every / 2)
	defer ping.Stop()
	last := time.Now()
	for {
		select {
		case <-c.done:
			return
		case <-c.seen:
			last = time.Now()
		case <-ping.C:
			if time.Since(last) > every*3/2 {
				c.fail(errors.New("mqtt: the broker stopped answering"))
				return
			}
			_ = c.write(pPingreq<<4, nil)
		}
	}
}

func readPacket(r *bufio.Reader) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for i := 0; ; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(b&0x7f) * mult
		if b&0x80 == 0 {
			break
		}
		if i == 3 {
			return 0, nil, errors.New("mqtt: bad remaining length")
		}
		mult *= 128
	}
	if n > 1<<20 {
		return 0, nil, fmt.Errorf("mqtt: packet of %d bytes is too large", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return typ, body, nil
}

func parsePublish(typ byte, b []byte) (Message, uint16, byte, error) {
	qos := (typ >> 1) & 3
	if len(b) < 2 {
		return Message{}, 0, 0, errors.New("mqtt: short publish")
	}
	tl := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+tl {
		return Message{}, 0, 0, errors.New("mqtt: short publish topic")
	}
	m := Message{Topic: string(b[2 : 2+tl])}
	rest := b[2+tl:]
	var pid uint16
	if qos > 0 {
		if len(rest) < 2 {
			return Message{}, 0, 0, errors.New("mqtt: short publish id")
		}
		pid = binary.BigEndian.Uint16(rest)
		rest = rest[2:]
	}
	m.Payload = rest
	return m, pid, qos, nil
}

func remainingLength(n int) []byte {
	var out []byte
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func appendBytes(b []byte, p []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
	return append(b, p...)
}
