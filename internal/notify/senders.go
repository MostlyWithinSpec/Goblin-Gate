package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func (d *Dispatcher) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// post sends a request and maps the answer: 2xx ok, 4xx permanent (a retry
// will not help: wrong URL or token), anything else retried.
func (d *Dispatcher) post(ctx context.Context, url, contentType string, body []byte, header map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "goblin-gate")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", redact(err, url))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests:
		return permanentError{fmt.Errorf("the server refused the message: HTTP %d", resp.StatusCode)}
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
}

// redact keeps URLs (which may hold a token, as Telegram's do) out of errors.
func redact(err error, url string) error {
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), url, "<url>"))
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// webhook: the event as JSON {event, key, title, message, time, fields}.
func (d *Dispatcher) sendWebhook(ctx context.Context, c config.NotifyChannel, ev Event) error {
	h := map[string]string{}
	if c.Token != "" {
		h["Authorization"] = "Bearer " + c.Token
	}
	return d.post(ctx, c.URL, "application/json", jsonBody(ev), h)
}

// discord: an execute-webhook message (2000 characters at most).
func (d *Dispatcher) sendDiscord(ctx context.Context, c config.NotifyChannel, ev Event) error {
	text := "**" + ev.Title + "**\n" + ev.Message
	if len(text) > 1990 {
		text = text[:1990] + "…"
	}
	return d.post(ctx, c.URL, "application/json", jsonBody(map[string]string{"content": text}), nil)
}

// ntfy: the message as the body, the title and tags as headers.
func (d *Dispatcher) sendNtfy(ctx context.Context, c config.NotifyChannel, ev Event) error {
	h := map[string]string{"Title": ev.Title, "Tags": "goblin," + ev.Kind}
	if ev.Kind == "wan_down" || ev.Kind == "ips_block" || ev.Kind == "login_failed" {
		h["Priority"] = "high"
	}
	if c.Token != "" {
		h["Authorization"] = "Bearer " + c.Token
	}
	return d.post(ctx, c.URL, "text/plain; charset=utf-8", []byte(ev.Message), h)
}

// telegram: Bot API sendMessage. The bot token is part of the URL, so errors
// never include the URL.
func (d *Dispatcher) sendTelegram(ctx context.Context, c config.NotifyChannel, ev Event) error {
	base := d.TelegramAPI
	if base == "" {
		base = "https://api.telegram.org"
	}
	body := jsonBody(map[string]string{"chat_id": c.ChatID, "text": ev.Title + "\n" + ev.Message})
	return d.post(ctx, base+"/bot"+c.BotToken+"/sendMessage", "application/json", body, nil)
}

// sendEmail: SMTP with STARTTLS when the server offers it (port 587/25), or
// TLS from the start on port 465; PLAIN auth when a user name is set.
func sendEmail(ctx context.Context, c config.NotifyChannel, ev Event) error {
	port := c.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: c.SMTPHost})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer cl.Close()
	if ok, _ := cl.Extension("STARTTLS"); ok && port != 465 {
		if err := cl.StartTLS(&tls.Config{ServerName: c.SMTPHost}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.SMTPHost)); err != nil {
			return permanentError{fmt.Errorf("smtp login failed: %w", err)}
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, to := range c.To {
		if err := cl.Rcpt(to); err != nil {
			return permanentError{fmt.Errorf("smtp recipient %s: %w", to, err)}
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(ev.Title)
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		c.From, strings.Join(c.To, ", "), subject, ev.Time.Format(time.RFC1123Z), strings.ReplaceAll(ev.Message, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return cl.Quit()
}
