package ddns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Default IP check URL used when the interface has no public IPv4 address.
const DefaultIPCheckURL = "https://ipv4.icanhazip.com"

// MaxBody limits response body reads to 64 KB.
const MaxBody = 64 << 10

// Updater performs a single DDNS update for one entry against its provider.
type Updater struct {
	Client         *http.Client
	IPCheckURL     string // default https://ipv4.icanhazip.com
	CloudflareBase string
	DuckDNSBase    string
	Now            func() time.Time
	Logger         *slog.Logger
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) logger() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do sends req. A transport error from net/http quotes the full URL, which
// for DuckDNS and custom providers carries the token, and the error ends up
// in the status that read-only users see, so only the host is kept.
func (u *Updater) do(req *http.Request) (*http.Response, error) {
	resp, err := u.client().Do(req)
	var ue *url.Error
	if errors.As(err, &ue) {
		err = fmt.Errorf("%s %s: %w", ue.Op, req.URL.Host, ue.Err)
	}
	return resp, err
}

// Update performs one DDNS update for the given entry using the given IP.
func (u *Updater) Update(ctx context.Context, entry config.DDNS, ip netip.Addr) error {
	switch entry.Provider {
	case "cloudflare":
		return u.updateCloudflare(ctx, entry, ip)
	case "duckdns":
		return u.updateDuckDNS(ctx, entry, ip)
	case "custom":
		return u.updateCustom(ctx, entry, ip)
	default:
		return fmt.Errorf("ddns: unknown provider %q", entry.Provider)
	}
}

func (u *Updater) updateCloudflare(ctx context.Context, entry config.DDNS, ip netip.Addr) error {
	base := u.CloudflareBase
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	ipStr := ip.String()

	// List existing A records for this hostname.
	listURL := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s", base, url.PathEscape(entry.ZoneID), url.QueryEscape(entry.Hostname))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+entry.Token)
	resp, err := u.do(req)
	if err != nil {
		return fmt.Errorf("cloudflare list: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return fmt.Errorf("cloudflare list: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloudflare list: HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var listResp struct {
		Success bool `json:"success"`
		Result  []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return fmt.Errorf("cloudflare list: bad JSON: %w", err)
	}
	if !listResp.Success {
		return fmt.Errorf("cloudflare list: success=false")
	}

	var recordID string
	for _, r := range listResp.Result {
		if r.Name == entry.Hostname {
			recordID = r.ID
			break
		}
	}

	if recordID != "" {
		// Update existing record.
		putURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", base, url.PathEscape(entry.ZoneID), url.PathEscape(recordID))
		payload, _ := json.Marshal(map[string]any{"type": "A", "name": entry.Hostname, "content": ipStr, "ttl": 300, "proxied": false})
		req2, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, strings.NewReader(string(payload)))
		if err != nil {
			return fmt.Errorf("cloudflare: %w", err)
		}
		req2.Header.Set("Authorization", "Bearer "+entry.Token)
		req2.Header.Set("Content-Type", "application/json")
		resp2, err := u.do(req2)
		if err != nil {
			return fmt.Errorf("cloudflare update: %w", err)
		}
		defer resp2.Body.Close()
		body2, _ := io.ReadAll(io.LimitReader(resp2.Body, MaxBody))
		var out struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(body2, &out); err != nil || !out.Success {
			return fmt.Errorf("cloudflare update: HTTP %d: %s", resp2.StatusCode, truncate(string(body2), 200))
		}
		u.logger().Info("ddns: cloudflare record updated", "name", entry.Name, "ip", ipStr)
		return nil
	}

	// Create new A record.
	postURL := fmt.Sprintf("%s/zones/%s/dns_records", base, url.PathEscape(entry.ZoneID))
	payload, _ := json.Marshal(map[string]any{"type": "A", "name": entry.Hostname, "content": ipStr, "ttl": 300, "proxied": false})
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	req2.Header.Set("Authorization", "Bearer "+entry.Token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := u.do(req2)
	if err != nil {
		return fmt.Errorf("cloudflare create: %w", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(io.LimitReader(resp2.Body, MaxBody))
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body2, &out); err != nil || !out.Success {
		return fmt.Errorf("cloudflare create: HTTP %d: %s", resp2.StatusCode, truncate(string(body2), 200))
	}
	u.logger().Info("ddns: cloudflare record created", "name", entry.Name, "ip", ipStr)
	return nil
}

func (u *Updater) updateDuckDNS(ctx context.Context, entry config.DDNS, ip netip.Addr) error {
	base := u.DuckDNSBase
	if base == "" {
		base = "https://www.duckdns.org"
	}
	// First label of the hostname.
	label := entry.Hostname
	if idx := strings.IndexByte(label, '.'); idx >= 0 {
		label = label[:idx]
	}
	updateURL := fmt.Sprintf("%s/update?domains=%s&token=%s&ip=%s", base, url.QueryEscape(label), url.QueryEscape(entry.Token), ip.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateURL, nil)
	if err != nil {
		return fmt.Errorf("duckdns: %w", err)
	}
	resp, err := u.do(req)
	if err != nil {
		return fmt.Errorf("duckdns: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	text := strings.TrimSpace(string(body))
	if text != "OK" {
		return fmt.Errorf("duckdns: unexpected response %q (HTTP %d)", truncate(text, 100), resp.StatusCode)
	}
	u.logger().Info("ddns: duckdns updated", "name", entry.Name, "ip", ip.String())
	return nil
}

func (u *Updater) updateCustom(ctx context.Context, entry config.DDNS, ip netip.Addr) error {
	ipStr := ip.String()
	finalURL := strings.ReplaceAll(entry.URL, "{ip}", url.QueryEscape(ipStr))
	finalURL = strings.ReplaceAll(finalURL, "{hostname}", url.QueryEscape(entry.Hostname))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		return fmt.Errorf("custom: %w", err)
	}
	resp, err := u.do(req)
	if err != nil {
		return fmt.Errorf("custom: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, MaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("custom: HTTP %d", resp.StatusCode)
	}
	u.logger().Info("ddns: custom updated", "name", entry.Name, "ip", ipStr)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
