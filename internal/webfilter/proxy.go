package webfilter

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// ProxyPort is where nft redirects the DNS of filtered subnets.
const ProxyPort = 5300

// WarnGrant is how long "continue anyway" on a warn page lasts.
const WarnGrant = time.Hour

// subnet is one filtered client subnet.
type subnet struct {
	prefix  netip.Prefix // client subnet
	box     netip.Addr   // the box's address there: block page and upstream source
	profile *Profile     // web filter (may be nil)
	apps    *AppProfile  // application control (may be nil)
}

// Filter decides and answers DNS for the filtered subnets. Set the
// exported fields, call Resync with the live config and Serve.
type Filter struct {
	Logger *slog.Logger
	// Event records blocked, warned and monitored lookups (nil = none).
	Event func(logs.Event)
	// UpstreamPort is Unbound's port on the box addresses (default 53).
	UpstreamPort int
	Now          func() time.Time

	db      atomic.Pointer[DB]
	subnets atomic.Pointer[[]subnet]

	mu     sync.Mutex
	grants map[grantKey]time.Time
}

type grantKey struct {
	client netip.Addr
	match  string
}

func (f *Filter) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Filter) logger() *slog.Logger {
	if f.Logger != nil {
		return f.Logger
	}
	return slog.Default()
}

// SetDB swaps in a new category database.
func (f *Filter) SetDB(db *DB) { f.db.Store(db) }

// DB returns the current category database (may be nil).
func (f *Filter) DB() *DB { return f.db.Load() }

// Resync applies the live config's web filter profiles and assignments.
func (f *Filter) Resync(live *config.Config) {
	profiles := map[string]*Profile{}
	for _, p := range live.Profiles.WebFilter {
		profiles[p.Name] = Compile(p)
	}
	apps := map[string]*AppProfile{}
	for _, p := range live.Profiles.AppCtrl {
		apps[p.Name] = CompileApps(p)
	}
	byIface := map[string]*subnet{}
	var order []string
	get := func(a render.ProfileAssignment) *subnet {
		s, ok := byIface[a.Interface]
		if !ok {
			s = &subnet{prefix: a.Address.Masked(), box: a.Address.Addr()}
			byIface[a.Interface] = s
			order = append(order, a.Interface)
		}
		return s
	}
	for _, a := range render.WebFilterAssignments(live) {
		get(a).profile = profiles[a.Profile]
	}
	for _, a := range render.AppCtrlAssignments(live) {
		get(a).apps = apps[a.Profile]
	}
	subs := make([]subnet, 0, len(order))
	for _, name := range order {
		subs = append(subs, *byIface[name])
	}
	f.subnets.Store(&subs)
}

// lookup finds the subnet of a client.
func (f *Filter) lookup(client netip.Addr) (subnet, bool) {
	subs := f.subnets.Load()
	if subs == nil {
		return subnet{}, false
	}
	client = client.Unmap()
	for _, s := range *subs {
		if s.prefix.Contains(client) {
			return s, true
		}
	}
	return subnet{}, false
}

// Decide is the profile's decision for a client and name, with warn grants
// applied.
func (f *Filter) Decide(client netip.Addr, name string) (Decision, subnet, bool) {
	s, ok := f.lookup(client)
	if !ok {
		return Decision{Action: ActionAllow}, s, false
	}
	// application control first: a blocked app is blocked whatever the
	// web filter says; a monitored one is logged when nothing else is
	var app Decision
	if s.apps != nil {
		app = s.apps.Decide(name)
		if app.Action == ActionBlock {
			return app, s, true
		}
	}
	d := Decision{Action: ActionAllow}
	if s.profile != nil {
		d = s.profile.Decide(f.DB(), name)
		if d.Action == ActionWarn && f.granted(client.Unmap(), d.Match) {
			d.Action = ActionMonitor
		}
	}
	if d.Action == ActionAllow && app.Action == ActionMonitor {
		return app, s, true
	}
	return d, s, true
}

// Grant lets a client past a warn page for match (and its subdomains).
func (f *Filter) Grant(client netip.Addr, match string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grants == nil {
		f.grants = map[grantKey]time.Time{}
	}
	now := f.now()
	for k, exp := range f.grants {
		if now.After(exp) {
			delete(f.grants, k)
		}
	}
	f.grants[grantKey{client.Unmap(), match}] = now.Add(WarnGrant)
}

func (f *Filter) granted(client netip.Addr, match string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	exp, ok := f.grants[grantKey{client, match}]
	return ok && f.now().Before(exp)
}

// Answer handles one query: a blocked or warned name is answered here with
// the block page address; anything else goes to Unbound on the box address
// of the client's subnet (so the DNS filter's per-subnet tags still apply).
func (f *Filter) Answer(ctx context.Context, client netip.Addr, query []byte, tcp bool) ([]byte, error) {
	q, err := parseQuestion(query)
	if err != nil {
		return nil, err
	}
	d, s, ok := f.Decide(client, q.name)
	if !ok {
		return nil, errors.New("webfilter: client is not in a filtered subnet")
	}
	if render.IsDoHCanary(q.name) {
		// tells browsers this network filters DNS: no DNS-over-HTTPS
		return nxdomainAnswer(query, q), nil
	}
	if d.Action != ActionAllow {
		f.record(client, q.name, d, s)
	}
	if d.Action == ActionBlock || d.Action == ActionWarn {
		return blockedAnswer(query, q, s.box), nil
	}
	return f.forward(ctx, s.box, query, tcp)
}

func (f *Filter) record(client netip.Addr, name string, d Decision, s subnet) {
	if f.Event == nil {
		return
	}
	action := map[string]string{ActionBlock: "deny", ActionWarn: "warn", ActionMonitor: "allow"}[d.Action]
	ev := logs.Event{Time: f.now().Unix(), Kind: "webfilter", Action: action, SrcIP: client.Unmap().String(), Category: d.Category, Message: name}
	if s.profile != nil {
		ev.App = s.profile.Name
	}
	if id, ok := strings.CutPrefix(d.Category, "app:"); ok {
		ev.Kind, ev.App, ev.Category = "appctrl", id, ""
		if a, ok := appctrl.Get(id); ok {
			ev.Category = a.Group
		}
	}
	f.Event(ev)
}

func (f *Filter) upstream(box netip.Addr) string {
	port := f.UpstreamPort
	if port == 0 {
		port = 53
	}
	return netip.AddrPortFrom(box, uint16(port)).String()
}

// forward sends the query to Unbound from the box address.
func (f *Filter) forward(ctx context.Context, box netip.Addr, query []byte, tcp bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	network := "udp"
	if tcp {
		network = "tcp"
	}
	d := net.Dialer{LocalAddr: localAddr(network, box)}
	conn, err := d.DialContext(ctx, network, f.upstream(box))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if !tcp {
		if _, err := conn.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	if err := writeTCP(conn, query); err != nil {
		return nil, err
	}
	return readTCP(conn)
}

func localAddr(network string, box netip.Addr) net.Addr {
	if network == "tcp" {
		return &net.TCPAddr{IP: box.AsSlice()}
	}
	return &net.UDPAddr{IP: box.AsSlice()}
}

func writeTCP(w io.Writer, msg []byte) error {
	b := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(b, uint16(len(msg)))
	copy(b[2:], msg)
	_, err := w.Write(b)
	return err
}

func readTCP(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(r, msg)
	return msg, err
}

// ServeUDP answers queries on pc until ctx ends.
func (f *Filter) ServeUDP(ctx context.Context, pc net.PacketConn) error {
	go func() { <-ctx.Done(); pc.Close() }()
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		ua, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		query := append([]byte(nil), buf[:n]...)
		client, _ := netip.AddrFromSlice(ua.IP)
		go func() {
			resp, err := f.Answer(ctx, client, query, false)
			if err != nil {
				f.logger().Debug("webfilter: query failed", "client", client, "error", err)
				return
			}
			pc.WriteTo(resp, addr)
		}()
	}
}

// ServeTCP answers queries on ln until ctx ends.
func (f *Filter) ServeTCP(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			defer conn.Close()
			ta, ok := conn.RemoteAddr().(*net.TCPAddr)
			if !ok {
				return
			}
			client, _ := netip.AddrFromSlice(ta.IP)
			for {
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				query, err := readTCP(conn)
				if err != nil {
					return
				}
				resp, err := f.Answer(ctx, client, query, true)
				if err != nil {
					return
				}
				if err := writeTCP(conn, resp); err != nil {
					return
				}
			}
		}()
	}
}
