package logexport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// app name and facility for the RFC 5424 lines we emit.
const (
	syslogAppName  = "goblin-gate"
	syslogFacility = 16 // local0
)

// severity maps an event action to its syslog severity: deny/drop = warning,
// alert = notice, everything else info.
func severity(action string) int {
	switch action {
	case "deny", "drop":
		return 4 // warning
	case "alert":
		return 5 // notice
	default:
		return 6 // info
	}
}

// formatSyslog renders ev as one RFC 5424 line. The MSG is the event as
// compact JSON.
func formatSyslog(ev *logs.Event) (string, error) {
	msg, err := json.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("syslog: marshal event: %w", err)
	}
	ts := time.Unix(ev.Time, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	pri := syslogFacility*8 + severity(ev.Action)
	return fmt.Sprintf("<%d>1 %s - - - - %s", pri, ts, msg), nil
}

// sendSyslog delivers evs to the configured syslog server over UDP, TCP or
// TLS. TCP and TLS use octet-counting framing (RFC 6587) and reconnect with
// backoff; UDP is connectionless.
func sendSyslog(ctx context.Context, c config.LogExport, evs []*logs.Event) error {
	proto := c.Protocol
	if proto == "" {
		proto = "udp"
	}
	var lines []string
	for _, ev := range evs {
		line, err := formatSyslog(ev)
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	switch proto {
	case "udp":
		return sendUDP(ctx, c.Address, lines)
	case "tcp", "tls":
		return sendStream(ctx, c.Address, proto, lines)
	default:
		return fmt.Errorf("syslog: unknown protocol %q", proto)
	}
}

func sendUDP(ctx context.Context, addr string, lines []string) error {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return fmt.Errorf("syslog udp dial %s: %w", hostOnly(addr), err)
	}
	defer conn.Close()
	for _, l := range lines {
		if _, err := conn.Write([]byte(l + "\n")); err != nil {
			return fmt.Errorf("syslog udp write: %w", err)
		}
	}
	return nil
}

// sendStream sends framed messages over a persistent TCP/TLS connection,
// reconnecting with backoff (1 s doubling to 60 s) on failure.
func sendStream(ctx context.Context, addr, proto string, lines []string) error {
	backoff := time.Second
	for {
		err := doSendStream(ctx, addr, proto, lines)
		if err == nil || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

func doSendStream(ctx context.Context, addr, proto string, lines []string) error {
	var conn net.Conn
	var err error
	switch proto {
	case "tls":
		cfg := &tls.Config{ServerName: hostOnly(addr)}
		if TLSRoots != nil {
			cfg.RootCAs = TLSRoots
		}
		conn, err = tls.Dial("tcp", addr, cfg)
	default:
		d := net.Dialer{}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("syslog %s dial %s: %w", proto, hostOnly(addr), err)
	}
	defer conn.Close()
	w := bufio.NewWriter(conn)
	for _, l := range lines {
		frame := strconv.Itoa(len(l)) + " " + l + "\n"
		if _, err := w.WriteString(frame); err != nil {
			return fmt.Errorf("syslog %s write: %w", proto, err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("syslog %s flush: %w", proto, err)
	}
	return nil
}

// hostOnly strips the port so errors never leak more than needed and TLS
// ServerName gets a bare hostname.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
