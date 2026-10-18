package logexport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

type lokiPayload struct {
	Streams []struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	} `json:"streams"`
}

// TestSendLokiBodyShape checks the push body groups events into one stream
// per kind with unix-nanosecond timestamps.
func TestSendLokiBodyShape(t *testing.T) {
	var got lokiPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	evs := []*logs.Event{
		{Time: 1700000000, Kind: "traffic", Action: "allow"},
		{Time: 1700000001, Kind: "ips", Action: "alert"},
		{Time: 1700000002, Kind: "traffic", Action: "deny"},
	}
	c := config.LogExport{Name: "l", Type: "loki", URL: srv.URL + "/loki/api/v1/push"}
	if err := sendLoki(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}

	if len(got.Streams) != 2 {
		t.Fatalf("got %d streams, want 2 (one per kind)", len(got.Streams))
	}
	var traffic, ips *struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	for i := range got.Streams {
		if got.Streams[i].Stream["kind"] == "traffic" {
			traffic = &got.Streams[i]
		}
		if got.Streams[i].Stream["kind"] == "ips" {
			ips = &got.Streams[i]
		}
	}
	if traffic == nil || ips == nil {
		t.Fatalf("missing streams: %+v", got.Streams)
	}
	if traffic.Stream["job"] != "goblin-gate" {
		t.Errorf("job = %q, want goblin-gate", traffic.Stream["job"])
	}
	if len(traffic.Values) != 2 || len(ips.Values) != 1 {
		t.Fatalf("traffic values = %d, ips values = %d", len(traffic.Values), len(ips.Values))
	}
	if traffic.Values[0][0] != "1700000000000000000" {
		t.Errorf("ts = %q, want unix ns string", traffic.Values[0][0])
	}
	var ev logs.Event
	if err := json.Unmarshal([]byte(traffic.Values[0][1]), &ev); err != nil {
		t.Fatalf("value is not event JSON: %v", err)
	}
	if ev.Action != "allow" {
		t.Errorf("decoded action = %q", ev.Action)
	}
}

// TestSendLokiBasicAuth checks the basic-auth header.
func TestSendLokiBasicAuth(t *testing.T) {
	var user, pass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
	}))
	defer srv.Close()

	c := config.LogExport{Name: "b", Type: "loki", URL: srv.URL + "/loki/api/v1/push", Username: "alice", Password: "s3cret"}
	evs := []*logs.Event{{Time: 1, Kind: "system", Action: "allow"}}
	if err := sendLoki(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}
	if user != "alice" || pass != "s3cret" {
		t.Errorf("basic auth = %q/%q, want alice/s3cret", user, pass)
	}
}

// TestSendLokiBearer checks the bearer token header.
func TestSendLokiBearer(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := config.LogExport{Name: "t", Type: "loki", URL: srv.URL + "/loki/api/v1/push", Token: "tok-123"}
	evs := []*logs.Event{{Time: 1, Kind: "system", Action: "allow"}}
	if err := sendLoki(context.Background(), c, evs); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", auth)
	}
}

// TestSendLokiRetryOnce checks a failed batch is retried once then returned.
func TestSendLokiRetryOnce(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := config.LogExport{Name: "r", Type: "loki", URL: srv.URL + "/loki/api/v1/push"}
	evs := []*logs.Event{{Time: 1, Kind: "system", Action: "allow"}}
	if err := sendLoki(context.Background(), c, evs); err == nil {
		t.Fatal("expected an error after two failures")
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one retry)", attempts)
	}
}
