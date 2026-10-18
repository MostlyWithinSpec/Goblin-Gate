package api

import (
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// simulateRequest is the body of POST /api/v1/policy/simulate (task 072).
type simulateRequest struct {
	SrcIP    string `json:"src_ip"`
	DstIP    string `json:"dst_ip"`
	Protocol string `json:"protocol"` // tcp | udp | icmp | any
	DstPort  int    `json:"dst_port"`
	SrcPort  int    `json:"src_port"`
	SrcIntf  string `json:"src_intf"`
	At       string `json:"at"`     // RFC 3339; "" = now
	Config   string `json:"config"` // running (default) | candidate
}

// POST /api/v1/policy/simulate → would this connection pass, and why
// (nft.SimResult). Temporary blocks and threat feed contents come from the
// running box.
func (s *Server) simulatePolicy(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	bad := func(msg string) { writeError(w, http.StatusBadRequest, "invalid_request", msg, nil) }
	src, err1 := netip.ParseAddr(req.SrcIP)
	dst, err2 := netip.ParseAddr(req.DstIP)
	if err1 != nil || err2 != nil {
		bad("src_ip and dst_ip must be IP addresses")
		return
	}
	proto := req.Protocol
	if proto == "any" {
		proto = ""
	}
	if !slices.Contains([]string{"", "tcp", "udp", "icmp"}, proto) {
		bad("protocol must be tcp, udp, icmp or any")
		return
	}
	if req.DstPort < 0 || req.DstPort > 65535 || req.SrcPort < 0 || req.SrcPort > 65535 {
		bad("ports must be 1-65535")
		return
	}
	at := time.Now()
	if req.At != "" {
		t, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			bad("at must be an RFC 3339 time")
			return
		}
		at = t
	}
	cfg, err := s.Store.Running()
	switch req.Config {
	case "", "running":
	case "candidate":
		cfg, err = s.Store.Candidate()
	default:
		bad("config must be running or candidate")
		return
	}
	if err != nil {
		s.internal(w, "read config", err)
		return
	}
	rt := nft.SimRuntime{}
	if s.Blocks != nil {
		rt.Blocked = func(a netip.Addr) bool { _, ok := s.Blocks.Get(a); return ok }
	}
	if s.Feeds != nil {
		rt.InFeed = func(feed string, a netip.Addr) bool { return slices.Contains(s.Feeds.Lookup(a.String()), feed) }
	}
	res, err := nft.Simulate(cfg, nft.SimFlow{SrcIP: src, DstIP: dst, Protocol: proto,
		DstPort: uint16(req.DstPort), SrcPort: uint16(req.SrcPort), SrcIntf: req.SrcIntf, At: at}, rt)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cannot_simulate", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
