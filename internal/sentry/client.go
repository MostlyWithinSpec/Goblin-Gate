package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Client talks to goblind on the plugin socket (plain HTTP: goblind knows
// the plugin by its unit). It implements Goblind.
type Client struct {
	HTTP *http.Client
	Base string // default http://goblind
}

// NewClient returns a client for the plugin socket.
func NewClient(socket string) *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// APIError is a non-2xx answer from goblind.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Message) }

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	base := c.Base
	if base == "" {
		base = "http://goblind"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &APIError{Status: resp.StatusCode, Message: e.Error.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Self returns the plugin's approved capabilities and settings.
func (c *Client) Self(ctx context.Context) ([]string, map[string]any, error) {
	var out struct {
		Capabilities []string       `json:"capabilities"`
		Settings     map[string]any `json:"settings"`
	}
	err := c.call(ctx, "GET", "/api/v1/plugin/self", nil, &out)
	return out.Capabilities, out.Settings, err
}

// Config returns the running config (secrets redacted by goblind).
func (c *Client) Config(ctx context.Context) (*config.Config, error) {
	var cfg config.Config
	if err := c.call(ctx, "GET", "/api/v1/config/running", nil, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type logEvent struct {
	ID        int64  `json:"id"`
	Time      int64  `json:"time"`
	Action    string `json:"action"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	DstIP     string `json:"dst_ip"`
	DstPort   int    `json:"dst_port"`
	Proto     string `json:"proto"`
	InIntf    string `json:"in_intf"`
	Category  string `json:"category"`
	Signature string `json:"signature"` // "<sid>: <text>" when Suricata gave a sid
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}

func (ev logEvent) alert() Alert {
	a := Alert{ID: ev.ID, Time: ev.Time, Action: ev.Action, SrcIP: ev.SrcIP, SrcPort: ev.SrcPort, DstIP: ev.DstIP,
		DstPort: ev.DstPort, Proto: ev.Proto, InIntf: ev.InIntf, Category: ev.Category, Signature: ev.Signature}
	if sid, text, ok := strings.Cut(ev.Signature, ": "); ok {
		if _, err := strconv.Atoi(sid); err == nil {
			a.SignatureID, a.Signature = sid, text
		}
	}
	a.Severity, _ = strconv.Atoi(ev.Severity)
	return a
}

// Alerts returns the IPS alerts logged after event id after, at most max.
func (c *Client) Alerts(ctx context.Context, after int64, max int) ([]Alert, error) {
	var out []Alert
	cursor := int64(0)
	for {
		q := url.Values{"kind": {"ips"}, "after": {strconv.FormatInt(after, 10)}, "limit": {"1000"}}
		if cursor > 0 {
			q.Set("cursor", strconv.FormatInt(cursor, 10))
		}
		var page struct {
			Events []logEvent `json:"events"`
			Next   int64      `json:"next_cursor"`
		}
		if err := c.call(ctx, "GET", "/api/v1/logs?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, ev := range page.Events {
			out = append(out, ev.alert())
		}
		if page.Next == 0 || len(out) >= max {
			return out, nil
		}
		cursor = page.Next
	}
}

// Block adds a temporary block.
func (c *Client) Block(ctx context.Context, ip string, minutes int, reason string) error {
	return c.call(ctx, "POST", "/api/v1/blocks", map[string]any{"ip": ip, "minutes": minutes, "reason": reason}, nil)
}

// Blocked returns the blocked addresses.
func (c *Client) Blocked(ctx context.Context) (map[string]bool, error) {
	var out struct {
		Blocks []struct {
			IP string `json:"ip"`
		} `json:"blocks"`
	}
	if err := c.call(ctx, "GET", "/api/v1/blocks", nil, &out); err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, b := range out.Blocks {
		m[b.IP] = true
	}
	return m, nil
}

// Notify sends an alert through the admin's notification rules.
func (c *Client) Notify(ctx context.Context, title, message, key string) error {
	if len(title) > 200 {
		title = title[:200]
	}
	if len(message) > 4000 {
		message = message[:4000]
	}
	return c.call(ctx, "POST", "/api/v1/plugin/notify", map[string]string{"title": title, "message": message, "key": key}, nil)
}

// SettingsFrom reads Sentry's plugin settings (defaults filled by goblind).
func SettingsFrom(m map[string]any) Settings {
	s := Settings{Mode: ModeApprove}
	if v, ok := m["mode"].(string); ok {
		s.Mode = v
	}
	if v, ok := m["dedup_minutes"].(float64); ok && v > 0 {
		s.DedupWindow = time.Duration(v) * time.Minute
	}
	if v, ok := m["block_minutes"].(float64); ok && v > 0 {
		s.BlockMinutes = int(v)
	}
	return s
}

// LatestAlertID returns the id of the newest IPS alert (0 = none).
func (c *Client) LatestAlertID(ctx context.Context) (int64, error) {
	var page struct {
		Events []logEvent `json:"events"`
	}
	if err := c.call(ctx, "GET", "/api/v1/logs?kind=ips&limit=1", nil, &page); err != nil {
		return 0, err
	}
	if len(page.Events) == 0 {
		return 0, nil
	}
	return page.Events[0].ID, nil
}
