package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/portal"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// newPortal makes the captive portal manager. In dev nothing touches nft.
func newPortal(path string, cfg func() (*config.Config, error), dev bool, logger *slog.Logger) *portal.Manager {
	m := &portal.Manager{Path: path, Logger: logger, Config: cfg}
	if dev {
		return m
	}
	m.RunNFT = func(script []byte) error {
		cmd := exec.Command("nft", "-f", "-")
		cmd.Stdin = bytes.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	m.Seen = portalSeen
	return m
}

// portalSeen lists the "mac ip" keys of the traffic-seen set.
func portalSeen() (map[string]bool, error) {
	out, err := exec.Command("nft", "-j", "list", "set", "inet", nft.FilterTable, nft.PortalSeenSet).Output()
	if err != nil {
		return nil, err
	}
	return parsePortalSeen(out)
}

// parsePortalSeen reads `nft -j list set`: elements are
// {"concat": [mac, ip]} or {"elem": {"val": {"concat": [...]}, ...}}.
func parsePortalSeen(b []byte) (map[string]bool, error) {
	var doc struct {
		Nftables []struct {
			Set *struct {
				Elem []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, n := range doc.Nftables {
		if n.Set == nil {
			continue
		}
		for _, raw := range n.Set.Elem {
			var e struct {
				Concat []string `json:"concat"`
				Elem   struct {
					Val struct {
						Concat []string `json:"concat"`
					} `json:"val"`
				} `json:"elem"`
			}
			if json.Unmarshal(raw, &e) != nil {
				continue
			}
			c := e.Concat
			if len(c) == 0 {
				c = e.Elem.Val.Concat
			}
			if len(c) == 2 {
				seen[c[0]+" "+c[1]] = true
			}
		}
	}
	return seen, nil
}

// startPortal serves the portal pages and ends idle sessions.
func startPortal(ctx context.Context, m *portal.Manager, cfg func() (*config.Config, error), dev bool, logger *slog.Logger) {
	neighbor := portal.ArpNeighbor
	if dev {
		neighbor = func(netip.Addr) net.HardwareAddr { return nil }
	}
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(config.PortalPort),
		Handler:           portal.NewServer(m, cfg, neighbor),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("portal: page server stopped", "error", err)
		}
	}()
	go func() { <-ctx.Done(); srv.Close() }()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.Sweep()
			}
		}
	}()
}
