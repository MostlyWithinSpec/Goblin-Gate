package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
)

type dhcpLease struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Hostname string `json:"hostname,omitempty"`
	Expires  string `json:"expires,omitempty"` // RFC 3339
}

// GET /api/v1/dhcp/leases → {leases: [{ip, mac, hostname?, expires?}]}: the
// active leases in Kea's lease file, sorted by address.
func (s *Server) dhcpLeases(w http.ResponseWriter, r *http.Request) {
	path := s.DHCPLeaseFile
	if path == "" {
		path = kea.LeaseFile
	}
	out := []dhcpLease{}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"leases": out})
		return
	}
	if err != nil {
		s.internal(w, "read dhcp leases", err)
		return
	}
	recs, err := dhcpdns.ParseLeaseRecords(f, time.Now())
	f.Close()
	if err != nil {
		s.internal(w, "parse dhcp leases", err)
		return
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].IP.Less(recs[j].IP) })
	for _, rec := range recs {
		l := dhcpLease{IP: rec.IP.String(), MAC: colonMAC(rec.MAC), Hostname: rec.Hostname}
		if rec.Expires > 0 {
			l.Expires = time.Unix(rec.Expires, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, l)
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": out})
}

// colonMAC turns "aabbccddeeff" into "aa:bb:cc:dd:ee:ff".
func colonMAC(m string) string {
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
