package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/capture"
)

// onePacketPcap is a pcap stream with one ARP-sized ethernet frame.
func onePacketPcap() []byte {
	var b bytes.Buffer
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], 1)
	b.Write(h)
	frame := make([]byte, 42)
	binary.BigEndian.PutUint16(frame[12:], 0x0806)
	r := make([]byte, 16)
	binary.LittleEndian.PutUint32(r[8:], uint32(len(frame)))
	binary.LittleEndian.PutUint32(r[12:], uint32(len(frame)))
	b.Write(r)
	b.Write(frame)
	return b.Bytes()
}

func newCaptureAPI(t *testing.T) (*testAPI, *[]string) {
	var args []string
	a := newTestAPIWith(t, func(s *Server) {
		s.Capture = &capture.Manager{Dir: t.TempDir(), Command: func(ctx context.Context, a []string) (io.ReadCloser, func() error, error) {
			args = a
			return io.NopCloser(bytes.NewReader(onePacketPcap())), func() error { return nil }, nil
		}}
	})
	return a, &args
}

func TestCaptureAPI(t *testing.T) {
	a, args := newCaptureAPI(t)
	out := a.expect("POST", "/api/v1/capture", map[string]any{"interface": "lan", "host": "192.168.1.23", "protocol": "tcp", "port": 8123, "max_packets": 10}, 201)
	id, _ := out["id"].(string)
	if id == "" || out["filter"] != "tcp and host 192.168.1.23 and port 8123" || out["interface"] != "lan" {
		t.Fatalf("start: %v", out)
	}
	if (*args)[1] != "eth1" { // the default config's LAN device
		t.Fatalf("tcpdump args %v", *args)
	}
	a.server.Capture.Wait(id)
	if st := a.expect("GET", "/api/v1/capture/"+id, nil, 200); st["state"] != "done" || st["packets"].(float64) != 1 {
		t.Fatalf("status: %v", st)
	}

	resp, err := http.Get(a.srv.URL + "/api/v1/capture/" + id + "/pcap")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, onePacketPcap()) || !strings.Contains(resp.Header.Get("Content-Disposition"), id) {
		t.Fatalf("pcap: %d %d bytes %v", resp.StatusCode, len(body), resp.Header)
	}

	// a finished capture's live stream ends with done at once
	resp, err = http.Get(a.srv.URL + "/api/v1/capture/" + id + "/live")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(string(live), "event: done\ndata: {") {
		t.Fatalf("live: %q", live)
	}

	for _, bad := range []map[string]any{
		{"interface": "ghost"},
		{"interface": "lan", "host": "not-an-ip"},
		{"interface": "lan", "protocol": "tcp; reboot"},
		{"interface": "lan", "max_seconds": 3600},
	} {
		a.expect("POST", "/api/v1/capture", bad, 400)
	}
	a.expect("GET", "/api/v1/capture/nope", nil, 404)
	req, _ := http.NewRequest(http.MethodDelete, a.srv.URL+"/api/v1/capture/"+id, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %v %v", resp, err)
	}
	resp.Body.Close()
	a.expect("GET", "/api/v1/capture/"+id, nil, 404)
}

func TestCaptureAPIDisabledAndReadonly(t *testing.T) {
	plain := newTestAPI(t)
	if out := plain.expect("POST", "/api/v1/capture", map[string]any{"interface": "lan"}, 503); errCode(out) != "capture_unavailable" {
		t.Fatalf("no capture manager: %v", out)
	}

	a := newAuthAPI(t)
	a.server.Capture = &capture.Manager{Dir: t.TempDir()}
	b := a.browser()
	b.login("viewer")
	b.expect("POST", "/api/v1/capture", map[string]any{"interface": "lan"}, 403)
	b.expect("GET", "/api/v1/capture/x", nil, 403)
}
