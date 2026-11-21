// Package radius is goblind's side of RADIUS: FreeRADIUS (configured by
// internal/render/radius) asks the hook here, on loopback, for every
// decision. MAC authentication accepts a device the inventory knows a VLAN
// for (or an unknown one per the configured default), 802.1X users come
// from the configuration, and post-auth replies with the device's VLAN and
// per-device Wi-Fi password. Accounting tells which switch or access point
// and port a device is on; moving a device to another VLAN sends a
// Disconnect-Request (RFC 5176) so it reconnects into the new one at once.
package radius

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
)

// Methods of an authentication.
const (
	MethodMAB    = "mab"
	MethodPEAP   = "peap"
	MethodEAPTLS = "eap-tls"
)

// Results of an authentication.
const (
	ResultAccept     = "accept"
	ResultQuarantine = "quarantine"
	ResultReject     = "reject"
)

// Event is one line of the authentication log.
type Event struct {
	Time   time.Time `json:"time"`
	Client string    `json:"client,omitempty"` // the switch or access point (config name)
	NAS    string    `json:"nas,omitempty"`    // its address
	Port   string    `json:"port,omitempty"`   // NAS-Port-Id or NAS-Port
	MAC    string    `json:"mac,omitempty"`
	User   string    `json:"user,omitempty"`
	Method string    `json:"method"`
	Result string    `json:"result"`
	VLAN   string    `json:"vlan,omitempty"` // VLAN name
	VLANID int       `json:"vlan_id,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// Session is a device a switch or access point let in.
type Session struct {
	MAC       string    `json:"mac"`
	User      string    `json:"user,omitempty"`
	Method    string    `json:"method"`
	Client    string    `json:"client,omitempty"`
	NAS       string    `json:"nas"`
	NASName   string    `json:"nas_identifier,omitempty"`
	Port      string    `json:"port,omitempty"` // NAS-Port-Id, or NAS-Port
	SSID      string    `json:"ssid,omitempty"` // from Called-Station-Id ("ap-mac:SSID")
	VLAN      string    `json:"vlan,omitempty"`
	IP        string    `json:"ip,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
	// what the switch sent, for the Disconnect-Request
	callingID string
}

// Devices is what the manager needs from the device inventory.
type Devices interface {
	VLANOf(mac string) string
}

// Manager answers the hook and keeps sessions and the log.
type Manager struct {
	Logger  *slog.Logger
	Config  func() *config.Config // the running configuration
	Devices Devices
	State   *State // PPSKs and issued client certificates
	// Annotate, when set, gets every device's switch/AP and port (by MAC)
	// whenever they change.
	Annotate func(map[string]map[string]string)
	Now      func() time.Time

	mu       sync.Mutex
	events   []Event // ring, newest last
	sessions map[string]*Session
}

// MaxEvents is how many authentications the log keeps.
const MaxEvents = 1000

// sessions idle this long without accounting are dropped (a switch that
// sends no accounting: the session from post-auth expires a day later).
const sessionTTL = 24 * time.Hour

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m *Manager) cfg() *config.Config {
	if m.Config == nil {
		return &config.Config{}
	}
	if c := m.Config(); c != nil {
		return c
	}
	return &config.Config{}
}

func (m *Manager) record(e Event) {
	e.Time = m.now()
	m.mu.Lock()
	m.events = append(m.events, e)
	if len(m.events) > MaxEvents {
		m.events = append([]Event(nil), m.events[len(m.events)-MaxEvents:]...)
	}
	m.mu.Unlock()
	m.logger().Info("radius", "client", e.Client, "mac", e.MAC, "user", e.User, "method", e.Method, "result", e.Result, "vlan", e.VLAN, "reason", e.Reason)
}

// Events returns the log, newest first, at most limit (0 = all).
func (m *Manager) Events(limit int) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.events)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Event, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, m.events[i])
	}
	return out
}

// Sessions returns the devices let in, newest first.
func (m *Manager) Sessions() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func (m *Manager) expireLocked() {
	cut := m.now().Add(-sessionTTL)
	for k, s := range m.sessions {
		if s.Updated.Before(cut) {
			delete(m.sessions, k)
		}
	}
}

// request is FreeRADIUS's JSON body: {"Attr": {"type": .., "value": [..]}}.
type request map[string]struct {
	Type  string `json:"type"`
	Value []any  `json:"value"`
}

func (r request) get(name string) string {
	a, ok := r[name]
	if !ok || len(a.Value) == 0 {
		return ""
	}
	switch v := a.Value[0].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// reply is rlm_rest's response: "list:Attribute" → value.
type reply map[string]any

// clientOf finds the configured switch or access point of an address.
func clientOf(cfg *config.Config, addr string) (config.RADIUSClient, bool) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return config.RADIUSClient{}, false
	}
	a = a.Unmap()
	best, bits := config.RADIUSClient{}, -1
	for _, c := range cfg.RADIUS.Clients {
		if p, ok := c.Prefix(); ok && p.Contains(a) && p.Bits() > bits {
			best, bits = c, p.Bits()
		}
	}
	return best, bits >= 0
}

// macOf normalizes a MAC in any notation ("" if it is none).
func macOf(s string) string {
	m := dhcpdns.NormalizeMAC(s)
	if len(m) != 12 {
		return ""
	}
	return m
}

// ColonMAC formats a 12-hex MAC as aa:bb:cc:dd:ee:ff.
func ColonMAC(m string) string {
	if len(m) != 12 {
		return m
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(m[i : i+2])
	}
	return b.String()
}

// stripUser drops a Windows domain ("DOMAIN\user") from an identity.
func stripUser(u string) string {
	if i := strings.LastIndexByte(u, '\\'); i >= 0 {
		return u[i+1:]
	}
	return u
}

// vlanOf decides a device's VLAN: the inventory's, else the user's group's;
// a VLAN no longer in the configuration counts as none.
func vlanOf(cfg *config.Config, deviceVLAN, user string) string {
	if deviceVLAN != "" && cfg.VLANID(deviceVLAN) != 0 {
		return deviceVLAN
	}
	if user != "" {
		if u, ok := cfg.RADIUS.RADIUSUserByName(user); ok {
			if v := cfg.RADIUS.GroupVLAN(u.Group); v != "" && cfg.VLANID(v) != 0 {
				return v
			}
		}
	}
	return ""
}

// mab answers MAC authentication: the user name is the device's MAC.
func (m *Manager) mab(cfg *config.Config, req request) (int, reply) {
	user := req.get("User-Name")
	mac := macOf(user)
	nas := req.get("Tmp-String-2")
	client, _ := clientOf(cfg, nas)
	ev := Event{Client: client.Name, NAS: nas, Port: port(req), MAC: mac, Method: MethodMAB}
	if mac == "" {
		ev.User, ev.Result, ev.Reason = user, ResultReject, "not a MAC address (and not 802.1X)"
		m.record(ev)
		return http.StatusUnauthorized, nil
	}
	if !cfg.RADIUS.MAB {
		ev.Result, ev.Reason = ResultReject, "MAC authentication is off"
		m.record(ev)
		return http.StatusUnauthorized, nil
	}
	if vlanOf(cfg, m.deviceVLAN(mac), "") == "" {
		switch cfg.RADIUS.Unknown {
		case "quarantine", "accept":
		default:
			ev.Result, ev.Reason = ResultReject, "unknown device"
			m.record(ev)
			return http.StatusUnauthorized, nil
		}
	}
	// the password a switch sends for MAB is the MAC again (as it wrote it)
	out := reply{"control:Cleartext-Password": user}
	switch {
	case req.get("CHAP-Password") != "":
		out["control:Auth-Type"] = "CHAP"
	case req.get("User-Password") != "":
		out["control:Auth-Type"] = "PAP"
	default: // Service-Type Call-Check without a password
		out["control:Auth-Type"] = "Accept"
	}
	return http.StatusOK, out
}

func (m *Manager) deviceVLAN(mac string) string {
	if m.Devices == nil {
		return ""
	}
	return m.Devices.VLANOf(mac)
}

// inner answers the PEAP tunnel: the user's password for MSCHAPv2.
func (m *Manager) inner(cfg *config.Config, req request) (int, reply) {
	if !cfg.RADIUS.Dot1X {
		return http.StatusUnauthorized, nil
	}
	u, ok := cfg.RADIUS.RADIUSUserByName(stripUser(req.get("User-Name")))
	if !ok {
		return http.StatusNotFound, nil // mschap rejects; post-auth logs it
	}
	return http.StatusOK, reply{"control:Cleartext-Password": u.Password}
}

// postAuth decides the VLAN of an accepted request and records it.
func (m *Manager) postAuth(cfg *config.Config, req request) (int, reply) {
	nas := req.get("Tmp-String-2")
	client, _ := clientOf(cfg, nas)
	calling := req.get("Calling-Station-Id")
	mac := macOf(calling)
	ev := Event{Client: client.Name, NAS: nas, Port: port(req), MAC: mac}
	user := ""
	switch {
	case req.get("TLS-Client-Cert-Common-Name") != "":
		ev.Method = MethodEAPTLS
		user = req.get("TLS-Client-Cert-Common-Name")
		if _, ok := cfg.RADIUS.RADIUSUserByName(user); !ok {
			ev.User, ev.Result, ev.Reason = user, ResultReject, "no enabled user with the certificate's name"
			m.record(ev)
			return http.StatusUnauthorized, nil
		}
		if m.State != nil && !m.State.CertValid(user, req.get("TLS-Client-Cert-Serial")) {
			ev.User, ev.Result, ev.Reason = user, ResultReject, "certificate revoked or not issued by this box"
			m.record(ev)
			return http.StatusUnauthorized, nil
		}
	case req.get("Tmp-String-1") != "":
		ev.Method = MethodPEAP
		user = stripUser(req.get("Tmp-String-1"))
	default:
		ev.Method = MethodMAB
		if mac == "" {
			mac = macOf(req.get("User-Name"))
			ev.MAC = mac
		}
	}
	if u, ok := cfg.RADIUS.RADIUSUserByName(user); ok {
		user = u.Name
	}
	ev.User = user
	vlan := vlanOf(cfg, m.deviceVLAN(mac), user)
	ev.Result = ResultAccept
	if vlan == "" && ev.Method == MethodMAB && cfg.RADIUS.Unknown == "quarantine" {
		vlan, ev.Result = cfg.RADIUS.QuarantineVLAN, ResultQuarantine
	}
	out := reply{}
	if id := cfg.VLANID(vlan); id != 0 {
		ev.VLAN, ev.VLANID = vlan, id
		out["reply:Tunnel-Type"] = "VLAN"
		out["reply:Tunnel-Medium-Type"] = "IEEE-802"
		out["reply:Tunnel-Private-Group-Id"] = strconv.Itoa(id)
	}
	if m.State != nil && mac != "" {
		if psk := m.State.PPSK(mac); psk != "" {
			out["reply:Tunnel-Password"] = psk
		}
	}
	m.record(ev)
	if mac != "" {
		now := m.now()
		m.mu.Lock()
		if m.sessions == nil {
			m.sessions = map[string]*Session{}
		}
		s, ok := m.sessions[mac]
		if !ok {
			s = &Session{MAC: mac, Started: now}
			m.sessions[mac] = s
		}
		s.User, s.Method, s.Client, s.NAS, s.VLAN, s.Updated, s.callingID = user, ev.Method, client.Name, nas, vlan, now, calling
		if v := req.get("NAS-Identifier"); v != "" {
			s.NASName = v
		}
		if p := port(req); p != "" {
			s.Port = p
		}
		if ssid := ssidOf(req.get("Called-Station-Id")); ssid != "" {
			s.SSID = ssid
		}
		m.mu.Unlock()
		m.annotate()
	}
	if len(out) == 0 {
		return http.StatusNoContent, nil
	}
	return http.StatusOK, out
}

// rejected logs a reject (wrong password, unknown user, bad certificate).
func (m *Manager) rejected(cfg *config.Config, req request) {
	nas := req.get("Tmp-String-2")
	client, _ := clientOf(cfg, nas)
	ev := Event{Client: client.Name, NAS: nas, Port: port(req), MAC: macOf(req.get("Calling-Station-Id")), Result: ResultReject}
	switch {
	case req.get("TLS-Client-Cert-Common-Name") != "" || req.get("EAP-Type") == "TLS":
		ev.Method, ev.User = MethodEAPTLS, req.get("TLS-Client-Cert-Common-Name")
	case req.get("Tmp-String-1") != "" || req.get("EAP-Message") != "":
		ev.Method, ev.User = MethodPEAP, stripUser(req.get("Tmp-String-1"))
		if ev.User == "" {
			ev.User = stripUser(req.get("User-Name"))
		}
	default:
		ev.Method, ev.User = MethodMAB, ""
		if ev.MAC == "" {
			ev.MAC = macOf(req.get("User-Name"))
		}
	}
	// inside the PEAP tunnel says more than the outer "rejected again"
	msg := req.get("Tmp-String-3")
	if msg == "" {
		msg = req.get("Module-Failure-Message")
	}
	ev.Reason = reasonOf(msg)
	if ev.Method == MethodMAB {
		// the MAB hook logged its own refusals; what is left is a password
		// that did not match the MAC
		l := strings.ToLower(msg)
		if !strings.Contains(l, "pap") && !strings.Contains(l, "chap") {
			return
		}
		ev.Reason = "wrong password (MAC authentication)"
	}
	if ev.Reason == "" {
		ev.Reason = "rejected"
	}
	m.record(ev)
}

// reasonOf turns FreeRADIUS's failure message into something short.
func reasonOf(msg string) string {
	l := strings.ToLower(msg)
	switch {
	case msg == "":
		return ""
	case strings.Contains(l, "mschap") && (strings.Contains(l, "password") || strings.Contains(l, "response is incorrect")):
		return "wrong password"
	case strings.Contains(l, "cleartext-password") || strings.Contains(l, "no password") || strings.Contains(l, "found no good password"):
		return "unknown or disabled user"
	case strings.Contains(l, "certificate") || strings.Contains(l, "tls alert"):
		return "certificate refused"
	case strings.Contains(l, "proxy"), strings.Contains(l, "previously rejected"):
		return "" // says nothing about why
	}
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// port reads where on the switch the device is.
func port(req request) string {
	if p := req.get("NAS-Port-Id"); p != "" {
		return p
	}
	return req.get("NAS-Port")
}

// ssidOf reads the SSID from Called-Station-Id ("AA-BB-CC-DD-EE-FF:Guest").
func ssidOf(called string) string {
	if i := strings.IndexByte(called, ':'); i >= 12 && i < len(called)-1 && macOf(called[:i]) != "" {
		return called[i+1:]
	}
	return ""
}

// accounting tracks sessions: Start and Interim-Update refresh, Stop ends.
func (m *Manager) accounting(cfg *config.Config, req request) {
	mac := macOf(req.get("Calling-Station-Id"))
	if mac == "" {
		return
	}
	nas := req.get("Tmp-String-2")
	client, _ := clientOf(cfg, nas)
	now := m.now()
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]*Session{}
	}
	status := req.get("Acct-Status-Type")
	switch status { // sent as a number by some FreeRADIUS versions
	case "1":
		status = "Start"
	case "2":
		status = "Stop"
	case "3":
		status = "Interim-Update"
	}
	switch status {
	case "Stop":
		if s, ok := m.sessions[mac]; ok && (s.SessionID == "" || s.SessionID == req.get("Acct-Session-Id")) {
			delete(m.sessions, mac)
		}
	case "Start", "Interim-Update", "Alive":
		s, ok := m.sessions[mac]
		if !ok {
			s = &Session{MAC: mac, Started: now, Method: MethodMAB}
			if u := req.get("User-Name"); macOf(u) == "" {
				s.User, s.Method = stripUser(u), MethodPEAP
			}
			s.VLAN = vlanOf(cfg, m.deviceVLAN(mac), s.User)
			m.sessions[mac] = s
		}
		s.Client, s.NAS, s.Updated, s.callingID = client.Name, nas, now, req.get("Calling-Station-Id")
		s.SessionID = req.get("Acct-Session-Id")
		if v := req.get("NAS-Identifier"); v != "" {
			s.NASName = v
		}
		if p := port(req); p != "" {
			s.Port = p
		}
		if ssid := ssidOf(req.get("Called-Station-Id")); ssid != "" {
			s.SSID = ssid
		}
		if ip := req.get("Framed-IP-Address"); ip != "" {
			s.IP = ip
		}
	default:
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.annotate()
}

// annotate hands every session's place to the device inventory.
func (m *Manager) annotate() {
	if m.Annotate == nil {
		return
	}
	m.mu.Lock()
	set := map[string]map[string]string{}
	for mac, s := range m.sessions {
		f := map[string]string{}
		where := s.NASName
		if where == "" {
			where = s.Client
		}
		if s.SSID != "" {
			f["connection"] = "wifi"
			f["ap"] = clip(where)
			f["ssid"] = clip(s.SSID)
		} else {
			f["connection"] = "wired"
			f["switch"] = clip(where)
			if s.Port != "" {
				f["port"] = clip(s.Port)
			}
		}
		if s.VLAN != "" {
			f["vlan"] = clip(s.VLAN)
		}
		set[mac] = f
	}
	m.mu.Unlock()
	m.Annotate(set)
}

// clip keeps an annotation value printable and at most 64 characters.
func clip(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && b.Len() < 64 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Errors of Disconnect.
var (
	ErrNoSession = errors.New("the device has no RADIUS session")
	ErrNoClient  = errors.New("the switch or access point is not in the RADIUS clients any more")
)

// DisconnectOutcome is what a Disconnect did.
type DisconnectOutcome struct {
	MAC    string           `json:"mac"`
	Client string           `json:"client"`
	NAS    string           `json:"nas"`
	Result DisconnectResult `json:"result"`
	Cause  uint32           `json:"error_cause,omitempty"`
}

// Disconnect asks the switch or access point a device is on to end its
// session; the device reconnects and gets its (new) VLAN.
func (m *Manager) Disconnect(ctx context.Context, mac string) (DisconnectOutcome, error) {
	mac = macOf(mac)
	cfg := m.cfg()
	m.mu.Lock()
	m.expireLocked()
	s, ok := m.sessions[mac]
	var sess Session
	if ok {
		sess = *s
	}
	m.mu.Unlock()
	if !ok || sess.NAS == "" {
		return DisconnectOutcome{MAC: mac}, ErrNoSession
	}
	client, ok := clientOf(cfg, sess.NAS)
	if !ok {
		return DisconnectOutcome{MAC: mac, NAS: sess.NAS}, ErrNoClient
	}
	nas, err := netip.ParseAddr(sess.NAS)
	if err != nil {
		return DisconnectOutcome{MAC: mac, NAS: sess.NAS}, ErrNoClient
	}
	calling := sess.callingID
	if calling == "" {
		calling = strings.ToUpper(strings.ReplaceAll(ColonMAC(mac), ":", "-"))
	}
	attrs := []attr{{attrCallingStationID, []byte(calling)}}
	user := sess.User
	if sess.Method == MethodMAB {
		user = ""
	}
	if user != "" {
		attrs = append(attrs, attr{attrUserName, []byte(user)})
	}
	if sess.SessionID != "" {
		attrs = append(attrs, attr{attrAcctSessionID, []byte(sess.SessionID)})
	}
	if nas.Is4() {
		attrs = append(attrs, attr{attrNASIPAddress, nas.AsSlice()})
	}
	res, cause, err := sendDisconnect(ctx, netip.AddrPortFrom(nas.Unmap(), uint16(client.CoA())), client.Secret, attrs)
	out := DisconnectOutcome{MAC: mac, Client: client.Name, NAS: sess.NAS, Result: res, Cause: cause}
	if err != nil {
		return out, err
	}
	ev := Event{Client: client.Name, NAS: sess.NAS, MAC: mac, User: sess.User, Method: "disconnect", Result: string(res)}
	if cause != 0 {
		ev.Reason = "error cause " + strconv.Itoa(int(cause))
	}
	m.record(ev)
	if res == DisconnectACK {
		m.mu.Lock()
		delete(m.sessions, mac)
		m.mu.Unlock()
		m.annotate()
	}
	return out, nil
}

// Hook serves FreeRADIUS's rlm_rest calls (bind it to loopback only):
// POST /radius/{mab,inner,post-auth,reject,accounting} with HTTP basic auth
// (user HookUser, the box's token).
func (m *Manager) Hook(user, token string) http.Handler {
	mux := http.NewServeMux()
	handle := func(path string, fn func(*config.Config, request) (int, reply)) {
		mux.HandleFunc("POST /radius/"+path, func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(token)) != 1 {
				http.Error(w, "unauthorized", http.StatusForbidden)
				return
			}
			var req request
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			code, out := fn(m.cfg(), req)
			if len(out) == 0 {
				if code == http.StatusOK {
					code = http.StatusNoContent
				}
				w.WriteHeader(code)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(out)
		})
	}
	handle("mab", m.mab)
	handle("inner", m.inner)
	handle("post-auth", m.postAuth)
	handle("reject", func(c *config.Config, req request) (int, reply) {
		m.rejected(c, req)
		return http.StatusNoContent, nil
	})
	handle("accounting", func(c *config.Config, req request) (int, reply) {
		m.accounting(c, req)
		return http.StatusNoContent, nil
	})
	return mux
}

// Prune drops sessions of switches no longer configured (after a commit).
func (m *Manager) Prune() {
	cfg := m.cfg()
	m.mu.Lock()
	changed := false
	for k, s := range m.sessions {
		if _, ok := clientOf(cfg, s.NAS); !ok || !cfg.RADIUSActive() {
			delete(m.sessions, k)
			changed = true
		}
	}
	m.mu.Unlock()
	if changed {
		m.annotate()
	}
}
