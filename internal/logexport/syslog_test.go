package logexport

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// TestSeverityMapping checks the action→severity table.
func TestSeverityMapping(t *testing.T) {
	cases := []struct {
		action string
		want   int
	}{
		{"deny", 4},
		{"drop", 4},
		{"alert", 5},
		{"allow", 6},
		{"", 6},
	}
	for _, c := range cases {
		if got := severity(c.action); got != c.want {
			t.Errorf("severity(%q) = %d, want %d", c.action, got, c.want)
		}
	}
}

// TestFormatSyslog checks the RFC 5424 line shape and that the MSG is the
// event as compact JSON.
func TestFormatSyslog(t *testing.T) {
	ev := &logs.Event{Time: 1700000000, Kind: "traffic", Action: "deny", SrcIP: "10.0.0.1", DstIP: "10.0.0.2"}
	line, err := formatSyslog(ev)
	if err != nil {
		t.Fatal(err)
	}
	// <PRI>VERSION SP TIMESTAMP SP HOSTNAME SP APP-NAME SP PROCID SP MSGID SP [SD] MSG
	re := regexp.MustCompile(`^<(\d+)>1\s\S+\s-\s-\s-\s-\s(.+)$`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("line does not match RFC 5424 shape: %q", line)
	}
	pri, _ := strconv.Atoi(m[1])
	// facility local0 (16) * 8 + severity warning (4) = 132
	if pri != 16*8+4 {
		t.Errorf("PRI = %d, want %d", pri, 16*8+4)
	}
	var decoded logs.Event
	if err := json.Unmarshal([]byte(m[2]), &decoded); err != nil {
		t.Fatalf("MSG is not JSON: %v", err)
	}
	if decoded.Kind != "traffic" || decoded.Action != "deny" {
		t.Errorf("decoded = %+v, want traffic/deny", decoded)
	}
}

// TestSendUDP runs a UDP listener and checks the sender delivers RFC 5424
// lines.
func TestSendUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	addr := pc.LocalAddr().String()

	got := make(chan string, 4)
	go func() {
		buf := make([]byte, 65536)
		for i := 0; i < 2; i++ {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()

	evs := []*logs.Event{
		{Time: 1700000000, Kind: "traffic", Action: "deny"},
		{Time: 1700000001, Kind: "ips", Action: "alert"},
	}
	c := config.LogExport{Name: "u", Type: "syslog", Address: addr, Protocol: "udp"}
	if err := sendSyslog(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}

	line1 := <-got
	line2 := <-got
	if !strings.HasPrefix(line1, "<132>1 ") {
		t.Errorf("line1 prefix = %q", line1[:7])
	}
	if !strings.HasPrefix(line2, "<133>1 ") { // alert → notice (5): 16*8+5=133
		t.Errorf("line2 prefix = %q", line2[:7])
	}
}

// TestSendTCP runs a TCP listener and checks octet-counting framing.
func TestSendTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type frame struct {
		count int
		msg   string
	}
	frames := make(chan frame, 4)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			sp := strings.IndexByte(line, ' ')
			n, _ := strconv.Atoi(line[:sp])
			frames <- frame{count: n, msg: line[sp+1 : sp+1+n]}
		}
	}()

	evs := []*logs.Event{{Time: 1700000000, Kind: "traffic", Action: "allow"}}
	c := config.LogExport{Name: "t", Type: "syslog", Address: ln.Addr().String(), Protocol: "tcp"}
	if err := sendSyslog(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}

	f := <-frames
	if f.count != len(f.msg) {
		t.Errorf("octet count = %d, message length = %d", f.count, len(f.msg))
	}
	if !strings.HasPrefix(f.msg, "<134>1 ") { // allow → info (6): 16*8+6=134
		t.Errorf("msg prefix = %q", f.msg[:7])
	}
}

// testTLSCert builds a self-signed cert for "localhost" and returns the TLS
// config plus a root pool that trusts it.
func testTLSCert(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return &tls.Config{Certificates: []tls.Certificate{cert}}, pool
}

// TestSendTLS runs a TLS listener, injects the root pool, and checks the
// sender delivers framed messages.
func TestSendTLS(t *testing.T) {
	tlsCfg, pool := testTLSCert(t)
	oldRoots := TLSRoots
	TLSRoots = pool
	defer func() { TLSRoots = oldRoots }()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type frame struct{ count int }
	frames := make(chan frame, 2)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(line, "\n")
		sp := strings.IndexByte(line, ' ')
		n, _ := strconv.Atoi(line[:sp])
		frames <- frame{count: n}
	}()

	evs := []*logs.Event{{Time: 1700000000, Kind: "dns", Action: "allow"}}
	c := config.LogExport{Name: "tls", Type: "syslog", Address: ln.Addr().String(), Protocol: "tls"}
	if err := sendSyslog(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}
	f := <-frames
	if f.count == 0 {
		t.Error("octet count was 0")
	}
}
