package config

import (
	"fmt"
	"net/mail"
	"strings"
)

// Notifications says where alerts go (task 053): channels, and per event the
// channels that receive it. Secrets (passwords, tokens) are stored in the
// config like feed auth headers; they are never logged.
type Notifications struct {
	Channels []NotifyChannel `json:"channels,omitempty"`
	Rules    []NotifyRule    `json:"rules,omitempty"`
}

// NotifyChannel is one destination. Which fields apply depends on Type:
//   - email: smtp_host, smtp_port (default 587; 465 = TLS), username, password, from, to
//   - webhook: url (JSON POST), token (optional, sent as a Bearer token)
//   - discord: url (the channel's webhook URL)
//   - ntfy: url (server and topic, e.g. https://ntfy.sh/my-gate), token (optional)
//   - telegram: bot_token, chat_id
type NotifyChannel struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Enabled  bool     `json:"enabled"`
	URL      string   `json:"url,omitempty"`
	Token    string   `json:"token,omitempty"`
	BotToken string   `json:"bot_token,omitempty"`
	ChatID   string   `json:"chat_id,omitempty"`
	SMTPHost string   `json:"smtp_host,omitempty"`
	SMTPPort int      `json:"smtp_port,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
}

// NotifyRule sends an event to channels.
type NotifyRule struct {
	Event    string   `json:"event"`
	Channels []string `json:"channels"`
}

// NotifyEvents are the events a rule can name.
var NotifyEvents = []string{
	"commit",           // a config commit was applied (who, revision)
	"login_failed",     // a user was locked out after repeated wrong passwords
	"ips_block",        // IPS dropped traffic (batched per minute)
	"feed_error",       // a threat feed download failed
	"wan_down",         // a WAN gateway stopped answering (multi-WAN, task 049)
	"wan_up",           // a WAN gateway is back
	"update_available", // a newer Goblin Gate is in the update channel
	"plugin",           // a plugin sent an alert (POST /api/v1/plugin/notify)
	"block_added",      // an address was blocked temporarily (POST /api/v1/blocks)
	"new_device",       // a new MAC address appeared on the network (device inventory, task 070)
	"test",             // sent by POST /api/v1/notifications/test
}

func (v *validator) checkNotifications() {
	n := v.cfg.Notifications
	names := map[string]bool{}
	for i, c := range n.Channels {
		p := fmt.Sprintf("notifications.channels[%d]", i)
		if !nameRe.MatchString(c.Name) {
			v.add(p+".name", "invalid channel name %q", c.Name)
		} else if names[c.Name] {
			v.add(p+".name", "channel name %q is already used", c.Name)
		}
		names[c.Name] = true
		url := func(field, val string) {
			if !strings.HasPrefix(val, "https://") && !strings.HasPrefix(val, "http://") {
				v.add(p+"."+field, "want an http:// or https:// URL")
			}
		}
		switch c.Type {
		case "webhook", "discord", "ntfy":
			url("url", c.URL)
		case "telegram":
			if c.BotToken == "" || strings.ContainsAny(c.BotToken, "/?# ") {
				v.add(p+".bot_token", "a Telegram bot token is required")
			}
			if c.ChatID == "" {
				v.add(p+".chat_id", "a Telegram chat id is required")
			}
		case "email":
			if !validHostname(c.SMTPHost) {
				v.add(p+".smtp_host", "invalid SMTP server %q", c.SMTPHost)
			}
			if c.SMTPPort < 0 || c.SMTPPort > 65535 {
				v.add(p+".smtp_port", "smtp_port %d out of range", c.SMTPPort)
			}
			if _, err := mail.ParseAddress(c.From); err != nil {
				v.add(p+".from", "invalid sender address %q", c.From)
			}
			if len(c.To) == 0 {
				v.add(p+".to", "at least one recipient is required")
			}
			for j, to := range c.To {
				if _, err := mail.ParseAddress(to); err != nil {
					v.add(fmt.Sprintf("%s.to[%d]", p, j), "invalid address %q", to)
				}
			}
		default:
			v.add(p+".type", "unknown channel type %q: want email, webhook, discord, ntfy or telegram", c.Type)
		}
	}
	for i, r := range n.Rules {
		p := fmt.Sprintf("notifications.rules[%d]", i)
		if !oneOf(r.Event, NotifyEvents...) {
			v.add(p+".event", "unknown event %q", r.Event)
		}
		for j, ch := range r.Channels {
			if !names[ch] {
				v.add(fmt.Sprintf("%s.channels[%d]", p, j), "unknown channel %q", ch)
			}
		}
	}
}
