package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
	"github.com/networkgoblinlabs/goblin-gate/internal/sites"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// Site-to-site VPN (task 155). Pairing:
//
//  1. gate A: POST /vpn/sites/invitations {site} → {code}: A turns sites on
//     (and picks its tunnel address) in the candidate if needed and keeps the
//     one-time secret; nothing else changes until the reply.
//  2. gate B: POST /vpn/sites/join/check {code} shows what the code brings;
//     POST /vpn/sites/join {code, site} adds A to B's candidate and answers
//     the reply code.
//  3. gate A: POST /vpn/sites/accept {code} adds B to A's candidate.
//
// Both commit. Pairing endpoints need a full admin: an invitation lets
// another gate into the network.

// SitePing pings a site's gate address through its tunnel and returns the
// round trip (tests replace it).
type SitePing func(ctx context.Context, dev string, addr netip.Addr) (time.Duration, error)

var pingTimeRe = regexp.MustCompile(`time[=<]([0-9.]+) ms`)

// PingSite is the box's SitePing: one ICMP echo, 1 s timeout.
func PingSite(ctx context.Context, dev string, addr netip.Addr) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, "ping", "-n", "-q", "-c", "1", "-W", "1", "-I", dev, addr.String()).CombinedOutput()
	if err != nil {
		return 0, errors.New("no answer")
	}
	// -q prints "rtt min/avg/max/mdev = 0.4/0.4/0.4/0.0 ms"
	if i := strings.Index(string(out), " = "); i >= 0 {
		if f, err := strconv.ParseFloat(strings.Split(string(out[i+3:]), "/")[0], 64); err == nil {
			return time.Duration(f * float64(time.Millisecond)), nil
		}
	}
	if m := pingTimeRe.FindSubmatch(out); m != nil {
		if f, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			return time.Duration(f * float64(time.Millisecond)), nil
		}
	}
	return 0, nil
}

func (s *Server) now() time.Time {
	if s.SiteNow != nil {
		return s.SiteNow()
	}
	return time.Now()
}

func (s *Server) sitesReady(w http.ResponseWriter) bool {
	if s.WireGuardKeyDir == "" || s.SiteInvites == nil {
		unavailable(w, "Site-to-site VPN")
		return false
	}
	return true
}

// SiteOffer is what a code brings, as the join check shows it.
type SiteOffer struct {
	Site      string    `json:"site"` // suggested name on this gate
	Endpoint  string    `json:"endpoint,omitempty"`
	Address   string    `json:"address"`
	Networks  []string  `json:"networks"`
	PublicKey string    `json:"public_key"`
	Expires   time.Time `json:"expires,omitzero"`
	// Problems that stop the join (overlapping networks, no endpoint on
	// either gate, the name taken); empty = it can be joined.
	Problems []SiteProblem `json:"problems"`
}

// SiteProblem is one reason a code cannot be used as it is.
type SiteProblem struct {
	Code    string `json:"code"` // overlap | no_endpoint | name_taken | address_taken
	Network string `json:"network,omitempty"`
	Message string `json:"message"`
}

type siteInviteRequest struct {
	Site       string `json:"site"`                  // what this gate calls the other one
	TTLMinutes int    `json:"ttl_minutes,omitempty"` // 5-1440, default 60
}

type siteJoinRequest struct {
	Code string `json:"code"`
	Site string `json:"site,omitempty"` // name for the other gate; default its suggestion
}

type siteInviteView struct {
	ID      string    `json:"id"`
	Site    string    `json:"site"`
	Port    int       `json:"port"`
	Expires time.Time `json:"expires"`
	Created time.Time `json:"created"`
}

// ownSites makes sure the candidate has sites on with a tunnel address;
// it returns whether it changed anything.
func ownSites(cfg *config.Config, avoid ...netip.Addr) (bool, error) {
	changed := false
	if !cfg.VPN.Sites.Enabled {
		cfg.VPN.Sites.Enabled, changed = true, true
	}
	if !cfg.VPN.Sites.Address.IsValid() {
		a, err := pickSiteAddress(cfg, avoid...)
		if err != nil {
			return false, err
		}
		cfg.VPN.Sites.Address, changed = a, true
	}
	return changed, nil
}

// pickSiteAddress picks a random free host in DefaultSitesNetwork.
func pickSiteAddress(cfg *config.Config, avoid ...netip.Addr) (netip.Addr, error) {
	base := config.DefaultSitesNetwork.Addr().As4()
	for range 64 {
		var b [2]byte
		if _, err := rand.Read(b[:]); err != nil {
			return netip.Addr{}, err
		}
		n := binary.BigEndian.Uint16(b[:])
		if n&0xff == 0 || n&0xff == 255 {
			continue
		}
		a := netip.AddrFrom4([4]byte{base[0], base[1], b[0], b[1]})
		if cfg.SiteOverlap(netip.PrefixFrom(a, 32), "") != "" || contains(avoid, a) {
			continue
		}
		return a, nil
	}
	return netip.Addr{}, errors.New("no free tunnel address in " + config.DefaultSitesNetwork.String())
}

func contains(list []netip.Addr, a netip.Addr) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

// endpointFor is this gate's host:port for a tunnel, "" without an endpoint.
func endpointFor(cfg *config.Config, port int) string {
	if cfg.VPN.Sites.Endpoint == "" {
		return ""
	}
	return net.JoinHostPort(cfg.VPN.Sites.Endpoint, strconv.Itoa(port))
}

func (s *Server) offer(cfg *config.Config, site string, port int) (sites.Offer, error) {
	pub, err := wireguard.EnsureKey(s.WireGuardKeyDir, config.SiteKeyName(site))
	if err != nil {
		return sites.Offer{}, err
	}
	name := sites.SiteName(cfg.System.Hostname)
	if name == "" {
		name = "gate"
	}
	return sites.Offer{Name: name, PublicKey: pub.String(), Endpoint: endpointFor(cfg, port),
		Address: cfg.VPN.Sites.Address, Networks: cfg.SiteNetworks()}, nil
}

// nameProblem says why a site name cannot be used on this gate ("" = fine).
func (s *Server) nameProblem(cfg *config.Config, name string) string {
	if !config.ValidSiteName(name) {
		return fmt.Sprintf("invalid site name %q: 1-15 of a-z, 0-9 and -, starting with a letter", name)
	}
	if _, used := render.Devices(cfg)[name]; used {
		return fmt.Sprintf("%q is already the name of an interface or site on this gate", name)
	}
	for _, r := range cfg.VPN.Sites.Remotes {
		if r.Name == name {
			return fmt.Sprintf("there is already a site %q", name)
		}
	}
	if reserved, _ := s.SiteInvites.Reserved(); reserved[name] {
		return fmt.Sprintf("an open invitation already uses the name %q", name)
	}
	return ""
}

// problems lists what stops the offer from being added as site name.
func (s *Server) problems(cfg *config.Config, o sites.Offer, name string, ownEndpoint bool) []SiteProblem {
	out := []SiteProblem{}
	if msg := s.nameProblem(cfg, name); msg != "" {
		out = append(out, SiteProblem{Code: "name_taken", Message: msg})
	}
	if o.Endpoint == "" && !ownEndpoint {
		out = append(out, SiteProblem{Code: "no_endpoint", Message: "neither gate has a public endpoint: set one under VPN → Sites on at least one of them (the one the other can reach from the internet)"})
	}
	if cfg.VPN.Sites.Address == o.Address {
		out = append(out, SiteProblem{Code: "address_taken", Message: fmt.Sprintf("the other gate uses this gate's tunnel address %s: change one of them", o.Address)})
	} else if what := cfg.SiteOverlap(netip.PrefixFrom(o.Address, 32), ""); what != "" {
		out = append(out, SiteProblem{Code: "address_taken", Message: fmt.Sprintf("the other gate's tunnel address %s is inside %s", o.Address, what)})
	}
	for _, n := range o.Networks {
		if what := cfg.SiteOverlap(n, ""); what != "" {
			out = append(out, SiteProblem{Code: "overlap", Network: n.String(), Message: fmt.Sprintf("%s overlaps %s on this gate: renumber one of them first", n, what)})
		}
	}
	return out
}

func offerView(o sites.Offer, site string, problems []SiteProblem) SiteOffer {
	v := SiteOffer{Site: site, Endpoint: o.Endpoint, Address: o.Address.String(), PublicKey: o.PublicKey, Networks: []string{}, Problems: problems}
	for _, n := range o.Networks {
		v.Networks = append(v.Networks, n.String())
	}
	return v
}

// validateSites stores cfg only if the vpn.sites part is valid.
func (s *Server) storeSites(w http.ResponseWriter, cfg *config.Config, what string) bool {
	var errs []config.ValidationError
	for _, e := range config.Validate(cfg) {
		if strings.HasPrefix(e.Path, "vpn.sites") {
			errs = append(errs, e)
		}
	}
	if len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", what+" is not valid", errs)
		return false
	}
	if err := s.Store.SetCandidate(cfg); err != nil {
		s.internal(w, "store candidate", err)
		return false
	}
	return true
}

// POST /api/v1/vpn/sites/invitations {site, ttl_minutes?} → 201 {id, code,
// site, port, expires}.
func (s *Server) siteInvite(w http.ResponseWriter, r *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	var req siteInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Site = strings.TrimSpace(req.Site)
	ttl := time.Duration(req.TTLMinutes) * time.Minute
	if req.TTLMinutes == 0 {
		ttl = time.Hour
	}
	if ttl < 5*time.Minute || ttl > 24*time.Hour {
		writeError(w, http.StatusBadRequest, "bad_request", "ttl_minutes must be 5-1440", nil)
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	if msg := s.nameProblem(cfg, req.Site); msg != "" {
		writeError(w, http.StatusConflict, "name_taken", msg, nil)
		return
	}
	changed, err := ownSites(cfg)
	if err != nil {
		s.internal(w, "pick a tunnel address", err)
		return
	}
	_, reserved := s.SiteInvites.Reserved()
	port := cfg.FreeSitePort(reserved...)
	o, err := s.offer(cfg, req.Site, port)
	if err != nil {
		s.internal(w, "create WireGuard key", err)
		return
	}
	inv, err := sites.NewInvitation(o, s.now(), ttl)
	if err != nil {
		s.internal(w, "make invitation", err)
		return
	}
	if changed && !s.storeSites(w, cfg, "turning sites on") {
		return
	}
	if err := s.SiteInvites.Add(sites.Pending{ID: inv.ID, Site: req.Site, Port: port, Secret: inv.Secret, Expires: inv.Expires}); err != nil {
		if errors.Is(err, sites.ErrTooMany) {
			writeError(w, http.StatusConflict, "too_many", err.Error(), nil)
			return
		}
		s.internal(w, "keep invitation", err)
		return
	}
	s.audit(r, "site_invitation_created", "", map[string]any{"site": req.Site, "id": inv.ID, "expires": inv.Expires})
	writeJSON(w, http.StatusCreated, map[string]any{"id": inv.ID, "code": inv.Code(), "site": req.Site, "port": port,
		"expires": inv.Expires, "sites_enabled": changed})
}

// GET /api/v1/vpn/sites/invitations → {invitations: [...]} (no secrets).
func (s *Server) siteInvitations(w http.ResponseWriter, _ *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	all, err := s.SiteInvites.List()
	if err != nil {
		s.internal(w, "read invitations", err)
		return
	}
	out := []siteInviteView{}
	for _, p := range all {
		out = append(out, siteInviteView{ID: p.ID, Site: p.Site, Port: p.Port, Expires: p.Expires, Created: p.Created})
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

// DELETE /api/v1/vpn/sites/invitations/{id} cancels an invitation.
func (s *Server) siteInvitationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	ok, err := s.SiteInvites.Delete(r.PathValue("id"))
	if err != nil {
		s.internal(w, "cancel invitation", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such open invitation", nil)
		return
	}
	s.audit(r, "site_invitation_cancelled", "", map[string]any{"id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// POST /api/v1/vpn/sites/join/check {code, site?} → SiteOffer: what the
// invitation brings and what would stop it; changes nothing.
func (s *Server) siteJoinCheck(w http.ResponseWriter, r *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	var req siteJoinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	inv, err := sites.ParseInvitation(req.Code, s.now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_code", err.Error(), nil)
		return
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	name := strings.TrimSpace(req.Site)
	if name == "" {
		name = inv.Name
	}
	v := offerView(inv.Offer, name, s.problems(cfg, inv.Offer, name, cfg.VPN.Sites.Endpoint != ""))
	v.Expires = inv.Expires
	writeJSON(w, http.StatusOK, v)
}

// POST /api/v1/vpn/sites/join {code, site?} → 201 {site, reply}: adds the
// inviting gate to the candidate (commit to activate) and answers the reply
// code to paste on it.
func (s *Server) siteJoin(w http.ResponseWriter, r *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	var req siteJoinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	inv, err := sites.ParseInvitation(req.Code, s.now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_code", err.Error(), nil)
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	name := strings.TrimSpace(req.Site)
	if name == "" {
		name = inv.Name
	}
	if _, err := ownSites(cfg, inv.Address); err != nil {
		s.internal(w, "pick a tunnel address", err)
		return
	}
	if p := s.problems(cfg, inv.Offer, name, cfg.VPN.Sites.Endpoint != ""); len(p) > 0 {
		writeError(w, http.StatusConflict, p[0].Code, p[0].Message, p)
		return
	}
	_, reserved := s.SiteInvites.Reserved()
	port := cfg.FreeSitePort(reserved...)
	site := config.Site{Name: name, Enabled: true, PublicKey: inv.PublicKey, Endpoint: inv.Endpoint, ListenPort: port,
		Address: inv.Address, Networks: inv.Networks}
	cfg.VPN.Sites.Remotes = append(cfg.VPN.Sites.Remotes, site)
	o, err := s.offer(cfg, name, port)
	if err != nil {
		s.internal(w, "create WireGuard key", err)
		return
	}
	psk, err := sites.PresharedKey(inv.Secret, inv.ID)
	if err != nil {
		s.internal(w, "derive preshared key", err)
		return
	}
	if err := wireguard.SetPresharedKey(s.WireGuardKeyDir, config.SiteKeyName(name), psk); err != nil {
		s.internal(w, "store preshared key", err)
		return
	}
	if !s.storeSites(w, cfg, "the site") {
		return
	}
	reply := inv.Answer(o)
	s.audit(r, "site_joined", "", map[string]any{"site": name, "invitation": inv.ID, "endpoint": inv.Endpoint})
	writeJSON(w, http.StatusCreated, map[string]any{"site": site, "reply": reply.Code()})
}

// POST /api/v1/vpn/sites/accept {code} → 201 {site}: adds the gate that
// answered an open invitation to the candidate (commit to activate).
func (s *Server) siteAccept(w http.ResponseWriter, r *http.Request) {
	if !s.sitesReady(w) {
		return
	}
	var req siteJoinRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	reply, err := sites.ParseReply(req.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_code", err.Error(), nil)
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	var site config.Site
	var failure error
	found, err := s.SiteInvites.Take(reply.ID, func(p sites.Pending) error {
		if !reply.Verify(p.Secret) {
			return errBadReply
		}
		cfg, err := s.Store.Candidate()
		if err != nil {
			return err
		}
		if pr := s.problemsForAccept(cfg, reply.Offer, p.Site); len(pr) > 0 {
			failure = &problemError{pr}
			return failure
		}
		site = config.Site{Name: p.Site, Enabled: true, PublicKey: reply.PublicKey, Endpoint: reply.Endpoint, ListenPort: p.Port,
			Address: reply.Address, Networks: reply.Networks}
		cfg.VPN.Sites.Remotes = append(cfg.VPN.Sites.Remotes, site)
		var verrs []config.ValidationError
		for _, e := range config.Validate(cfg) {
			if strings.HasPrefix(e.Path, "vpn.sites") {
				verrs = append(verrs, e)
			}
		}
		if len(verrs) > 0 {
			failure = &validationError{verrs}
			return failure
		}
		psk, err := sites.PresharedKey(p.Secret, p.ID)
		if err != nil {
			return err
		}
		if err := wireguard.SetPresharedKey(s.WireGuardKeyDir, config.SiteKeyName(p.Site), psk); err != nil {
			return err
		}
		return s.Store.SetCandidate(cfg)
	})
	var pe *problemError
	var ve *validationError
	switch {
	case !found && err == nil:
		writeError(w, http.StatusNotFound, "no_invitation", "this reply answers no open invitation of this gate (it expired, was cancelled or was used already): make a new invitation", nil)
	case errors.Is(err, errBadReply):
		writeError(w, http.StatusForbidden, "bad_reply", "the reply was not made from this gate's invitation", nil)
	case errors.As(err, &pe):
		writeError(w, http.StatusConflict, pe.p[0].Code, pe.p[0].Message, pe.p)
	case errors.As(err, &ve):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "the site is not valid", ve.errs)
	case err != nil:
		s.internal(w, "accept reply", err)
	default:
		s.audit(r, "site_accepted", "", map[string]any{"site": site.Name, "invitation": reply.ID, "endpoint": site.Endpoint})
		writeJSON(w, http.StatusCreated, map[string]any{"site": site})
	}
}

// problemsForAccept is problems() for a name the invitation reserved.
func (s *Server) problemsForAccept(cfg *config.Config, o sites.Offer, name string) []SiteProblem {
	var out []SiteProblem
	for _, p := range s.problems(cfg, o, "", cfg.VPN.Sites.Endpoint != "") {
		if p.Code != "name_taken" {
			out = append(out, p)
		}
	}
	for _, r := range cfg.VPN.Sites.Remotes {
		if r.Name == name {
			out = append(out, SiteProblem{Code: "name_taken", Message: fmt.Sprintf("there is already a site %q", name)})
		}
	}
	return out
}

var errBadReply = errors.New("bad reply")

type problemError struct{ p []SiteProblem }

func (e *problemError) Error() string { return e.p[0].Message }

type validationError struct{ errs []config.ValidationError }

func (e *validationError) Error() string { return "validation failed" }

// SiteStatus is one site in GET /api/v1/vpn/sites/status.
type SiteStatus struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Via      string `json:"via,omitempty"`
	Address  string `json:"address"`
	Endpoint string `json:"endpoint,omitempty"` // live, else configured
	// LatestHandshake is unix seconds (0 = never); HandshakeAge seconds
	// since then (-1 = never).
	LatestHandshake int64 `json:"latest_handshake"`
	HandshakeAge    int64 `json:"handshake_age"`
	RxBytes         int64 `json:"rx_bytes"`
	TxBytes         int64 `json:"tx_bytes"`
	// Reachable: the remote gate answered a ping through the tunnel; nil
	// when not checked (disabled, not committed, dev mode).
	Reachable *bool   `json:"reachable,omitempty"`
	RTTMillis float64 `json:"rtt_ms,omitempty"`
}

// GET /api/v1/vpn/sites/status → {enabled, address, endpoint, sites}: the
// running config's sites with live tunnel stats and a ping to each gate.
func (s *Server) siteStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	st := cfg.VPN.Sites
	out := []SiteStatus{}
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	for _, site := range st.Remotes {
		v := SiteStatus{Name: site.Name, Enabled: site.Enabled && st.Enabled, Via: site.Via, Address: site.Address.String(), Endpoint: site.Endpoint, HandshakeAge: -1}
		out = append(out, v)
	}
	for i := range out {
		v := &out[i]
		site := st.Remotes[i]
		if !v.Enabled {
			continue
		}
		dev := site.Name
		if site.Via != "" {
			dev = site.Via
		}
		if s.WireGuardStats != nil && site.Via == "" {
			if stats, err := s.WireGuardStats(ctx, dev); err == nil {
				if ps, ok := stats[site.PublicKey]; ok {
					v.LatestHandshake, v.RxBytes, v.TxBytes = ps.LatestHandshake, ps.RxBytes, ps.TxBytes
					if ps.Endpoint != "" {
						v.Endpoint = ps.Endpoint
					}
					if ps.LatestHandshake > 0 {
						v.HandshakeAge = max(0, s.now().Unix()-ps.LatestHandshake)
					}
				}
			}
		}
		if s.SitePing != nil && site.Address.IsValid() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rtt, err := s.SitePing(ctx, dev, site.Address)
				ok := err == nil
				v.Reachable = &ok
				if ok {
					v.RTTMillis = float64(rtt.Microseconds()) / 1000
				}
			}()
		}
	}
	wg.Wait()
	resp := map[string]any{"enabled": st.Enabled, "sites": out}
	if st.Address.IsValid() {
		resp["address"] = st.Address.String()
	}
	if st.Endpoint != "" {
		resp["endpoint"] = st.Endpoint
	}
	writeJSON(w, http.StatusOK, resp)
}
