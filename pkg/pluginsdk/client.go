package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// DefaultSocket is where goblind listens for plugins.
const DefaultSocket = "/run/goblin-plugins/api.sock"

// Client calls goblind's plugin API.
type Client struct {
	// HTTP sends the requests; NewClient dials the Unix socket.
	HTTP *http.Client
	// Base is the URL prefix: "http://goblind" on the socket (the host is not
	// used), a test server's URL in tests.
	Base string
}

// NewClient talks to goblind on a Unix socket ("" = DefaultSocket).
func NewClient(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket
	}
	return &Client{Base: "http://goblind", HTTP: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// APIError is a non-2xx answer from goblind.
type APIError struct {
	Status  int    // HTTP status
	Code    string // goblind's error code, e.g. "scope_denied", "rate_limited"
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("goblind: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("goblind: %d %s", e.Status, e.Message)
}

// Denied reports whether err is goblind refusing a route because its
// capability is not approved (403).
func Denied(err error) bool { return StatusOf(err) == http.StatusForbidden }

// StatusOf returns the HTTP status of an APIError in err's chain, 0 for any
// other error (a network failure, a bad answer).
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// maxAnswer bounds an answer goblind sends (a large config or log page).
const maxAnswer = 32 << 20

// Do sends one request: body (when not nil) as JSON, the answer decoded into
// out (when not nil). Use it for routes the SDK has no typed call for.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
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
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
