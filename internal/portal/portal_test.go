package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

type fakeNFT struct {
	mu      sync.Mutex
	scripts []string
}

func (f *fakeNFT) run(b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, string(b))
	return nil
}

func (f *fakeNFT) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.scripts, "")
}

var (
	t0     = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	mac1   = net.HardwareAddr{0xaa, 0xbb, 0xcc, 0, 0, 1}
	guest1 = netip.MustParseAddr("192.168.30.100")
)

func newManager(t *testing.T, cfg *config.Config) (*Manager, *fakeNFT, *time.Time) {
	t.Helper()
	f := &fakeNFT{}
	now := t0
	m := &Manager{Path: filepath.Join(t.TempDir(), "portal", "state.json"), RunNFT: f.run, Now: func() time.Time { return now },
		Config: func() (*config.Config, error) { return cfg, nil }}
	return m, f, &now
}

func TestClickAndPassword(t *testing.T) {
	m, f, now := newManager(t, rendertest.Portal())
	click := config.CaptivePortal{Interface: "guest", Enabled: true, Mode: "click"}
	if _, err := m.Login("guest", click, guest1, mac1, false, "", ""); !errors.Is(err, ErrTerms) {
		t.Fatalf("no terms accepted: %v", err)
	}
	if _, err := m.Login("guest", click, guest1, nil, true, "", ""); !errors.Is(err, ErrNoClient) {
		t.Fatalf("unknown MAC: %v", err)
	}
	s, err := m.Login("guest", click, guest1, mac1, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Expires != t0.Add(24*time.Hour) || s.MAC != "aa:bb:cc:00:00:01" {
		t.Errorf("session %+v", s)
	}
	if !strings.Contains(f.all(), "add element inet goblin portal_ok { aa:bb:cc:00:00:01 . 192.168.30.100 timeout 86400s }") {
		t.Errorf("nft: %s", f.all())
	}
	if _, ok := m.Find("192.168.30.100"); !ok {
		t.Error("not found after login")
	}
	*now = t0.Add(25 * time.Hour)
	if _, ok := m.Find("192.168.30.100"); ok {
		t.Error("found after the session ended")
	}

	pw := config.CaptivePortal{Interface: "guest", Enabled: true, Mode: "password", Password: "letmein", SessionMinutes: 60}
	if _, err := m.Login("guest", pw, guest1, mac1, true, "nope", ""); !errors.Is(err, ErrPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := m.Login("guest", pw, guest1, mac1, false, "letmein", ""); err != nil {
		t.Fatalf("password without terms text: %v", err)
	}
	pw.Text = "Terms."
	if _, err := m.Login("guest", pw, guest1, mac1, false, "letmein", ""); !errors.Is(err, ErrTerms) {
		t.Fatalf("terms text not accepted: %v", err)
	}
	// ten wrong answers in a minute and even the right one waits
	for range 10 {
		_, _ = m.Login("guest", pw, guest1, mac1, true, "nope", "")
	}
	if _, err := m.Login("guest", pw, guest1, mac1, true, "letmein", ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("no rate limit: %v", err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := m.Login("guest", pw, guest1, mac1, true, "letmein", ""); err != nil {
		t.Fatalf("after waiting: %v", err)
	}
	if n := len(m.Clients()); n != 1 {
		t.Errorf("%d sessions, want one per client", n)
	}
}

func TestVouchers(t *testing.T) {
	m, _, now := newManager(t, rendertest.Portal())
	p := config.CaptivePortal{Interface: "guest", Enabled: true, Mode: "voucher"}
	if _, err := m.NewBatch(0, 60, 1, 0, "", ""); err == nil {
		t.Error("empty batch accepted")
	}
	if _, err := m.NewBatch(3, 0, 2, 7, "conference", ""); err == nil {
		t.Fatal("0-minute vouchers accepted")
	}
	b, err := m.NewBatch(3, 60, 2, 7, "conference", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Codes) != 3 || !regexp.MustCompile(`^[A-HJ-KM-NP-Z2-9]{4}-[A-HJ-KM-NP-Z2-9]{4}$`).MatchString(b.Codes[0].Code) {
		t.Fatalf("codes %v", b.Codes)
	}
	code := b.Codes[0].Code
	if _, err := m.Login("guest", p, guest1, mac1, false, "", "WRONG-CODE"); !errors.Is(err, ErrVoucher) {
		t.Fatalf("unknown code: %v", err)
	}
	// case and dashes do not matter
	s, err := m.Login("guest", p, guest1, mac1, false, "", strings.ToLower(strings.ReplaceAll(code, "-", "")))
	if err != nil {
		t.Fatal(err)
	}
	if s.Voucher != code || s.Batch != b.ID || s.Expires != t0.Add(time.Hour) {
		t.Errorf("session %+v", s)
	}
	mac2 := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0, 0, 2}
	if _, err := m.Login("guest", p, netip.MustParseAddr("192.168.30.101"), mac2, false, "", code); err != nil {
		t.Fatalf("second use: %v", err)
	}
	if _, err := m.Login("guest", p, netip.MustParseAddr("192.168.30.102"), net.HardwareAddr{1, 2, 3, 4, 5, 6}, false, "", code); !errors.Is(err, ErrVoucher) {
		t.Fatalf("third use of a 2-use code: %v", err)
	}
	got := m.Batches()[0]
	if st := got.State(got.Codes[0], *now); st != "used_up" {
		t.Errorf("state %s", st)
	}
	if st := got.State(got.Codes[1], *now); st != "unused" {
		t.Errorf("state %s", st)
	}
	// the codes expire after their validity
	*now = t0.Add(8 * 24 * time.Hour)
	if _, err := m.Login("guest", p, guest1, mac1, false, "", got.Codes[1].Code); !errors.Is(err, ErrVoucher) {
		t.Fatalf("expired code: %v", err)
	}
	// a batch for another portal does not open this one
	*now = t0
	other, _ := m.NewBatch(1, 60, 1, 0, "", "lobby")
	if _, err := m.Login("guest", p, guest1, mac1, false, "", other.Codes[0].Code); !errors.Is(err, ErrVoucher) {
		t.Fatalf("code of another portal: %v", err)
	}
}

func TestRevokeAndKick(t *testing.T) {
	m, f, _ := newManager(t, rendertest.Portal())
	p := config.CaptivePortal{Interface: "guest", Enabled: true, Mode: "voucher"}
	b, _ := m.NewBatch(2, 60, 1, 0, "", "")
	if _, err := m.Login("guest", p, guest1, mac1, false, "", b.Codes[0].Code); err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(b.ID, b.Codes[0].Code); err != nil {
		t.Fatal(err)
	}
	if len(m.Clients()) != 0 {
		t.Error("client of a revoked code stays")
	}
	if !strings.Contains(f.all(), "delete element inet goblin portal_ok { aa:bb:cc:00:00:01 . 192.168.30.100 }") {
		t.Errorf("not removed from nft: %s", f.all())
	}
	if _, err := m.Login("guest", p, guest1, mac1, false, "", b.Codes[0].Code); !errors.Is(err, ErrVoucher) {
		t.Fatal("revoked code works")
	}
	if err := m.Revoke(b.ID, "NOPE-NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
	if _, err := m.Login("guest", config.CaptivePortal{Mode: "click"}, guest1, mac1, true, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Kick("AA:BB:CC:00:00:01"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Kick("aa:bb:cc:00:00:01"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second kick: %v", err)
	}
	if err := m.DeleteBatch(b.ID); err != nil || len(m.Batches()) != 0 {
		t.Errorf("delete: %v", err)
	}
}

// Sessions survive a restart and go back into the sets after a ruleset load;
// sessions of a removed portal end.
func TestResync(t *testing.T) {
	cfg := rendertest.Portal()
	m, _, now := newManager(t, cfg)
	if _, err := m.Login("guest", config.CaptivePortal{Mode: "click"}, guest1, mac1, true, "", ""); err != nil {
		t.Fatal(err)
	}
	f2 := &fakeNFT{}
	later := t0.Add(time.Hour)
	m2 := &Manager{Path: m.Path, RunNFT: f2.run, Now: func() time.Time { return later }}
	m2.Resync(cfg)
	if !strings.Contains(f2.all(), "aa:bb:cc:00:00:01 . 192.168.30.100 timeout 82800s") {
		t.Errorf("not refilled with the time left: %q", f2.all())
	}
	cfg2 := rendertest.Portal()
	cfg2.CaptivePortals = nil
	m2.Resync(cfg2)
	if len(m2.Clients()) != 0 {
		t.Error("session kept without a portal")
	}
	_ = now
	b, _ := os.ReadFile(m.Path)
	if fi, _ := os.Stat(m.Path); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("state file mode %v", fi.Mode())
	}
	if !json.Valid(b) {
		t.Error("state not JSON")
	}
}

func TestSweepIdle(t *testing.T) {
	cfg := rendertest.Portal() // guest: idle 30 min
	m, _, now := newManager(t, cfg)
	seen := map[string]bool{}
	m.Seen = func() (map[string]bool, error) { return seen, nil }
	p := cfg.CaptivePortals[0]
	b, _ := m.NewBatch(1, 600, 1, 0, "", "")
	if _, err := m.Login("guest", p, guest1, mac1, true, "", b.Codes[0].Code); err != nil {
		t.Fatal(err)
	}
	m.Sweep() // a fresh session is not idle yet
	if len(m.Clients()) != 1 {
		t.Fatal("fresh session ended")
	}
	*now = t0.Add(40 * time.Minute)
	seen["aa:bb:cc:00:00:01 192.168.30.100"] = true
	m.Sweep()
	if len(m.Clients()) != 1 {
		t.Fatal("active session ended")
	}
	delete(seen, "aa:bb:cc:00:00:01 192.168.30.100")
	m.Sweep()
	if len(m.Clients()) != 0 {
		t.Fatal("idle session kept")
	}
}

// --- the page server

func request(method, target string, body url.Values, client string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.RemoteAddr = client + ":50000"
	local := &net.TCPAddr{IP: net.ParseIP("192.168.30.1"), Port: config.PortalPort}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestServer(t *testing.T) {
	cfg := rendertest.Portal()
	cfg.CaptivePortals[0].Mode = "click"
	m, _, _ := newManager(t, cfg)
	m.Now = nil // the page uses the wall clock
	s := NewServer(m, func() (*config.Config, error) { return cfg, nil }, func(netip.Addr) net.HardwareAddr { return mac1 })

	// an OS probe (redirected HTTP) goes to the portal page
	w := serve(s, request("GET", "http://captive.apple.com/hotspot-detect.html", nil, "192.168.30.100"))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "http://192.168.30.1:8880/" {
		t.Fatalf("probe: %d %v", w.Code, w.Header())
	}
	// RFC 8908 API
	w = serve(s, request("GET", "http://192.168.30.1:8880/captive-portal/api", nil, "192.168.30.100"))
	if w.Header().Get("Content-Type") != "application/captive+json" || !strings.Contains(w.Body.String(), `"captive":true`) {
		t.Fatalf("api: %s", w.Body)
	}
	// the page
	w = serve(s, request("GET", "http://192.168.30.1:8880/", nil, "192.168.30.100"))
	page := w.Body.String()
	if w.Code != 200 || !strings.Contains(page, "<h1>Guest Wi-Fi</h1>") || !strings.Contains(page, "<p>Be nice.</p>") ||
		!strings.Contains(page, "I accept the terms") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("page: %d %s", w.Code, page)
	}
	token := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`).FindStringSubmatch(page)[1]

	// no token, a wrong one, or another client's: refused
	w = serve(s, request("POST", "http://192.168.30.1:8880/login", url.Values{"accept": {"on"}}, "192.168.30.100"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("no token: %d", w.Code)
	}
	w = serve(s, request("POST", "http://192.168.30.1:8880/login", url.Values{"accept": {"on"}, "token": {token}}, "192.168.30.101"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("another client's token: %d", w.Code)
	}
	w = serve(s, request("POST", "http://192.168.30.1:8880/login", url.Values{"token": {token}}, "192.168.30.100"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Accept the terms to continue.") {
		t.Errorf("terms not accepted: %d %s", w.Code, w.Body)
	}
	w = serve(s, request("POST", "http://192.168.30.1:8880/login", url.Values{"accept": {"on"}, "token": {token}}, "192.168.30.100"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	w = serve(s, request("GET", "http://192.168.30.1:8880/", nil, "192.168.30.100"))
	if !strings.Contains(w.Body.String(), "You are online.") || !strings.Contains(w.Body.String(), "minutes left") {
		t.Errorf("online page: %s", w.Body)
	}
	w = serve(s, request("GET", "http://192.168.30.1:8880/captive-portal/api", nil, "192.168.30.100"))
	if !strings.Contains(w.Body.String(), `"captive":false`) || !strings.Contains(w.Body.String(), `"seconds-remaining":`) {
		t.Errorf("api after login: %s", w.Body)
	}
	// a client from outside the portal network, or the portal turned off: nothing
	if w := serve(s, request("GET", "http://192.168.30.1:8880/", nil, "192.168.1.50")); w.Code != http.StatusNotFound {
		t.Errorf("other network: %d", w.Code)
	}
	cfg.CaptivePortals[0].Enabled = false
	if w := serve(s, request("GET", "http://192.168.30.1:8880/", nil, "192.168.30.100")); w.Code != http.StatusNotFound {
		t.Errorf("disabled portal: %d", w.Code)
	}
}

func TestPageModesAndEscaping(t *testing.T) {
	p := config.CaptivePortal{Mode: "voucher", Title: `<script>x</script>`, Text: "a\n\nb", Logo: "data:image/png;base64,iVBORw0KGgo="}
	w := httptest.NewRecorder()
	Preview(w, p)
	body := w.Body.String()
	for _, want := range []string{"&lt;script&gt;", `name="voucher"`, `<img src="data:image/png;base64,iVBORw0KGgo="`, "<p>a</p><p>b</p>", "Preview:", "disabled"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("title not escaped")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Error("preview cannot be framed by the admin UI")
	}
	w = httptest.NewRecorder()
	Preview(w, config.CaptivePortal{Mode: "password", Logo: "data:text/html;base64,PGI+"})
	if !strings.Contains(w.Body.String(), `type="password"`) || strings.Contains(w.Body.String(), "<img") || !strings.Contains(w.Body.String(), "<h1>Welcome</h1>") {
		t.Errorf("password page: %s", w.Body)
	}
}

func TestParseARP(t *testing.T) {
	arp := `IP address       HW type     Flags       HW address            Mask     Device
192.168.30.100   0x1         0x2         aa:bb:cc:00:00:01     *        eth1.30
192.168.30.101   0x1         0x0         00:00:00:00:00:00     *        eth1.30
`
	if got := parseARP(strings.NewReader(arp), guest1); got.String() != "aa:bb:cc:00:00:01" {
		t.Errorf("got %v", got)
	}
	if got := parseARP(strings.NewReader(arp), netip.MustParseAddr("192.168.30.101")); got != nil {
		t.Errorf("incomplete entry: %v", got)
	}
}

// Every string the page uses is in its catalog.
func TestStringsInCatalog(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	keys := regexp.MustCompile(`"(portalPage\.[a-zA-Z]+)"`).FindAllStringSubmatch(string(src), -1)
	for _, k := range []string{"errNoClient", "errTerms", "errPassword", "errVoucher", "errTooMany"} {
		keys = append(keys, []string{"", "portalPage." + k})
	}
	for _, m := range keys {
		if Strings()[m[1]] == "" {
			t.Errorf("%s is not in strings.json", m[1])
		}
	}
}
