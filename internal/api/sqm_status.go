package api

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/sqm"
)

// SQMStatus is one entry of GET /api/v1/system/status `sqm`.
type SQMStatus struct {
	Interface    string `json:"interface"`
	Active       bool   `json:"active"` // CAKE is on the device right now
	DownloadMbit int    `json:"download_mbit"`
	UploadMbit   int    `json:"upload_mbit"`
}

// sqmCache keeps tc answers for a few seconds: the status is polled often.
var sqmCache struct {
	sync.Mutex
	at     map[string]time.Time
	active map[string]bool
}

func (s *Server) sqmStatus(cfg *config.Config) []SQMStatus {
	_, shapers := sqm.Plan(cfg)
	out := []SQMStatus{}
	for _, sh := range shapers {
		out = append(out, SQMStatus{Interface: sh.Interface, Active: s.sqmActive(sh.Device), DownloadMbit: sh.DownloadMbit, UploadMbit: sh.UploadMbit})
	}
	return out
}

func (s *Server) sqmActive(dev string) bool {
	if s.SQMActive != nil {
		return s.SQMActive(dev)
	}
	sqmCache.Lock()
	defer sqmCache.Unlock()
	if t, ok := sqmCache.at[dev]; ok && time.Since(t) < 10*time.Second {
		return sqmCache.active[dev]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "tc", "qdisc", "show", "dev", dev, "root").Output()
	on := err == nil && strings.Contains(string(b), "qdisc cake")
	if sqmCache.at == nil {
		sqmCache.at, sqmCache.active = map[string]time.Time{}, map[string]bool{}
	}
	sqmCache.at[dev], sqmCache.active[dev] = time.Now(), on
	return on
}
