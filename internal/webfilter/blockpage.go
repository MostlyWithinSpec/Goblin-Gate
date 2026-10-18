package webfilter

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
)

// BlockPageTemplate is where an installed custom block page lives (task 034
// ships one); the built-in page is used when it is missing or broken.
const BlockPageTemplate = "/usr/share/goblin-gate/blockpage.html"

// PageData is what the block page template gets.
type PageData struct {
	Domain   string
	Category string
	Profile  string
	Warn     bool   // a "continue anyway" form is shown
	Token    string // for the continue form
	Match    string
	Expires  int64
}

// BlockPage serves the page that blocked and warned names resolve to (port
// 80 on the box addresses of filtered subnets). HTTPS sites cannot show it
// (the browser reports a certificate error instead) - that is expected
// without TLS interception.
type BlockPage struct {
	Filter       *Filter
	TemplatePath string // default BlockPageTemplate

	secret []byte
	tmpl   *template.Template
}

// NewBlockPage creates the handler with a fresh signing key (warn tokens do
// not survive a restart, which only means clicking "continue" again).
func NewBlockPage(f *Filter) *BlockPage {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &BlockPage{Filter: f, secret: key}
}

func (b *BlockPage) template() *template.Template {
	if b.tmpl != nil {
		return b.tmpl
	}
	path := b.TemplatePath
	if path == "" {
		path = BlockPageTemplate
	}
	if raw, err := os.ReadFile(path); err == nil {
		if t, err := template.New("page").Parse(string(raw)); err == nil {
			b.tmpl = t
			return t
		}
	}
	b.tmpl = template.Must(template.New("page").Parse(defaultPage))
	return b.tmpl
}

func (b *BlockPage) sign(client netip.Addr, match string, exp int64) string {
	m := hmac.New(sha256.New, b.secret)
	fmt.Fprintf(m, "%s|%s|%d", client.Unmap(), match, exp)
	return hex.EncodeToString(m.Sum(nil))
}

func clientOf(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// ServeHTTP shows the block page for the requested host, and handles the
// warn page's "continue" form at /__goblin/continue.
func (b *BlockPage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client := clientOf(r)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/__goblin/continue" {
		b.cont(w, r, client)
		return
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	d, s, _ := b.Filter.Decide(client, host)
	data := PageData{Domain: host, Category: d.Category, Match: d.Match}
	if id, ok := strings.CutPrefix(d.Category, "app:"); ok {
		data.Category = id
		if a, ok := appctrl.Get(id); ok {
			data.Category = a.Name + " (" + a.Group + ")"
		}
	}
	if s.profile != nil {
		data.Profile = s.profile.Name
	}
	status := http.StatusForbidden
	switch d.Action {
	case ActionWarn:
		data.Warn = true
		data.Expires = time.Now().Add(10 * time.Minute).Unix()
		data.Token = b.sign(client, d.Match, data.Expires)
	case ActionBlock:
	default:
		status = http.StatusOK // not blocked (any more): just say so
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	b.template().Execute(w, data)
}

func (b *BlockPage) cont(w http.ResponseWriter, r *http.Request, client netip.Addr) {
	if r.Method != http.MethodPost {
		http.Error(w, "use the button on the warning page", http.StatusMethodNotAllowed)
		return
	}
	match, domain := r.PostFormValue("match"), r.PostFormValue("domain")
	exp, err := strconv.ParseInt(r.PostFormValue("expires"), 10, 64)
	want := b.sign(client, match, exp)
	if err != nil || time.Now().Unix() > exp || !hmac.Equal([]byte(want), []byte(r.PostFormValue("token"))) {
		http.Error(w, "this warning page has expired; reload it and try again", http.StatusForbidden)
		return
	}
	d, s, _ := b.Filter.Decide(client, domain)
	b.Filter.Grant(client, match)
	// logged as allowed, with the category that warned
	b.Filter.record(client, domain, Decision{Action: ActionMonitor, Category: d.Category, Match: match}, s)
	// only back to the warned name or a name under it (no open redirect)
	if (domain != match && !strings.HasSuffix(domain, "."+match)) || strings.ContainsAny(domain, "/\\@:?#") {
		domain = match
	}
	http.Redirect(w, r, "http://"+domain+"/", http.StatusSeeOther)
}

const defaultPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if .Warn}}Are you sure?{{else}}Blocked{{end}} - Goblin Gate</title>
<style>
body{font-family:system-ui,sans-serif;background:#111827;color:#e5e7eb;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
main{max-width:32rem;padding:2rem;background:#1f2937;border-radius:.75rem;box-shadow:0 10px 30px #0008}
h1{margin-top:0;color:{{if .Warn}}#fbbf24{{else}}#f87171{{end}}}
code{background:#374151;padding:.1rem .3rem;border-radius:.25rem}
button{background:#2563eb;color:#fff;border:0;border-radius:.375rem;padding:.5rem 1rem;font-size:1rem;cursor:pointer}
small{color:#9ca3af}
</style></head><body><main>
{{if .Warn}}<h1>Are you sure?</h1>{{else}}<h1>This site is blocked</h1>{{end}}
<p><code>{{.Domain}}</code>{{if .Category}} is in the category <strong>{{.Category}}</strong>{{end}}.</p>
{{if .Warn}}<form method="post" action="/__goblin/continue">
<input type="hidden" name="domain" value="{{.Domain}}"><input type="hidden" name="match" value="{{.Match}}">
<input type="hidden" name="expires" value="{{.Expires}}"><input type="hidden" name="token" value="{{.Token}}">
<button type="submit">Continue anyway (1 hour)</button></form>
{{else}}<p>If you think this is a mistake, ask the person who runs this network.</p>{{end}}
<p><small>Goblin Gate web filter{{if .Profile}} &middot; profile {{.Profile}}{{end}}</small></p>
</main></body></html>
`
