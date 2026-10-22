package api

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// WANStatus is one WAN interface as the kernel sees it: what a single-WAN box
// shows where multi-WAN boxes show their monitored gateways.
type WANStatus struct {
	Name      string   `json:"name"`
	Device    string   `json:"device"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
	// Gateway is the default route's next hop through this interface (by
	// DHCP or static), "" when it has none.
	Gateway string `json:"gateway,omitempty"`
}

// wanLink reads a device's state; tests replace it.
var wanLink = func(dev string) (up bool, addrs []string) {
	ifc, err := net.InterfaceByName(dev)
	if err != nil {
		return false, nil
	}
	up = ifc.Flags&net.FlagUp != 0 && ifc.Flags&net.FlagRunning != 0
	as, _ := ifc.Addrs()
	for _, a := range as {
		if p, err := netip.ParsePrefix(a.String()); err == nil && !p.Addr().IsLinkLocalUnicast() {
			addrs = append(addrs, p.String())
		}
	}
	return up, addrs
}

// routeTable is /proc/net/route (IPv4 routes); tests replace it.
var routeTable = "/proc/net/route"

// defaultGateways maps a device to its IPv4 default route's next hop.
func defaultGateways() map[string]string {
	f, err := os.Open(routeTable)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[1] != "00000000" || f[2] == "00000000" {
			continue // not a default route, or no next hop
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var ip [4]byte
		binary.LittleEndian.PutUint32(ip[:], binary.BigEndian.Uint32(b))
		if _, seen := out[f[0]]; !seen {
			out[f[0]] = netip.AddrFrom4(ip).String()
		}
	}
	return out
}

// GET /api/v1/wan/status → {wans: [{name, device, up, addresses, gateway?}]}
func (s *Server) wanStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	devs := render.Devices(cfg)
	gws := defaultGateways()
	out := []WANStatus{}
	for _, i := range cfg.Interfaces {
		if i.Role != "wan" || !i.Enabled {
			continue
		}
		dev := devs[i.Name]
		if dev == "" {
			dev = i.Device
		}
		up, addrs := wanLink(dev)
		if addrs == nil {
			addrs = []string{}
		}
		out = append(out, WANStatus{Name: i.Name, Device: dev, Up: up, Addresses: addrs, Gateway: gws[dev]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"wans": out})
}
