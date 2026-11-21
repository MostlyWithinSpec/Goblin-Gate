package unifi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

var ctx = context.Background()

// console serves the recorded answers of a UniFi OS console (prefix
// "/proxy/network") or of a self-hosted Network application (prefix "").
type console struct {
	srv    *httptest.Server
	prefix string
	key    string
	mu     sync.Mutex
	down   bool
	hits   []string
}

func newConsole(t *testing.T, prefix string) *console {
	c := &console{prefix: prefix, key: "good-key"}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hits = append(c.hits, r.URL.Path)
		down := c.down
		c.mu.Unlock()
		if down {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Header.Get("X-API-KEY") != c.key {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"code":401,"message":"Unauthorized"}}`)
			return
		}
		file := map[string]string{
			c.prefix + "/api/s/default/stat/sta":          "testdata/stat-sta.json",
			c.prefix + "/api/s/default/stat/device-basic": "testdata/stat-device-basic.json",
		}[r.URL.Path]
		if file == "" {
			if strings.HasPrefix(r.URL.Path, c.prefix+"/api/s/") {
				// what UniFi answers for an unknown site
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"meta":{"rc":"error","msg":"api.err.NoSiteContext"},"data":[]}`)
				return
			}
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Error(err)
		}
		w.Write(data)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *console) fingerprint() string {
	sum := sha256.Sum256(c.srv.Certificate().Raw)
	return pluginsdk.ColonHex(sum[:])
}

func (c *console) settings(t *testing.T, extra map[string]any) Settings {
	t.Helper()
	m := map[string]any{"url": c.srv.URL, "api_key": c.key, "fingerprint": c.fingerprint(),
		"network_objects": []any{"iot=IoT"}, "ssid_objects": []any{"guests=Home Guest"}}
	maps.Copy(m, extra)
	s, err := SettingsFrom(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSettings(t *testing.T) {
	s, err := SettingsFrom(map[string]any{"url": "https://192.168.1.2", "api_key": "k", "network_objects": []any{"iot = IoT", ""},
		"interval_seconds": float64(30)})
	if err != nil || s.URL != "https://192.168.1.2:443" || s.Site != "default" || len(s.Networks) != 1 || s.Networks[0] != (Mapping{"iot", "IoT"}) || s.Interval.Seconds() != 30 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []map[string]any{
		{},
		{"url": "http://unifi.lan", "api_key": "k"},
		{"url": "https://unifi.lan"},
		{"url": "https://unifi.lan", "api_key": "k", "site": "../x"},
		{"url": "https://unifi.lan", "api_key": "k", "fingerprint": "AB:CD"},
		{"url": "https://unifi.lan", "api_key": "k", "ssid_objects": []any{"guests"}},
	} {
		if _, err := SettingsFrom(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestManifest(t *testing.T) {
	data, err := os.ReadFile("../../plugins/unifi/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest(data, "unifi")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Capabilities, []string{"objects:write", "devices:annotate"}) {
		t.Errorf("capabilities %v", m.Capabilities)
	}
	var raw struct {
		Settings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"settings_schema"`
	}
	json.Unmarshal(data, &raw)
	for _, k := range []string{"url", "api_key", "fingerprint", "ca", "site", "network_objects", "ssid_objects", "interval_seconds"} {
		if _, ok := raw.Settings.Properties[k]; !ok {
			t.Errorf("manifest lacks %s", k)
		}
	}
}

func TestClient(t *testing.T) {
	for _, prefix := range []string{"/proxy/network", ""} {
		c := newConsole(t, prefix)
		cl := &Client{Settings: c.settings(t, nil)}
		st, err := cl.Stations(ctx)
		if err != nil || len(st) != 4 || st[0].SwitchPort != 7 || st[1].ESSID != "Home IoT" || st[2].IPv6[0] != "fd00:40::12" {
			t.Fatalf("%q: %+v %v", prefix, st, err)
		}
		devs, err := cl.Devices(ctx)
		if err != nil || len(devs) != 3 || devs[0].Name != "Office Switch" {
			t.Fatalf("%q: devices %+v %v", prefix, devs, err)
		}
		// the prefix is found once, not probed on every call
		if prefix == "" && len(c.hits) != 3 {
			t.Errorf("self-hosted hits %v", c.hits)
		}
	}

	c := newConsole(t, "/proxy/network")
	// a refused key says where to make a working one
	_, err := (&Client{Settings: c.settings(t, map[string]any{"api_key": "cloud-key"})}).Stations(ctx)
	if err == nil || !strings.Contains(err.Error(), "Control Plane → Integrations") {
		t.Errorf("bad key: %v", err)
	}
	// an unknown site
	if _, err := (&Client{Settings: c.settings(t, map[string]any{"site": "nope"})}).Stations(ctx); err == nil {
		t.Error("unknown site accepted")
	}
	// a certificate other than the pinned one (httptest servers all share
	// one certificate, so pin another fingerprint)
	s := c.settings(t, map[string]any{"fingerprint": pluginsdk.ColonHex(make([]byte, 32))})
	if _, err := (&Client{Settings: s}).Stations(ctx); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("wrong certificate: %v", err)
	}
}

func TestAnnotations(t *testing.T) {
	c := newConsole(t, "/proxy/network")
	cl := &Client{Settings: c.settings(t, nil)}
	st, _ := cl.Stations(ctx)
	devs, _ := cl.Devices(ctx)
	got := Annotations(st, devs)
	want := map[string]map[string]string{
		"020000001001": {"connection": "wired", "switch": "Office Switch", "port": "7", "link_mbps": "1000", "network": "LAN"},
		"020000001002": {"connection": "wifi", "ap": "Hall AP", "ssid": "Home IoT", "band": "2.4 GHz", "network": "IoT", "vlan": "30"},
		// a device without a name: the client's uplink name stands in
		"020000001003": {"connection": "wifi", "ap": "Garage AP", "ssid": "Home Guest", "band": "5 GHz", "network": "Guest", "vlan": "40"},
		// a switch not in the device list
		"020000001004": {"connection": "wired", "switch": "Desk Switch", "port": "3", "link_mbps": "100", "network": "IoT", "vlan": "30"},
	}
	if !maps.EqualFunc(got, want, func(a, b map[string]string) bool { return maps.Equal(a, b) }) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for mac, f := range got {
		if len(f) > 8 {
			t.Errorf("%s: %d fields", mac, len(f))
		}
	}
}

// fakeAPI is goblind's plugin API.
type fakeAPI struct {
	objs        map[string][]netip.Prefix
	sets        int
	annotations map[string]map[string]string
	annSets     int
}

func (f *fakeAPI) LiveObjects(ctx context.Context) ([]pluginsdk.LiveObject, error) {
	var out []pluginsdk.LiveObject
	for _, n := range slices.Sorted(maps.Keys(f.objs)) {
		out = append(out, pluginsdk.LiveObject{Name: n, Plugin: "unifi", Members: f.objs[n]})
	}
	return out, nil
}

func (f *fakeAPI) SetLiveObject(ctx context.Context, name string, members []string) (pluginsdk.LiveChange, error) {
	f.sets++
	var ps []netip.Prefix
	for _, m := range members {
		ps = append(ps, netip.MustParsePrefix(m))
	}
	f.objs[name] = ps
	return pluginsdk.LiveChange{Name: name, Members: len(ps), Changed: true}, nil
}

func (f *fakeAPI) SetDeviceAnnotations(ctx context.Context, d map[string]map[string]string) (bool, error) {
	f.annSets++
	f.annotations = d
	return true, nil
}

func members(ps []netip.Prefix) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.String())
	}
	return strings.Join(s, " ")
}

func TestSync(t *testing.T) {
	c := newConsole(t, "/proxy/network")
	settings := c.settings(t, nil)
	api := &fakeAPI{objs: map[string][]netip.Prefix{"IoT": nil, "guests": nil, "servers": nil, "other": {netip.MustParsePrefix("10.0.0.1/32")}}}
	s := &Syncer{API: api, Source: &Client{Settings: settings}, Settings: settings, Objects: true, Annotate: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := s.Once(ctx); err != nil {
		t.Fatal(err)
	}
	// by network (object names match case-insensitively), by SSID with IPv6,
	// by note; an object nothing maps to is emptied
	for obj, want := range map[string]string{
		"IoT":     "192.168.30.40/32 192.168.30.41/32",
		"guests":  "192.168.40.12/32 fd00:40::12/128",
		"servers": "192.168.10.21/32",
		"other":   "",
	} {
		if got := members(api.objs[obj]); got != want {
			t.Errorf("%s: %q, want %q", obj, got, want)
		}
	}
	if len(api.annotations) != 4 || api.annotations["020000001001"]["port"] != "7" {
		t.Errorf("annotations %v", api.annotations)
	}
	// nothing changed: nothing is sent
	sets, annSets := api.sets, api.annSets
	if err := s.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if api.sets != sets || api.annSets != annSets {
		t.Errorf("a quiet poll sent %d objects, %d annotation sets", api.sets-sets, api.annSets-annSets)
	}
	// the controller down: an error, and objects and details stay
	c.down = true
	before := members(api.objs["IoT"])
	if err := s.Once(ctx); err == nil {
		t.Error("no error with the console down")
	}
	if members(api.objs["IoT"]) != before || api.sets != sets || api.annSets != annSets {
		t.Error("an outage changed something")
	}
}

func TestSyncWithoutDeviceNames(t *testing.T) {
	// device-basic failing must not stop the details: the uplink names stand in
	st := []Station{{MAC: "02:00:00:00:10:01", Wired: true, SwitchMAC: "02:00:00:00:00:01", SwitchPort: 2, LastUplinkName: "Office Switch"}}
	api := &fakeAPI{objs: map[string][]netip.Prefix{}}
	s := &Syncer{API: api, Source: fakeSource{st: st, devErr: errors.New("HTTP 500")}, Annotate: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := s.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if api.annotations["020000001001"]["switch"] != "Office Switch" {
		t.Errorf("%v", api.annotations)
	}
}

type fakeSource struct {
	st     []Station
	devErr error
}

func (f fakeSource) Stations(context.Context) ([]Station, error) { return f.st, nil }
func (f fakeSource) Devices(context.Context) ([]Device, error)   { return nil, f.devErr }
