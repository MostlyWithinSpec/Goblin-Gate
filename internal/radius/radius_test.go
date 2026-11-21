package radius

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fakeDevices map[string]string

func (f fakeDevices) VLANOf(mac string) string { return f[macOf(mac)] }

func newManager(t *testing.T, cfg *config.Config) (*Manager, *map[string]map[string]string) {
	t.Helper()
	ann := map[string]map[string]string{}
	m := &Manager{
		Config:   func() *config.Config { return cfg },
		Devices:  fakeDevices{"aabbcc000001": "iot", "aabbcc000002": "gone"},
		State:    &State{Path: filepath.Join(t.TempDir(), "state.json"), Now: func() time.Time { return now }},
		Annotate: func(s map[string]map[string]string) { ann = s },
		Now:      func() time.Time { return now },
	}
	return m, &ann
}

// req builds FreeRADIUS's JSON body.
func req(kv ...string) request {
	r := request{}
	for i := 0; i+1 < len(kv); i += 2 {
		a := r[kv[i]]
		a.Type = "string"
		a.Value = []any{kv[i+1]}
		r[kv[i]] = a
	}
	return r
}

func TestMAB(t *testing.T) {
	cfg := rendertest.Radius()
	m, _ := newManager(t, cfg)
	// known device: accepted, PAP with the MAC as password
	code, out := m.mab(cfg, req("User-Name", "aabbcc000001", "User-Password", "aabbcc000001", "Tmp-String-2", "192.168.1.2"))
	if code != 200 || out["control:Cleartext-Password"] != "aabbcc000001" || out["control:Auth-Type"] != "PAP" {
		t.Errorf("known: %d %v", code, out)
	}
	code, out = m.mab(cfg, req("User-Name", "AA-BB-CC-00-00-01", "CHAP-Password", "x"))
	if code != 200 || out["control:Auth-Type"] != "CHAP" {
		t.Errorf("chap: %d %v", code, out)
	}
	code, out = m.mab(cfg, req("User-Name", "aa:bb:cc:00:00:01"))
	if code != 200 || out["control:Auth-Type"] != "Accept" {
		t.Errorf("call-check: %d %v", code, out)
	}
	// unknown device with quarantine: accepted (post-auth gives the VLAN)
	if code, _ := m.mab(cfg, req("User-Name", "aabbcc0000ff", "User-Password", "aabbcc0000ff")); code != 200 {
		t.Errorf("quarantine: %d", code)
	}
	// unknown with reject; a device whose VLAN is gone counts as unknown
	cfg.RADIUS.Unknown = "reject"
	for _, mac := range []string{"aabbcc0000ff", "aabbcc000002"} {
		if code, _ := m.mab(cfg, req("User-Name", mac, "User-Password", mac)); code != 401 {
			t.Errorf("reject %s: %d", mac, code)
		}
	}
	// not a MAC: refused
	if code, _ := m.mab(cfg, req("User-Name", "alice", "User-Password", "x")); code != 401 {
		t.Errorf("not a MAC: %d", code)
	}
	cfg.RADIUS.MAB = false
	if code, _ := m.mab(cfg, req("User-Name", "aabbcc000001", "User-Password", "aabbcc000001")); code != 401 {
		t.Errorf("MAB off: %d", code)
	}
	ev := m.Events(0)
	if len(ev) != 4 || ev[0].Reason != "MAC authentication is off" || ev[3].Reason != "unknown device" || ev[3].Client != "" {
		t.Errorf("log: %+v", ev)
	}
}

func TestPostAuthVLANs(t *testing.T) {
	cfg := rendertest.Radius()
	m, ann := newManager(t, cfg)
	if err := m.State.SetPPSK("aa:bb:cc:00:00:01", "iot-wifi-pass"); err != nil {
		t.Fatal(err)
	}
	// MAB, known: its VLAN and its PPSK
	code, out := m.postAuth(cfg, req("User-Name", "aabbcc000001", "Calling-Station-Id", "AA-BB-CC-00-00-01", "Tmp-String-2", "192.168.1.66",
		"Called-Station-Id", "11-22-33-44-55-66:Things", "NAS-Identifier", "ap-office"))
	if code != 200 || out["reply:Tunnel-Private-Group-Id"] != "20" || out["reply:Tunnel-Type"] != "VLAN" || out["reply:Tunnel-Medium-Type"] != "IEEE-802" || out["reply:Tunnel-Password"] != "iot-wifi-pass" {
		t.Errorf("known: %d %v", code, out)
	}
	// MAB, unknown: quarantine
	code, out = m.postAuth(cfg, req("User-Name", "aabbcc0000ff", "Calling-Station-Id", "aa:bb:cc:00:00:ff", "Tmp-String-2", "192.168.1.2", "NAS-Port-Id", "Gi1/0/7"))
	if e := m.Events(1)[0]; e.Port != "Gi1/0/7" {
		t.Errorf("port %q", e.Port)
	}
	if code != 200 || out["reply:Tunnel-Private-Group-Id"] != "99" {
		t.Errorf("unknown: %d %v", code, out)
	}
	// unknown with accept: no VLAN attributes
	cfg.RADIUS.Unknown = "accept"
	if code, out := m.postAuth(cfg, req("User-Name", "aabbcc0000fe", "Calling-Station-Id", "aabbcc0000fe")); code != 204 || len(out) != 0 {
		t.Errorf("accept: %d %v", code, out)
	}
	// PEAP: the user's group VLAN (the inner identity, domain stripped)
	code, out = m.postAuth(cfg, req("User-Name", "anonymous", "Tmp-String-1", `CORP\alice`, "Calling-Station-Id", "aabbcc0000aa", "Tmp-String-2", "192.168.1.2"))
	if code != 200 || out["reply:Tunnel-Private-Group-Id"] != "40" {
		t.Errorf("peap: %d %v", code, out)
	}
	// the device's own VLAN wins over the group's
	code, out = m.postAuth(cfg, req("Tmp-String-1", "alice", "Calling-Station-Id", "aabbcc000001"))
	if out["reply:Tunnel-Private-Group-Id"] != "20" {
		t.Errorf("device over group: %d %v", code, out)
	}
	ev := m.Events(0)
	if ev[1].Method != MethodPEAP || ev[1].User != "alice" || ev[1].VLAN != "staff" || ev[1].VLANID != 40 || ev[1].Client != "switch" {
		t.Errorf("peap event: %+v", ev[1])
	}
	if ev[3].Result != ResultQuarantine || ev[3].VLAN != "quarantine" {
		t.Errorf("quarantine event: %+v", ev[3])
	}
	// sessions and where the devices are
	s := m.Sessions()
	if len(s) != 4 {
		t.Fatalf("sessions: %+v", s)
	}
	a := (*ann)["aabbcc0000ff"]
	if a["connection"] != "wired" || a["port"] != "Gi1/0/7" || a["switch"] != "switch" || a["vlan"] != "quarantine" {
		t.Errorf("wired annotation: %v", a)
	}
	if a := (*ann)["aabbcc000001"]; a["connection"] != "wifi" || a["ap"] != "ap-office" || a["ssid"] != "Things" {
		t.Errorf("wifi annotation: %v", a)
	}
}

func TestEAPTLS(t *testing.T) {
	cfg := rendertest.Radius()
	m, _ := newManager(t, cfg)
	makeRoot(t, m.State)
	c, err := m.State.IssueClient("alice", 30)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(c.Certificate))
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || crt.Subject.CommonName != "alice" || crt.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("certificate: %v %+v", err, crt)
	}
	// FreeRADIUS prints the serial as hex bytes
	serial := hex.EncodeToString(crt.SerialNumber.Bytes())
	tls := func(cn, sn string) int {
		code, _ := m.postAuth(cfg, req("User-Name", cn, "TLS-Client-Cert-Common-Name", cn, "TLS-Client-Cert-Serial", sn, "Calling-Station-Id", "aabbcc0000bb"))
		return code
	}
	if code := tls("alice", serial); code != 200 {
		t.Errorf("valid: %d", code)
	}
	if code := tls("alice", "0"+strings.ToUpper(serial)); code != 200 {
		t.Errorf("serial notation: %d", code)
	}
	if code := tls("alice", "deadbeef"); code != 401 {
		t.Errorf("foreign certificate: %d", code)
	}
	if code := tls("bob", serial); code != 401 {
		t.Errorf("disabled user: %d", code)
	}
	if n, err := m.State.RevokeCerts("ALICE", ""); n != 1 || err != nil {
		t.Fatalf("revoke: %d %v", n, err)
	}
	if code := tls("alice", serial); code != 401 {
		t.Errorf("revoked: %d", code)
	}
	if ev := m.Events(1)[0]; ev.Method != MethodEAPTLS || ev.Reason != "certificate revoked or not issued by this box" {
		t.Errorf("event %+v", ev)
	}
	if got := m.State.Certs("alice"); len(got) != 1 || got[0].Revoked == nil {
		t.Errorf("certs %+v", got)
	}
}

func TestInnerAndReject(t *testing.T) {
	cfg := rendertest.Radius()
	m, _ := newManager(t, cfg)
	if code, out := m.inner(cfg, req("User-Name", `CORP\Alice`)); code != 200 || out["control:Cleartext-Password"] != "alice-password" {
		t.Errorf("inner: %d %v", code, out)
	}
	for _, u := range []string{"bob", "nobody"} {
		if code, _ := m.inner(cfg, req("User-Name", u)); code != 404 {
			t.Errorf("%s: %d", u, code)
		}
	}
	m.rejected(cfg, req("User-Name", "anonymous", "Tmp-String-1", "alice", "EAP-Message", "0x02", "Module-Failure-Message", "mschap: MS-CHAP2-Response is incorrect", "Tmp-String-2", "192.168.1.2"))
	m.rejected(cfg, req("User-Name", "aabbcc0000ff")) // MAB: logged by the MAB hook already
	ev := m.Events(0)
	if len(ev) != 1 || ev[0].Reason != "wrong password" || ev[0].User != "alice" || ev[0].Method != MethodPEAP || ev[0].Client != "switch" {
		t.Errorf("events %+v", ev)
	}
	cfg.RADIUS.Dot1X = false
	if code, _ := m.inner(cfg, req("User-Name", "alice")); code != 401 {
		t.Errorf("802.1X off: %d", code)
	}
}

func TestAccounting(t *testing.T) {
	cfg := rendertest.Radius()
	m, ann := newManager(t, cfg)
	m.accounting(cfg, req("Acct-Status-Type", "Start", "Calling-Station-Id", "aa-bb-cc-00-00-01", "User-Name", "aabbcc000001",
		"Acct-Session-Id", "S1", "NAS-Port-Id", "port 3", "Tmp-String-2", "192.168.1.2", "Framed-IP-Address", "192.168.20.50"))
	s := m.Sessions()
	if len(s) != 1 || s[0].SessionID != "S1" || s[0].Port != "port 3" || s[0].Client != "switch" || s[0].VLAN != "iot" || s[0].IP != "192.168.20.50" || s[0].Method != MethodMAB {
		t.Fatalf("session %+v", s)
	}
	if (*ann)["aabbcc000001"]["port"] != "port 3" {
		t.Errorf("annotation %v", *ann)
	}
	// a Stop of another session id leaves it
	m.accounting(cfg, req("Acct-Status-Type", "Stop", "Calling-Station-Id", "aabbcc000001", "Acct-Session-Id", "OLD"))
	if len(m.Sessions()) != 1 {
		t.Error("stopped by an old session")
	}
	m.accounting(cfg, req("Acct-Status-Type", "Stop", "Calling-Station-Id", "aabbcc000001", "Acct-Session-Id", "S1"))
	if len(m.Sessions()) != 0 || len(*ann) != 0 {
		t.Error("not stopped")
	}
	// sessions expire without accounting
	m.postAuth(cfg, req("User-Name", "aabbcc000001", "Calling-Station-Id", "aabbcc000001", "Tmp-String-2", "192.168.1.2"))
	m.Now = func() time.Time { return now.Add(25 * time.Hour) }
	if len(m.Sessions()) != 0 {
		t.Error("session did not expire")
	}
}

func TestHook(t *testing.T) {
	cfg := rendertest.Radius()
	m, _ := newManager(t, cfg)
	srv := httptest.NewServer(m.Hook("freeradius", "tok"))
	defer srv.Close()
	post := func(path, user, pass, body string) (*http.Response, map[string]any) {
		r, _ := http.NewRequest("POST", srv.URL+"/radius/"+path, strings.NewReader(body))
		r.SetBasicAuth(user, pass)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	body := `{"User-Name":{"type":"string","value":["aabbcc000001"]},"User-Password":{"type":"string","value":["aabbcc000001"]},"NAS-Port":{"type":"integer","value":[7]}}`
	if resp, _ := post("mab", "freeradius", "wrong", body); resp.StatusCode != 403 {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	resp, out := post("mab", "freeradius", "tok", body)
	if resp.StatusCode != 200 || out["control:Auth-Type"] != "PAP" {
		t.Errorf("mab: %d %v", resp.StatusCode, out)
	}
	if resp, _ := post("accounting", "freeradius", "tok", `{"Acct-Status-Type":{"type":"integer","value":["Start"]},"Calling-Station-Id":{"type":"string","value":["aabbcc000009"]},"NAS-Port":{"type":"integer","value":[7]}}`); resp.StatusCode != 204 {
		t.Errorf("accounting: %d", resp.StatusCode)
	}
	if s := m.Sessions(); len(s) != 1 || s[0].Port != "7" {
		t.Errorf("numbers as values: %+v", s)
	}
	if resp, _ := post("mab", "freeradius", "tok", `{bad`); resp.StatusCode != 400 {
		t.Errorf("bad body: %d", resp.StatusCode)
	}
}

// fakeNAS answers Disconnect-Requests like a switch: ACK, NAK, or silence.
func fakeNAS(t *testing.T, secret string, answer byte) (netip.AddrPort, chan []byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	got := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			// the request's authenticator must check out
			chk := append([]byte(nil), pkt...)
			copy(chk[4:20], make([]byte, 16))
			sum := md5.Sum(append(chk, secret...))
			if !bytes.Equal(sum[:], pkt[4:20]) {
				continue
			}
			got <- pkt
			if answer == 0 {
				continue
			}
			attrs := []byte{}
			if answer == codeDisconnectNAK {
				attrs = []byte{attrErrorCause, 6, 0, 0, 0x01, 0xf7} // 503 session context not found
			}
			resp := make([]byte, 20+len(attrs))
			resp[0], resp[1] = answer, pkt[1]
			binary.BigEndian.PutUint16(resp[2:], uint16(len(resp)))
			copy(resp[20:], attrs)
			h := md5.New()
			h.Write(resp[:4])
			h.Write(pkt[4:20])
			h.Write(resp[20:])
			h.Write([]byte(secret))
			copy(resp[4:20], h.Sum(nil))
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return netip.MustParseAddrPort(pc.LocalAddr().String()), got
}

func attrsOf(pkt []byte) map[byte]string {
	out := map[byte]string{}
	for b := pkt[20:]; len(b) >= 2 && int(b[1]) <= len(b); b = b[b[1]:] {
		out[b[0]] = string(b[2:b[1]])
	}
	return out
}

func TestDisconnect(t *testing.T) {
	cfg := rendertest.Radius()
	addr, got := fakeNAS(t, "switch-secret-1", codeDisconnectACK)
	cfg.RADIUS.Clients[0].Address = "127.0.0.1"
	cfg.RADIUS.Clients[0].CoAPort = int(addr.Port())
	m, _ := newManager(t, cfg)
	if _, err := m.Disconnect(context.Background(), "aabbcc000001"); !errors.Is(err, ErrNoSession) {
		t.Errorf("no session: %v", err)
	}
	m.accounting(cfg, req("Acct-Status-Type", "Start", "Calling-Station-Id", "AA-BB-CC-00-00-01", "Acct-Session-Id", "S9", "Tmp-String-2", "127.0.0.1"))
	out, err := m.Disconnect(context.Background(), "aa:bb:cc:00:00:01")
	if err != nil || out.Result != DisconnectACK || out.Client != "switch" {
		t.Fatalf("disconnect: %+v %v", out, err)
	}
	a := attrsOf(<-got)
	if a[attrCallingStationID] != "AA-BB-CC-00-00-01" || a[attrAcctSessionID] != "S9" || a[attrNASIPAddress] != string([]byte{127, 0, 0, 1}) {
		t.Errorf("attributes %q", a)
	}
	if _, ok := a[attrUserName]; ok {
		t.Error("MAB session sent a user name")
	}
	if len(m.Sessions()) != 0 {
		t.Error("session kept after ACK")
	}
	// a NAK carries its cause
	addr2, _ := fakeNAS(t, "switch-secret-1", codeDisconnectNAK)
	cfg.RADIUS.Clients[0].CoAPort = int(addr2.Port())
	m.accounting(cfg, req("Acct-Status-Type", "Start", "Calling-Station-Id", "aabbcc000001", "Tmp-String-2", "127.0.0.1"))
	out, err = m.Disconnect(context.Background(), "aabbcc000001")
	if err != nil || out.Result != DisconnectNAK || out.Cause != 503 {
		t.Errorf("nak: %+v %v", out, err)
	}
	// a switch removed from the clients
	cfg.RADIUS.Clients[0].Address = "192.168.1.2"
	if _, err := m.Disconnect(context.Background(), "aabbcc000001"); !errors.Is(err, ErrNoClient) {
		t.Errorf("removed client: %v", err)
	}
	m.Prune()
	if len(m.Sessions()) != 0 {
		t.Error("prune kept a session of a removed client")
	}
}

func TestDisconnectTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 3 s")
	}
	addr, _ := fakeNAS(t, "s", 0)
	res, _, err := sendDisconnect(context.Background(), addr, "s", []attr{{attrCallingStationID, []byte("x")}})
	if err != nil || res != DisconnectTimeout {
		t.Errorf("%v %v", res, err)
	}
}

func TestPacketChecks(t *testing.T) {
	req, err := encodeRequest(codeDisconnectRequest, 7, []attr{{attrUserName, []byte("a")}, {attrAcctSessionID, nil}}, "s")
	if err != nil || len(req) != 23 {
		t.Fatalf("%v %d", err, len(req))
	}
	if _, err := encodeRequest(codeDisconnectRequest, 1, []attr{{attrUserName, make([]byte, 254)}}, "s"); err == nil {
		t.Error("long attribute")
	}
	resp := make([]byte, 20)
	resp[0], resp[1] = codeDisconnectACK, 7
	binary.BigEndian.PutUint16(resp[2:], 20)
	if checkResponse(resp, req, "s") {
		t.Error("unsigned response accepted")
	}
	if checkResponse(resp[:10], req, "s") {
		t.Error("short response accepted")
	}
}

func TestPPSK(t *testing.T) {
	st := &State{Path: filepath.Join(t.TempDir(), "radius", "state.json"), Now: func() time.Time { return now }}
	for _, bad := range []string{"short", strings.Repeat("x", 64), "tab\there!"} {
		if err := st.SetPPSK("aabbcc000001", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := st.SetPPSK("nope", "long-enough"); err == nil {
		t.Error("bad MAC accepted")
	}
	if err := st.SetPPSK("AA:BB:CC:00:00:01", "long enough!"); err != nil {
		t.Fatal(err)
	}
	st2 := &State{Path: st.Path}
	if st2.PPSK("aabbcc000001") != "long enough!" || len(st2.PPSKs()) != 1 {
		t.Error("not saved")
	}
	if fi, err := os.Stat(st.Path); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Errorf("state file mode: %v", err)
	}
	if err := st2.SetPPSK("aabbcc000001", ""); err != nil || st2.PPSK("aabbcc000001") != "" {
		t.Error("not removed")
	}
}

// makeRoot writes a CA root like Caddy's (EC key, "EC PRIVATE KEY").
func makeRoot(t *testing.T, st *State) *x509.Certificate {
	t.Helper()
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: now.Add(-time.Hour),
		NotAfter: now.Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	st.RootCert, st.RootKey = filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")
	_ = os.WriteFile(st.RootCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(st.RootKey, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestServerCert(t *testing.T) {
	st := &State{Path: filepath.Join(t.TempDir(), "state.json"), Now: func() time.Time { return now },
		RootCert: filepath.Join(t.TempDir(), "none.crt"), RootKey: filepath.Join(t.TempDir(), "none.key")}
	if _, _, _, err := st.ServerCert("gate"); !errors.Is(err, ErrNoCA) {
		t.Errorf("no CA: %v", err)
	}
	root := makeRoot(t, st)
	c1, k1, ca, err := st.ServerCert("gate")
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(c1)
	crt, _ := x509.ParseCertificate(blk.Bytes)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	if _, err := crt.Verify(x509.VerifyOptions{Roots: pool, DNSName: "gate", CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if !bytes.Contains(ca, []byte("BEGIN CERTIFICATE")) || !bytes.Contains(k1, []byte("PRIVATE KEY")) {
		t.Error("PEM")
	}
	// the same name and root: the same certificate (renders stay stable)
	c2, _, _, _ := st.ServerCert("gate")
	if !bytes.Equal(c1, c2) {
		t.Error("reissued without a reason")
	}
	// another name, or close to expiry: a new one
	c3, _, _, _ := st.ServerCert("gate2")
	if bytes.Equal(c1, c3) {
		t.Error("not reissued for a new name")
	}
	st.Now = func() time.Time { return now.Add(2*365*24*time.Hour - 10*24*time.Hour) }
	c4, _, _, _ := st.ServerCert("gate2")
	if bytes.Equal(c3, c4) {
		t.Error("not renewed before expiry")
	}
	if _, err := st.IssueClient("alice", MaxCertDays+1); err == nil {
		t.Error("too long accepted")
	}
}
