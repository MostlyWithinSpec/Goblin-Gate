package sentry

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// Client talks to goblind on the plugin socket through the plugin SDK. It
// implements Goblind.
type Client struct {
	pluginsdk.Client
}

// NewClient returns a client for the plugin socket.
func NewClient(socket string) *Client {
	return &Client{Client: *pluginsdk.NewClient(socket)}
}

// APIError is a non-2xx answer from goblind.
type APIError = pluginsdk.APIError

// Config returns the running config (secrets redacted by goblind).
func (c *Client) Config(ctx context.Context) (*config.Config, error) {
	var cfg config.Config
	if err := c.RunningConfig(ctx, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func alertOf(ev pluginsdk.Event) Alert {
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
	q := pluginsdk.LogQuery{Kind: "ips", After: after, Limit: 1000}
	for {
		page, err := c.Logs(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, ev := range page.Events {
			out = append(out, alertOf(ev))
		}
		if page.NextCursor == 0 || len(out) >= max {
			return out, nil
		}
		q.Cursor = page.NextCursor
	}
}

// Block adds a temporary block.
func (c *Client) Block(ctx context.Context, ip string, minutes int, reason string) error {
	_, err := c.AddBlock(ctx, ip, minutes, reason)
	return err
}

// Blocked returns the blocked addresses.
func (c *Client) Blocked(ctx context.Context) (map[string]bool, error) {
	list, err := c.Blocks(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, b := range list {
		m[b.IP.String()] = true
	}
	return m, nil
}

// Notify sends an alert through the admin's notification rules.
func (c *Client) Notify(ctx context.Context, title, message, key string) error {
	return c.Client.Notify(ctx, pluginsdk.Notification{Title: title, Message: message, Key: key})
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
	page, err := c.Logs(ctx, pluginsdk.LogQuery{Kind: "ips", Limit: 1})
	if err != nil || len(page.Events) == 0 {
		return 0, err
	}
	return page.Events[0].ID, nil
}
