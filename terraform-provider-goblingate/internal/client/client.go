// Package client talks to the Goblin Gate API (/api/v1) for the Terraform
// provider: it reads the running config and makes changes through the
// candidate, one commit at a time.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Options configure a Client.
type Options struct {
	URL            string // https://gate.home.lan
	Token          string // API token; empty for a gate without authentication (dev mode)
	Insecure       bool   // skip TLS verification (a self-signed certificate)
	CAFile         string // PEM file with the CA that signed the gate's certificate
	ConfirmMinutes int    // > 0: commit with a confirm timer, then confirm
	UserAgent      string
	// Window is how long a batch waits for more changes before it commits
	// (0 = DefaultWindow).
	Window time.Duration
}

// DefaultWindow collects the changes Terraform makes in parallel into one
// commit.
const DefaultWindow = 750 * time.Millisecond

// Client is safe for concurrent use.
type Client struct {
	base    *url.URL
	token   string
	ua      string
	http    *http.Client
	confirm int
	batch   *batcher
}

// New checks the options and returns a client.
func New(o Options) (*Client, error) {
	if o.URL == "" {
		return nil, errors.New("url is required")
	}
	u, err := url.Parse(strings.TrimRight(o.URL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("url %q: want https://host[:port]", o.URL)
	}
	if o.ConfirmMinutes < 0 || o.ConfirmMinutes > 1440 {
		return nil, fmt.Errorf("confirm_minutes must be 0-1440")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: o.Insecure} //nolint:gosec // the user asked for it
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %s: no PEM certificate in it", o.CAFile)
		}
		tc.RootCAs = pool
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tc
	c := &Client{
		base:    u,
		token:   o.Token,
		ua:      o.UserAgent,
		http:    &http.Client{Transport: tr, Timeout: 3 * time.Minute}, // a commit applies the whole config
		confirm: o.ConfirmMinutes,
	}
	w := o.Window
	if w <= 0 {
		w = DefaultWindow
	}
	c.batch = newBatcher(c, w)
	return c, nil
}

// APIError is an error answer of the API.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details json.RawMessage
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("%s (HTTP %d", e.Message, e.Status)
	if e.Code != "" {
		msg += ", " + e.Code
	}
	msg += ")"
	var problems []struct{ Path, Message string }
	if json.Unmarshal(e.Details, &problems) == nil && len(problems) > 0 {
		for _, p := range problems {
			msg += "\n  " + p.Path + ": " + p.Message
		}
	} else if len(e.Details) > 0 && string(e.Details) != "null" {
		var s string
		if json.Unmarshal(e.Details, &s) == nil {
			msg += ": " + s
		} else {
			msg += ": " + string(e.Details)
		}
	}
	return msg
}

// IsNotFound reports a 404 answer.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Do sends a request; body (if not nil) is sent as JSON and the answer is
// decoded into out (if not nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Details json.RawMessage `json:"details"`
			} `json:"error"`
		}
		ae := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			ae.Code, ae.Message, ae.Details = e.Error.Code, e.Error.Message, e.Error.Details
		} else {
			ae.Message = strings.TrimSpace(string(data))
			if ae.Message == "" {
				ae.Message = resp.Status
			}
		}
		if resp.StatusCode == http.StatusUnauthorized {
			ae.Message += "; check the token"
		}
		return ae
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}

// Config is a config document as generic JSON, so that fields this
// provider does not know are kept when it writes an object back.
type Config = map[string]any

// Running returns the running (committed) config.
func (c *Client) Running(ctx context.Context) (Config, error) {
	var cfg Config
	err := c.Do(ctx, http.MethodGet, "/api/v1/config/running", nil, &cfg)
	return cfg, err
}

func (c *Client) candidate(ctx context.Context) (Config, error) {
	var cfg Config
	err := c.Do(ctx, http.MethodGet, "/api/v1/config/candidate", nil, &cfg)
	return cfg, err
}

// Edit is what a change sees: the candidate as it is now (with the earlier
// changes of the same batch in it), and the means to change it.
type Edit interface {
	Candidate() (Config, error)
	Set(path string, value any) error
	Delete(path string) error
}

// Change runs fn as part of the next commit. summary ("create address web")
// goes into the commit comment. It returns when the commit is done; when
// any change of the batch fails, nothing is committed and every change of
// the batch gets the error.
func (c *Client) Change(ctx context.Context, summary string, fn func(Edit) error) error {
	return c.batch.run(ctx, summary, fn)
}

type edit struct {
	ctx context.Context
	c   *Client
}

func (e *edit) Candidate() (Config, error) { return e.c.candidate(e.ctx) }

func (e *edit) Set(path string, value any) error {
	return e.c.Do(e.ctx, http.MethodPatch, "/api/v1/config/candidate", map[string]any{"path": path, "value": value}, nil)
}

func (e *edit) Delete(path string) error {
	return e.c.Do(e.ctx, http.MethodPatch, "/api/v1/config/candidate", map[string]any{"path": path, "delete": true}, nil)
}

// clean refuses to start when the candidate holds changes that are not
// ours: committing would apply someone else's half-finished edit.
func (c *Client) clean(ctx context.Context) error {
	var d struct {
		Count   int `json:"count"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/v1/config/diff", nil, &d); err != nil {
		return fmt.Errorf("read the uncommitted changes: %w", err)
	}
	if d.Count == 0 {
		return nil
	}
	paths := make([]string, 0, 3)
	for i, ch := range d.Changes {
		if i == 3 {
			paths = append(paths, "…")
			break
		}
		paths = append(paths, ch.Path)
	}
	return fmt.Errorf("the gate has %d uncommitted change(s) (%s) that Terraform did not make; commit or discard them first (web UI: the Commit button, or POST /api/v1/commit/discard), then run Terraform again", d.Count, strings.Join(paths, ", "))
}

// commit commits the candidate, and with a confirm timer confirms it once
// the gate still answers.
func (c *Client) commit(ctx context.Context, comment string) error {
	body := map[string]any{"comment": comment}
	if c.confirm > 0 {
		body["confirm_minutes"] = c.confirm
	}
	var res struct {
		Revision int `json:"revision"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/v1/commit", body, &res); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if c.confirm > 0 {
		if err := c.Do(ctx, http.MethodPost, "/api/v1/commit/confirm", nil, nil); err != nil {
			return fmt.Errorf("revision %d was committed with a %d-minute confirm timer, but confirming it failed (%w); unless it is confirmed, the gate rolls back to the previous revision when the timer ends", res.Revision, c.confirm, err)
		}
	}
	return nil
}

func (c *Client) discard(ctx context.Context) {
	_ = c.Do(context.WithoutCancel(ctx), http.MethodPost, "/api/v1/commit/discard", nil, nil)
}
