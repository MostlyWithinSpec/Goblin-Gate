package logexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// lokiBatchMax is the max number of events in one push batch.
const lokiBatchMax = 500

// lokiClientTimeout bounds a single push request.
const lokiClientTimeout = 10 * time.Second

type lokiValue struct {
	ts    string
	value string
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

// sendLoki pushes evs to the Loki push API as one JSON body, grouped into
// one stream per event kind. A failed batch is retried once; a second
// failure is returned to the caller (which counts it as dropped).
func sendLoki(ctx context.Context, c config.LogExport, evs []*logs.Event) error {
	body, err := buildLokiBody(evs)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastErr = doLokiPush(ctx, c, body)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func buildLokiBody(evs []*logs.Event) ([]byte, error) {
	streams := map[string]*lokiStream{}
	var order []string
	for _, ev := range evs {
		kind := ev.Kind
		if kind == "" {
			kind = "system"
		}
		st, ok := streams[kind]
		if !ok {
			st = &lokiStream{Stream: map[string]string{"job": "goblin-gate", "kind": kind}}
			streams[kind] = st
			order = append(order, kind)
		}
		evJSON, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("loki: marshal event: %w", err)
		}
		ts := strconv.FormatInt(ev.Time*1e9, 10)
		st.Values = append(st.Values, [2]string{ts, string(evJSON)})
	}
	out := make([]lokiStream, 0, len(order))
	for _, k := range order {
		out = append(out, *streams[k])
	}
	payload := map[string]any{"streams": out}
	return json.Marshal(payload)
}

func doLokiPush(ctx context.Context, c config.LogExport, body []byte) error {
	client := &http.Client{Timeout: lokiClientTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("loki: bad request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("loki: push to host %s: %w", hostOnlyURL(c.URL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("loki: push to host %s failed with status %d", hostOnlyURL(c.URL), resp.StatusCode)
	}
	return nil
}

// hostOnlyURL returns the host part of a URL for error messages, so errors
// never carry a full URL (which might embed credentials). Userinfo is
// stripped.
func hostOnlyURL(u string) string {
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	// Strip userinfo: find the first '/' and look for '@' before it.
	slash := strings.IndexByte(rest, '/')
	hostPart := rest
	if slash >= 0 {
		hostPart = rest[:slash]
	}
	if at := strings.LastIndexByte(hostPart, '@'); at > 0 {
		hostPart = hostPart[at+1:]
	}
	return hostPart
}
