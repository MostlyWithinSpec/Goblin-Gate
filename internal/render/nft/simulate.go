package nft

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Policy simulator (task 072): "would this connection pass, and why?". It
// does not re-implement the firewall: it builds the ruleset of the given
// config, exactly as the commit writes it, and evaluates those rules for the
// first packet of a new connection. A rule it cannot read makes the answer
// fail instead of guessing, so the renderer and the simulator cannot drift
// apart silently.

// SimFlow is the connection to evaluate.
type SimFlow struct {
	SrcIP, DstIP     netip.Addr
	Protocol         string // tcp, udp, icmp; "" = any (rules that need a protocol do not match)
	SrcPort, DstPort uint16
	SrcIntf          string    // config interface name; "" = from the source address
	At               time.Time // for schedules; zero = now
}

// SimRuntime answers what only the running box knows. Nil funcs = nothing
// is blocked / no feed lists the address.
type SimRuntime struct {
	Blocked func(a netip.Addr) bool              // temporary block
	InFeed  func(feed string, a netip.Addr) bool // IP threat feed membership
}

// SimStep is one policy on the way, in order.
type SimStep struct {
	ID      uint32 `json:"id"`
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
	Reason  string `json:"reason"`
}

// SimPolicy names a policy.
type SimPolicy struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
}

// SimBlock is what dropped the connection before or instead of a policy.
type SimBlock struct {
	Kind string `json:"kind"` // temp_block | feed
	Name string `json:"name"`
}

// SimResult is the answer.
type SimResult struct {
	Verdict    string            `json:"verdict"` // accept | deny
	Reason     string            `json:"reason"`
	Chain      string            `json:"chain"` // forward | input
	SrcIntf    string            `json:"src_intf"`
	DstIntf    string            `json:"dst_intf,omitempty"`
	Policy     *SimPolicy        `json:"policy"`
	NAT        bool              `json:"nat"`
	Gateway    string            `json:"gateway,omitempty"`
	Profiles   map[string]string `json:"profiles,omitempty"`
	BlockedBy  *SimBlock         `json:"blocked_by,omitempty"`
	Translated string            `json:"translated,omitempty"`
	Steps      []SimStep         `json:"steps"`
	Notes      []string          `json:"notes,omitempty"`
}

// Simulate evaluates f against cfg.
func Simulate(cfg *config.Config, f SimFlow, rt SimRuntime) (*SimResult, error) {
	if !f.SrcIP.IsValid() || !f.DstIP.IsValid() {
		return nil, fmt.Errorf("source and destination addresses are required")
	}
	f.SrcIP, f.DstIP = f.SrcIP.Unmap(), f.DstIP.Unmap()
	if f.SrcIP.Is4() != f.DstIP.Is4() {
		return nil, fmt.Errorf("source and destination must both be IPv4 or both IPv6")
	}
	switch f.Protocol {
	case "", "tcp", "udp", "icmp":
	default:
		return nil, fmt.Errorf("protocol %q: want tcp, udp, icmp or empty", f.Protocol)
	}
	if f.At.IsZero() {
		f.At = time.Now()
	}
	r, err := prepare(cfg)
	if err != nil {
		return nil, err
	}
	s := newSim(r, rt)
	res := &SimResult{Steps: []SimStep{}}

	src, why, err := s.sourceIntf(f)
	if err != nil {
		return nil, err
	}
	res.SrcIntf = src
	if why != "" {
		res.Notes = append(res.Notes, why)
	}
	pk := &packet{iif: s.devs[src], src: f.SrcIP, dst: f.DstIP, proto: f.Protocol, sport: f.SrcPort, dport: f.DstPort, at: f.At.UTC()}
	if pk.dport == 0 && (pk.proto == "tcp" || pk.proto == "udp") {
		return nil, fmt.Errorf("a %s connection needs a destination port", pk.proto)
	}

	// prerouting: DNS redirect of filtered networks, then port forwards
	fromWAN := s.wanNames[src]
	if fromWAN && !s.internal(f.DstIP) {
		pk.toWANAddr = true // from the internet to the box's public address
	}
	for _, l := range r.dnsRedir {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			pk.toBox = true
			res.Translated = "DNS is redirected to the box (" + v.comment + ")"
			break
		}
	}
	if !pk.toBox && pk.toWANAddr {
		for _, l := range r.pre {
			v, err := s.eval(l, pk)
			if err != nil {
				return nil, err
			}
			if v.matched && v.dnatTo.IsValid() {
				res.Translated = fmt.Sprintf("%s → %s", v.comment, v.dnatTo)
				pk.dst, pk.dnat, pk.toWANAddr = v.dnatTo, true, false
				if v.dnatPort != 0 {
					pk.dport = v.dnatPort
					res.Translated += ":" + strconv.Itoa(int(v.dnatPort))
				}
				break
			}
		}
	}
	if s.boxAddr(pk.dst) || pk.toWANAddr {
		pk.toBox = true
	}

	if pk.toBox {
		res.Chain = "input"
		return s.input(res, pk)
	}
	res.Chain = "forward"
	dst, note := s.egress(pk.dst)
	pk.oif = s.devs[dst]
	res.DstIntf = dst
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	return s.forward(res, pk)
}

// packet is the first packet of the connection as the rules see it.
type packet struct {
	iif, oif         string // kernel devices
	src, dst         netip.Addr
	proto            string
	sport, dport     uint16
	at               time.Time // UTC
	dnat             bool      // translated by a port forward
	toBox, toWANAddr bool
}

type sim struct {
	r        *ruleset
	rt       SimRuntime
	devs     map[string]string // config name -> device
	names    map[string]string // device -> config name
	wanNames map[string]bool
	sets     map[string]setDecl
	setLabel map[string]string // set -> what it is, for reasons
	feedOf   map[string]string // feed set -> feed name
	notes    map[string]bool
}

func newSim(r *ruleset, rt SimRuntime) *sim {
	s := &sim{r: r, rt: rt, devs: r.devs, names: map[string]string{}, wanNames: map[string]bool{},
		sets: map[string]setDecl{}, setLabel: map[string]string{}, feedOf: map[string]string{}, notes: map[string]bool{}}
	for n, d := range r.devs {
		s.names[d] = n
	}
	for _, i := range r.cfg.Interfaces {
		s.wanNames[i.Name] = i.Role == "wan"
	}
	for _, v := range r.cfg.VLANs {
		s.wanNames[v.Name] = s.wanNames[v.Parent]
	}
	for _, d := range r.sets {
		s.sets[d.name] = d
	}
	for name, o := range r.objs.addrs {
		if o.set != "" {
			s.setLabel[o.set] = "address " + quote(name)
		}
		if o.set6 != "" {
			s.setLabel[o.set6] = "address " + quote(name)
		}
	}
	for name, o := range r.objs.svcs {
		if o.set != "" {
			s.setLabel[o.set] = "service " + quote(name)
		}
	}
	for feed, set := range r.feedSet {
		s.feedOf[set] = feed
	}
	return s
}

func (s *sim) note(n string) {
	s.notes[n] = true
}

// ifaceNets lists the enabled non-WAN interfaces and VLANs with an address.
func (s *sim) ifaceNets() []struct {
	name string
	pfx  netip.Prefix
} {
	var out []struct {
		name string
		pfx  netip.Prefix
	}
	add := func(name string, enabled bool, a *netip.Prefix, v6 *config.IPv6) {
		if !enabled || s.wanNames[name] {
			return
		}
		if a != nil {
			out = append(out, struct {
				name string
				pfx  netip.Prefix
			}{name, *a})
		}
		if v6 != nil && v6.Mode == "static" && v6.Address != nil {
			out = append(out, struct {
				name string
				pfx  netip.Prefix
			}{name, *v6.Address})
		}
	}
	for _, i := range s.r.cfg.Interfaces {
		add(i.Name, i.Enabled, i.Address, i.IPv6)
	}
	for _, v := range s.r.cfg.VLANs {
		add(v.Name, v.Enabled, v.Address, v.IPv6)
	}
	for _, w := range s.r.cfg.VPN.WireGuard {
		if w.Enabled && w.Address.IsValid() {
			out = append(out, struct {
				name string
				pfx  netip.Prefix
			}{w.Name, w.Address})
		}
	}
	return out
}

// internal reports whether a is inside one of the box's own networks.
func (s *sim) internal(a netip.Addr) bool {
	for _, n := range s.ifaceNets() {
		if n.pfx.Masked().Contains(a) {
			return true
		}
	}
	return false
}

// boxAddr reports whether a is one of the box's own (static) addresses.
func (s *sim) boxAddr(a netip.Addr) bool {
	for _, n := range s.ifaceNets() {
		if n.pfx.Addr() == a {
			return true
		}
	}
	for _, i := range s.r.cfg.Interfaces {
		if i.Address != nil && i.Address.Addr() == a {
			return true
		}
	}
	return false
}

// sourceIntf picks the interface the connection arrives on.
func (s *sim) sourceIntf(f SimFlow) (string, string, error) {
	if f.SrcIntf != "" {
		if _, ok := s.devs[f.SrcIntf]; !ok {
			return "", "", fmt.Errorf("unknown interface %q", f.SrcIntf)
		}
		return f.SrcIntf, "", nil
	}
	best, bits := "", -1
	for _, n := range s.ifaceNets() {
		if n.pfx.Masked().Contains(f.SrcIP) && n.pfx.Bits() > bits {
			best, bits = n.name, n.pfx.Bits()
		}
	}
	if best != "" {
		return best, "", nil
	}
	if s.r.cfg.VPN.Tailscale.Enabled && netip.MustParsePrefix(tailnetRange).Contains(f.SrcIP) {
		return config.TailscaleInterface, "", nil
	}
	if w := s.firstWAN(); w != "" {
		return w, fmt.Sprintf("%s is in none of the box's networks: treated as coming from the internet on %s", f.SrcIP, w), nil
	}
	return "", "", fmt.Errorf("%s is in none of the box's networks and there is no WAN: give src_intf", f.SrcIP)
}

func (s *sim) firstWAN() string {
	var wans []string
	for n, w := range s.wanNames {
		if w && s.enabled(n) {
			if _, ok := s.devs[n]; ok {
				wans = append(wans, n)
			}
		}
	}
	sort.Strings(wans)
	if len(wans) == 0 {
		return ""
	}
	return wans[0]
}

func (s *sim) enabled(name string) bool {
	for _, i := range s.r.cfg.Interfaces {
		if i.Name == name {
			return i.Enabled
		}
	}
	for _, v := range s.r.cfg.VLANs {
		if v.Name == name {
			return v.Enabled
		}
	}
	return true
}

// egress picks the interface the connection leaves through: the network
// that holds the destination, else the default route.
func (s *sim) egress(dst netip.Addr) (string, string) {
	best, bits := "", -1
	for _, n := range s.ifaceNets() {
		if n.pfx.Masked().Contains(dst) && n.pfx.Bits() > bits {
			best, bits = n.name, n.pfx.Bits()
		}
	}
	if best != "" {
		return best, ""
	}
	if s.r.route.Enabled {
		for _, m := range s.r.route.Default.Members {
			for _, g := range s.r.route.Gateways {
				if g.Name == m.Gateway {
					if n, ok := s.names[g.Device]; ok {
						return n, fmt.Sprintf("leaves through %s, the default route's first gateway %s (assuming it is up)", n, g.Name)
					}
				}
			}
		}
	}
	w := s.firstWAN()
	if w == "" {
		return "", "no WAN: the destination is not reachable"
	}
	return w, ""
}

// input evaluates the input chain (traffic to the box itself).
func (s *sim) input(res *SimResult, pk *packet) (*SimResult, error) {
	res.Policy = nil
	if v := s.blocked(pk); v != nil {
		return s.deny(res, "temporarily blocked: "+v.Name, v), nil
	}
	for _, l := range s.r.spoof {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			return s.deny(res, v.comment, nil), nil
		}
	}
	for _, l := range s.r.feedIn {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			return s.deny(res, "dropped by threat feed "+v.feed+" on the WAN", &SimBlock{Kind: "feed", Name: v.feed}), nil
		}
	}
	for _, l := range s.r.input {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if !v.matched {
			continue
		}
		if v.verdict == "accept" {
			res.Verdict, res.Reason = "accept", "the box accepts it: "+v.comment
		} else {
			return s.deny(res, v.comment, nil), nil
		}
		return s.finish(res), nil
	}
	access := "none"
	if a := s.allowAccess(res.SrcIntf); len(a) > 0 {
		access = strings.Join(a, ", ")
	}
	return s.deny(res, fmt.Sprintf("traffic to the box itself: %s does not allow it (allowed access on %s: %s)", what(pk), res.SrcIntf, access), nil), nil
}

func (s *sim) allowAccess(name string) []string {
	for _, i := range s.r.cfg.Interfaces {
		if i.Name == name {
			return i.AllowAccess
		}
	}
	for _, v := range s.r.cfg.VLANs {
		if v.Name == name {
			return v.AllowAccess
		}
	}
	if name == config.TailscaleInterface {
		return s.r.cfg.VPN.Tailscale.AllowAccess
	}
	for _, w := range s.r.cfg.VPN.WireGuard {
		if w.Name == name {
			return w.AllowAccess
		}
	}
	return nil
}

func what(pk *packet) string {
	switch pk.proto {
	case "tcp", "udp":
		return fmt.Sprintf("%s port %d", pk.proto, pk.dport)
	case "icmp":
		return "ping"
	}
	return "this traffic"
}

func (s *sim) blocked(pk *packet) *SimBlock {
	if s.rt.Blocked == nil {
		return nil
	}
	for _, a := range []netip.Addr{pk.src, pk.dst} {
		if s.rt.Blocked(a) {
			return &SimBlock{Kind: "temp_block", Name: a.String()}
		}
	}
	return nil
}

// forward evaluates the forward chain in the order write lays it out.
func (s *sim) forward(res *SimResult, pk *packet) (*SimResult, error) {
	if v := s.blocked(pk); v != nil {
		return s.deny(res, "temporarily blocked: "+v.Name, v), nil
	}
	for _, l := range s.r.spoof {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			return s.deny(res, v.comment, nil), nil
		}
	}
	for _, l := range s.r.feedFwd {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			return s.deny(res, "dropped by threat feed "+v.feed+" on the WAN", &SimBlock{Kind: "feed", Name: v.feed}), nil
		}
	}
	if s.r.cfg.UPnP.Enabled && s.wanNames[res.SrcIntf] {
		s.note("UPnP/NAT-PMP mappings exist only at run time and are not simulated")
	}
	for _, l := range s.r.dnsFwd {
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if v.matched {
			return s.deny(res, "rejected: "+v.comment, nil), nil
		}
	}

	type progress struct {
		matched bool
		reason  string
		depth   int
	}
	byPolicy := map[uint32]*progress{}
	var order []uint32
	pols := map[uint32]config.Policy{}
	for _, p := range s.r.cfg.Policies {
		pols[p.ID] = p
	}
	step := func(id uint32) *progress {
		if p, ok := byPolicy[id]; ok {
			return p
		}
		p := &progress{depth: -1}
		byPolicy[id] = p
		order = append(order, id)
		return p
	}
	finishSteps := func() {
		for _, id := range order {
			p := byPolicy[id]
			res.Steps = append(res.Steps, SimStep{ID: id, Name: pols[id].Name, Matched: p.matched, Reason: p.reason})
		}
	}

	for i, l := range s.r.forward {
		id, owned := s.r.owner[i]
		if strings.HasPrefix(l, "#") {
			if owned {
				step(id).reason = "disabled"
				if strings.Contains(l, "share no address family") {
					step(id).reason = "its source and destination addresses share no address family"
				}
			}
			continue
		}
		v, err := s.eval(l, pk)
		if err != nil {
			return nil, err
		}
		if !owned {
			if v.matched {
				// port forward accept, VPN rules
				finishSteps()
				if v.verdict == "accept" {
					res.Verdict, res.Reason = "accept", "accepted by "+v.comment
					return s.finish(res), nil
				}
				return s.deny(res, v.comment, nil), nil
			}
			continue
		}
		p := step(id)
		if p.matched {
			continue
		}
		if !v.matched {
			if v.depth > p.depth {
				p.depth, p.reason = v.depth, v.why
			}
			continue
		}
		pol := pols[id]
		p.matched, p.reason = true, "matches"
		res.Policy = &SimPolicy{ID: id, Name: pol.Name}
		finishSteps()
		if v.feed != "" {
			return s.deny(res, fmt.Sprintf("policy %d %q would accept it, but threat feed %s lists %s", id, pol.Name, v.feed, v.feedAddr),
				&SimBlock{Kind: "feed", Name: v.feed}), nil
		}
		if v.verdict != "accept" {
			return s.deny(res, fmt.Sprintf("denied by policy %d %q", id, pol.Name), nil), nil
		}
		res.Verdict, res.Reason = "accept", fmt.Sprintf("accepted by policy %d %q", id, pol.Name)
		res.NAT = pol.NAT && s.wanNames[res.DstIntf]
		res.Gateway = pol.Gateway
		prof := map[string]string{}
		for k, val := range map[string]string{"ips": pol.IPSProfile, "webfilter": pol.WebFilterProfile, "dnsfilter": pol.DNSFilterProfile, "appctrl": pol.AppCtrlProfile} {
			if val != "" {
				prof[k] = val
			}
		}
		if len(prof) > 0 {
			res.Profiles = prof
		}
		return s.finish(res), nil
	}
	finishSteps()
	return s.deny(res, "no policy matched: denied by default", nil), nil
}

func (s *sim) deny(res *SimResult, reason string, b *SimBlock) *SimResult {
	res.Verdict, res.Reason, res.BlockedBy = "deny", reason, b
	return s.finish(res)
}

func (s *sim) finish(res *SimResult) *SimResult {
	for n := range s.notes {
		res.Notes = append(res.Notes, n)
	}
	sort.Strings(res.Notes)
	return res
}

// verdict of one rule for the packet.
type ruleResult struct {
	matched  bool
	verdict  string // accept | drop | reject | "" (no verdict: a jump returned, a nat rule)
	comment  string
	why      string // first condition that failed
	depth    int    // how many conditions held before it
	feed     string // the threat feed set that matched
	feedAddr netip.Addr
	dnatTo   netip.Addr
	dnatPort uint16
}

// eval evaluates one rendered rule. It understands exactly the expressions
// the renderer writes; anything else is an error.
func (s *sim) eval(rule string, pk *packet) (ruleResult, error) {
	t, err := tokens(rule)
	if err != nil {
		return ruleResult{}, fmt.Errorf("simulate: %w in rule %q", err, rule)
	}
	var res ruleResult
	if i := slices.Index(t, "comment"); i >= 0 && i+1 < len(t) {
		res.comment = unquote(t[i+1])
	}
	fail := func(why string) (ruleResult, error) {
		res.why = why
		return res, nil
	}
	bad := func() (ruleResult, error) {
		return ruleResult{}, fmt.Errorf("simulate: cannot read rule %q", rule)
	}
	i := 0
	next := func() string {
		if i < len(t) {
			i++
			return t[i-1]
		}
		return ""
	}
	peek := func() string {
		if i < len(t) {
			return t[i]
		}
		return ""
	}
	// value reads a single value or a { a, b } set.
	value := func() []string {
		v := next()
		if v != "{" {
			return []string{unquote(v)}
		}
		var out []string
		for i < len(t) {
			x := next()
			if x == "}" {
				break
			}
			if x != "," {
				out = append(out, unquote(x))
			}
		}
		return out
	}
	for i < len(t) {
		switch tok := next(); tok {
		case "iifname", "oifname":
			neg := false
			if peek() == "!=" {
				next()
				neg = true
			}
			want := value()
			have := pk.iif
			if tok == "oifname" {
				have = pk.oif
			}
			if slices.Contains(want, have) == neg {
				dir := "source"
				if tok == "oifname" {
					dir = "destination"
				}
				return fail(fmt.Sprintf("%s interface %s %s %s", dir, s.ifname(have), map[bool]string{false: "is not", true: "is"}[neg], s.ifnames(want)))
			}
		case "ip", "ip6":
			dir := next()
			if dir != "saddr" && dir != "daddr" {
				return bad()
			}
			a := pk.src
			if dir == "daddr" {
				a = pk.dst
			}
			if (tok == "ip") != a.Is4() {
				return fail(fmt.Sprintf("the rule is for IPv%s", map[bool]string{true: "4", false: "6"}[tok == "ip"]))
			}
			neg := false
			if peek() == "!=" {
				next()
				neg = true
			}
			ref := next()
			in, label, err := s.addrIn(ref, a)
			if err != nil {
				return bad()
			}
			if feed, ok := s.feedOf[strings.TrimPrefix(ref, "@")]; ok && in {
				res.feed, res.feedAddr = feed, a
			}
			if in == neg {
				side := map[string]string{"saddr": "source", "daddr": "destination"}[dir]
				return fail(fmt.Sprintf("%s %s %s %s", side, a, map[bool]string{false: "is not in", true: "is in"}[neg], label))
			}
		case "meta":
			switch k := next(); k {
			case "l4proto":
				if peek() == "." { // meta l4proto . th dport @set
					next()
					if next() != "th" || next() != "dport" {
						return bad()
					}
					set := strings.TrimPrefix(next(), "@")
					ok, err := s.svcIn(set, pk)
					if err != nil {
						return bad()
					}
					if !ok {
						return fail(fmt.Sprintf("%s is not in %s", what(pk), s.label(set)))
					}
					break
				}
				want := value()
				if !protoIn(pk, want) {
					return fail(fmt.Sprintf("protocol %s is not %s", protoName(pk), strings.Join(want, " or ")))
				}
			case "nfproto":
				fam := next()
				if (fam == "ipv4") != pk.src.Is4() {
					return fail("the rule is for " + fam)
				}
			case "day":
				days := value()
				if !slices.Contains(days, pk.at.Weekday().String()) {
					return fail("outside the schedule (day)")
				}
			case "hour":
				lo, hi, ok := hourRange(next())
				if !ok {
					return bad()
				}
				sec := pk.at.Hour()*3600 + pk.at.Minute()*60 + pk.at.Second()
				if sec < lo || sec > hi {
					return fail("outside the schedule (time of day)")
				}
			case "mark":
				// meta mark set meta mark or 0x... (an action)
				if next() != "set" {
					return bad()
				}
				for _, w := range []string{"meta", "mark"} {
					if next() != w {
						return bad()
					}
				}
				next() // or / |
				next() // value
			default:
				return bad()
			}
		case "ct":
			switch k := next(); k {
			case "state":
				next()
				return fail("only for established connections") // a new connection
			case "status":
				if next() != "dnat" {
					return bad()
				}
				if !pk.dnat {
					return fail("only for port-forwarded connections")
				}
			case "mark":
				if peek() == "set" { // ct mark set ct mark and X or Y
					for n := 0; n < 7; n++ {
						next()
					}
					break
				}
				return fail("only for connections already handed to the IPS")
			default:
				return bad()
			}
		case "tcp", "udp", "th":
			field := next()
			if field != "dport" && field != "sport" {
				return bad()
			}
			want := tok
			if tok == "th" {
				want = "tcp or udp"
			}
			if tok == "th" && pk.proto != "tcp" && pk.proto != "udp" || tok != "th" && pk.proto != tok {
				return fail(fmt.Sprintf("protocol %s is not %s", protoName(pk), want))
			}
			port := pk.dport
			if field == "sport" {
				port = pk.sport
			}
			ok := false
			for _, v := range value() {
				sp, err := parsePorts(v)
				if err != nil {
					return bad()
				}
				for _, x := range sp {
					if uint32(port) >= x.lo && uint32(port) <= x.hi {
						ok = true
					}
				}
			}
			if !ok {
				return fail(fmt.Sprintf("%s port %d does not match", field[:1]+"port", port))
			}
		case "icmp", "icmpv6":
			if next() != "type" {
				return bad()
			}
			types := value()
			if pk.proto != "icmp" || (tok == "icmp") != pk.src.Is4() || !slices.Contains(types, "echo-request") {
				return fail("not a ping")
			}
		case "counter":
			if peek() == "name" {
				next()
				next()
			}
		case "limit": // limit rate 10/second burst 20 packets
			for n := 0; n < 5; n++ {
				next()
			}
		case "log":
			for peek() == "group" || peek() == "prefix" {
				next()
				next()
			}
		case "accept", "drop":
			res.matched, res.verdict = true, tok
			return res, nil
		case "reject":
			if peek() == "with" {
				next()
				next()
				next()
			}
			res.matched, res.verdict = true, "reject"
			return res, nil
		case "queue": // queue flags bypass,fanout to 16-19: accepted, inspected by the IPS
			res.matched, res.verdict = true, "accept"
			return res, nil
		case "redirect":
			res.matched = true
			return res, nil
		case "dnat":
			if next() != "to" {
				return bad()
			}
			target := next()
			host, port, _ := strings.Cut(target, ":")
			a, err := netip.ParseAddr(host)
			if err != nil {
				return bad()
			}
			res.matched, res.dnatTo = true, a
			if port != "" {
				n, err := strconv.Atoi(port)
				if err != nil {
					return bad()
				}
				res.dnatPort = uint16(n)
			}
			return res, nil
		case "jump":
			return bad() // the chains the simulator walks have no jumps besides the fixed ones
		case "comment":
			next()
		default:
			return bad()
		}
		res.depth++
	}
	return bad()
}

// addrIn checks a against @set or a literal address/prefix.
func (s *sim) addrIn(ref string, a netip.Addr) (bool, string, error) {
	if set, ok := strings.CutPrefix(ref, "@"); ok {
		if feed, ok := s.feedOf[set]; ok {
			return s.rt.InFeed != nil && s.rt.InFeed(feed, a), "threat feed " + feed, nil
		}
		d, ok := s.sets[set]
		if !ok {
			return false, "", fmt.Errorf("unknown set %s", set)
		}
		if strings.Contains(d.comment, "fqdn") {
			s.note("FQDN addresses are resolved at run time and treated as not matching here")
		}
		for _, e := range d.elements {
			if elemContains(e, a) {
				return true, s.label(set), nil
			}
		}
		return false, s.label(set), nil
	}
	return elemContains(ref, a), ref, nil
}

func (s *sim) label(set string) string {
	if l, ok := s.setLabel[set]; ok {
		return l
	}
	if d, ok := s.sets[set]; ok && strings.HasPrefix(d.comment, "union of ") {
		return "any of " + strings.TrimPrefix(d.comment, "union of ")
	}
	return set
}

// elemContains reports whether a set element (address, prefix or range)
// holds a.
func elemContains(e string, a netip.Addr) bool {
	if lo, hi, ok := strings.Cut(e, "-"); ok {
		l, err1 := netip.ParseAddr(lo)
		h, err2 := netip.ParseAddr(hi)
		return err1 == nil && err2 == nil && l.Is4() == a.Is4() && l.Compare(a) <= 0 && a.Compare(h) <= 0
	}
	if p, err := netip.ParsePrefix(e); err == nil {
		return p.Contains(a)
	}
	x, err := netip.ParseAddr(e)
	return err == nil && x == a
}

// svcIn checks the packet's protocol and port against a service set.
func (s *sim) svcIn(set string, pk *packet) (bool, error) {
	d, ok := s.sets[set]
	if !ok {
		return false, fmt.Errorf("unknown set %s", set)
	}
	for _, e := range d.elements {
		proto, ports, ok := strings.Cut(e, " . ")
		if !ok {
			return false, fmt.Errorf("bad element %q", e)
		}
		if proto != pk.proto {
			continue
		}
		sp, err := parsePorts(ports)
		if err != nil {
			return false, err
		}
		for _, x := range sp {
			if uint32(pk.dport) >= x.lo && uint32(pk.dport) <= x.hi {
				return true, nil
			}
		}
	}
	return false, nil
}

func protoIn(pk *packet, want []string) bool {
	for _, w := range want {
		switch {
		case w == pk.proto && pk.proto != "":
			return true
		case w == "icmp" && pk.proto == "icmp" && pk.src.Is4(), w == "ipv6-icmp" && pk.proto == "icmp" && !pk.src.Is4():
			return true
		}
	}
	return false
}

func protoName(pk *packet) string {
	if pk.proto == "" {
		return "any"
	}
	return pk.proto
}

// hourRange parses `"22:30"-"23:59:59"` into seconds after midnight.
func hourRange(s string) (int, int, bool) {
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false
	}
	a, ok1 := clockSeconds(unquote(lo))
	b, ok2 := clockSeconds(unquote(hi))
	return a, b, ok1 && ok2
}

func clockSeconds(s string) (int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	total := 0
	for i, mul := range []int{3600, 60, 1} {
		if i >= len(parts) {
			break
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, false
		}
		total += n * mul
	}
	return total, true
}

func (s *sim) ifname(dev string) string {
	if dev == "" {
		return "(none: traffic to the box)"
	}
	if n, ok := s.names[dev]; ok {
		return n
	}
	return dev
}

func (s *sim) ifnames(devs []string) string {
	out := make([]string, len(devs))
	for i, d := range devs {
		out[i] = s.ifname(d)
	}
	return strings.Join(out, " or ")
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// tokens splits a rule into words, quoted strings (kept with their quotes,
// and joined with a following -"..." for hour ranges), braces and commas.
func tokens(rule string) ([]string, error) {
	var out []string
	for i := 0; i < len(rule); {
		c := rule[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '{' || c == '}' || c == ',':
			out = append(out, string(c))
			i++
		case c == '"':
			j := strings.IndexByte(rule[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("unterminated string")
			}
			tok := rule[i : i+j+2]
			i += j + 2
			if strings.HasPrefix(rule[i:], `-"`) { // "a"-"b"
				k := strings.IndexByte(rule[i+2:], '"')
				if k < 0 {
					return nil, fmt.Errorf("unterminated string")
				}
				tok += rule[i : i+k+3]
				i += k + 3
			}
			out = append(out, tok)
		default:
			j := i
			for j < len(rule) && !strings.ContainsRune(" \t{},\"", rune(rule[j])) {
				j++
			}
			out = append(out, rule[i:j])
			i = j
		}
	}
	return out, nil
}
