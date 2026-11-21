package portal

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

//go:embed strings.json
var stringsJSON []byte

// catalog holds every string the page shows (English), by key, in the
// web UI catalog's format, ready for more languages.
var catalog = func() map[string]string {
	m := map[string]string{}
	if err := json.Unmarshal(stringsJSON, &m); err != nil {
		panic(err)
	}
	return m
}()

// Strings returns the page's strings by key.
func Strings() map[string]string { return catalog }

func tr(key string, args ...string) string {
	s := catalog[key]
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}

// Server serves the portal pages on config.PortalPort. Only portal
// interfaces reach that port (nft); it has nothing of the admin API.
type Server struct {
	M      *Manager
	Config func() (*config.Config, error)
	// Neighbor returns the MAC of a client on the local network
	// (ArpNeighbor reads /proc/net/arp).
	Neighbor func(ip netip.Addr) net.HardwareAddr

	secret []byte
}

// NewServer makes the handler with a fresh key for its form tokens.
func NewServer(m *Manager, cfg func() (*config.Config, error), neighbor func(netip.Addr) net.HardwareAddr) *Server {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &Server{M: m, Config: cfg, Neighbor: neighbor, secret: key}
}

func addrOf(hostport string) netip.Addr {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// portalOf finds the portal the client reached: the box address it came to.
func (s *Server) portalOf(r *http.Request) (string, config.CaptivePortal, netip.Addr, bool) {
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || s.Config == nil {
		return "", config.CaptivePortal{}, netip.Addr{}, false
	}
	local := addrOf(la.String())
	cfg, err := s.Config()
	if err != nil {
		return "", config.CaptivePortal{}, netip.Addr{}, false
	}
	client := addrOf(r.RemoteAddr)
	addrs := cfg.PortalAddrs()
	for name, p := range cfg.ActivePortals() {
		pa := addrs[name]
		if pa.Prefix.Addr() == local && pa.Prefix.Contains(client) {
			return name, p, local, true
		}
	}
	return "", config.CaptivePortal{}, netip.Addr{}, false
}

func (s *Server) token(client string, hour int64) string {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%s|%d", client, hour)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) tokenOK(client, got string) bool {
	h := time.Now().Unix() / 3600
	for _, hh := range []int64{h, h - 1} {
		if hmac.Equal([]byte(s.token(client, hh)), []byte(got)) {
			return true
		}
	}
	return false
}

func secure(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

// ServeHTTP: the RFC 8908 API, the page, the login form; any other host is
// a redirected request (an OS captive-portal probe or a browser) and goes to
// the page.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secure(w)
	iface, p, box, ok := s.portalOf(r)
	if !ok {
		http.Error(w, "no portal here", http.StatusNotFound)
		return
	}
	client := addrOf(r.RemoteAddr)
	if r.URL.Path == "/captive-portal/api" {
		s.api(w, client, box)
		return
	}
	if want := netip.AddrPortFrom(box, config.PortalPort).String(); r.Host != want {
		http.Redirect(w, r, config.PortalURL(box), http.StatusFound)
		return
	}
	switch {
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		s.login(w, r, iface, p, client)
	case r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.page(w, p, client, "", http.StatusOK)
	default:
		http.Redirect(w, r, config.PortalURL(box), http.StatusFound)
	}
}

func (s *Server) api(w http.ResponseWriter, client, box netip.Addr) {
	out := map[string]any{"captive": true, "user-portal-url": config.PortalURL(box)}
	if sess, ok := s.M.Find(client.String()); ok {
		out["captive"] = false
		out["seconds-remaining"] = max(0, int(time.Until(sess.Expires).Seconds()))
		out["can-extend-session"] = false
	}
	w.Header().Set("Content-Type", "application/captive+json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request, iface string, p config.CaptivePortal, client netip.Addr) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil || !s.tokenOK(client.String(), r.PostForm.Get("token")) {
		s.page(w, p, client, "", http.StatusBadRequest)
		return
	}
	var mac net.HardwareAddr
	if s.Neighbor != nil {
		mac = s.Neighbor(client)
	}
	_, err := s.M.Login(iface, p, client, mac, r.PostForm.Get("accept") == "on", r.PostForm.Get("password"), r.PostForm.Get("voucher"))
	if err != nil {
		key := map[error]string{ErrNoClient: "errNoClient", ErrTerms: "errTerms", ErrPassword: "errPassword", ErrVoucher: "errVoucher", ErrTooMany: "errTooMany"}[err]
		if key == "" {
			s.M.log().Warn("portal: login", "error", err)
			key = "errNoClient"
		}
		code := http.StatusForbidden
		if errors.Is(err, ErrTooMany) {
			code = http.StatusTooManyRequests
		}
		s.page(w, p, client, tr("portalPage."+key), code)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type pageData struct {
	Title, Error, Token, Logo, Online, Left, Note string
	Paragraphs                                    []string
	Mode                                          string
	T                                             map[string]string
	Preview                                       bool
}

func (s *Server) page(w http.ResponseWriter, p config.CaptivePortal, client netip.Addr, msg string, status int) {
	d := pageFor(p)
	d.Error = msg
	d.Token = s.token(client.String(), time.Now().Unix()/3600)
	if sess, ok := s.M.Find(client.String()); ok {
		d.Online = tr("portalPage.online")
		d.Left = tr("portalPage.minutesLeft", "minutes", strconv.Itoa(int(time.Until(sess.Expires).Minutes())))
	}
	render(w, d, status)
}

func pageFor(p config.CaptivePortal) pageData {
	d := pageData{Title: p.Title, Mode: p.Mode, T: catalog}
	if d.Title == "" {
		d.Title = tr("portalPage.defaultTitle")
	}
	for _, para := range strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			d.Paragraphs = append(d.Paragraphs, para)
		}
	}
	if _, _, ok := config.PortalLogo(p.Logo); ok {
		d.Logo = p.Logo
	}
	return d
}

// Preview writes the page as a guest who is not let in yet sees it (the
// admin UI shows it in a frame).
func Preview(w http.ResponseWriter, p config.CaptivePortal) {
	secure(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; frame-ancestors 'self'")
	d := pageFor(p)
	d.Preview = true
	d.Note = tr("portalPage.preview")
	render(w, d, http.StatusOK)
}

func render(w http.ResponseWriter, d pageData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pageTmpl.Execute(w, d); err != nil {
		_, _ = io.WriteString(w, "portal page error")
	}
}

var pageTmpl = template.Must(template.New("portal").Funcs(template.FuncMap{
	"safeURL": func(s string) template.URL { return template.URL(s) },
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;background:#f3f4f6;color:#111827}
main{max-width:28rem;margin:2rem auto;padding:1.5rem;background:#fff;border-radius:.5rem;box-shadow:0 1px 3px rgba(0,0,0,.1)}
img{max-width:100%;max-height:6rem;display:block;margin:0 auto 1rem}
h1{font-size:1.5rem;margin:0 0 1rem}
.text{max-height:16rem;overflow:auto;font-size:.9rem;line-height:1.4}
label{display:block;margin:.75rem 0}
input[type=text],input[type=password]{width:100%;box-sizing:border-box;padding:.6rem;font-size:1rem;border:1px solid #d1d5db;border-radius:.375rem}
button{width:100%;padding:.75rem;font-size:1rem;background:#2563eb;color:#fff;border:0;border-radius:.375rem;margin-top:.5rem}
.err{background:#fee2e2;color:#991b1b;padding:.5rem;border-radius:.375rem}
.ok{background:#dcfce7;color:#166534;padding:.75rem;border-radius:.375rem}
.note{font-size:.8rem;color:#6b7280;text-align:center}
@media (prefers-color-scheme:dark){body{background:#111827;color:#f9fafb}main{background:#1f2937}input[type=text],input[type=password]{background:#111827;color:#f9fafb;border-color:#4b5563}}
</style></head>
<body><main>
{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
{{if .Logo}}<img src="{{safeURL .Logo}}" alt="">{{end}}
<h1>{{.Title}}</h1>
{{if .Online}}<p class="ok" role="status">{{.Online}} {{.Left}}</p>{{else}}
{{if .Paragraphs}}<div class="text">{{range .Paragraphs}}<p>{{.}}</p>{{end}}</div>{{end}}
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/login">
<input type="hidden" name="token" value="{{.Token}}">
{{if eq .Mode "password"}}<label>{{index .T "portalPage.password"}}<input type="password" name="password" autocomplete="off" required></label>{{end}}
{{if eq .Mode "voucher"}}<label>{{index .T "portalPage.voucher"}}<input type="text" name="voucher" autocomplete="off" autocapitalize="characters" required></label>{{end}}
{{if or (eq .Mode "click") .Paragraphs}}<label><input type="checkbox" name="accept" required> {{index .T "portalPage.accept"}}</label>{{end}}
<button type="submit" {{if .Preview}}disabled{{end}}>{{index .T "portalPage.connect"}}</button>
</form>{{end}}
</main></body></html>
`))

// ArpNeighbor reads the client's MAC from the kernel's ARP table.
func ArpNeighbor(ip netip.Addr) net.HardwareAddr {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseARP(f, ip)
}

// parseARP reads /proc/net/arp: "IP address HW type Flags HW address Mask
// Device"; flags 0x0 is an incomplete entry.
func parseARP(r io.Reader, ip netip.Addr) net.HardwareAddr {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != ip.String() || f[2] == "0x0" {
			continue
		}
		if mac, err := net.ParseMAC(f[3]); err == nil && mac.String() != "00:00:00:00:00:00" {
			return mac
		}
	}
	return nil
}
