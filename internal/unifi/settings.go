// Package unifi is the UniFi plugin: it reads the clients a UniFi Network
// application knows (on a UniFi OS console such as a Cloud Key or a Dream
// Machine, or self-hosted) and
//
//   - keeps the plugin's live address objects filled with the addresses of
//     the clients on a UniFi network, on a Wi-Fi network (SSID), or carrying
//     gg-<object> in their note (capability objects:write);
//   - attaches where each device is connected to the device inventory: switch
//     and port, or access point, SSID, band and signal (devices:annotate).
//
// It only reads: it never changes anything on the controller. When the
// controller can't be reached, objects and annotations stay as they were.
package unifi

import (
	"crypto/tls"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// Settings are the plugin's settings, checked.
type Settings struct {
	URL      string // https://console:443, no path
	APIKey   string
	Site     string // the site's short name (internal reference), "default"
	TLS      *tls.Config
	Interval time.Duration
	// Networks and SSIDs map an object to the UniFi network or SSID whose
	// clients fill it.
	Networks []Mapping
	SSIDs    []Mapping
}

// Mapping puts the clients of a UniFi network or SSID into Object.
type Mapping struct {
	Object, Name string
}

var siteRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SettingsFrom checks the plugin's settings (secrets merged in).
func SettingsFrom(m map[string]any) (Settings, error) {
	str := func(k string) string {
		s, _ := m[k].(string)
		return strings.TrimSpace(s)
	}
	s := Settings{Interval: 60 * time.Second, Site: "default"}
	if v, ok := m["interval_seconds"].(float64); ok && v > 0 {
		s.Interval = time.Duration(v) * time.Second
	}
	raw := str("url")
	if raw == "" {
		return s, errors.New("url: enter the UniFi console's address, such as https://192.168.1.2")
	}
	base, err := pluginsdk.HTTPSURL(raw, "443")
	if err != nil {
		return s, fmt.Errorf("url: %w", err)
	}
	s.URL = base
	if s.APIKey = str("api_key"); s.APIKey == "" {
		return s, errors.New("api_key: create one in UniFi Network on the console: Settings → Control Plane → Integrations")
	}
	if v := str("site"); v != "" {
		if !siteRe.MatchString(v) {
			return s, fmt.Errorf("site: %q: want the site's short name, such as default", v)
		}
		s.Site = v
	}
	if s.TLS, err = pluginsdk.ServerTLS(str("ca"), str("fingerprint")); err != nil {
		return s, err
	}
	if s.Networks, err = mappings(m["network_objects"], "network_objects", "iot=IoT"); err != nil {
		return s, err
	}
	if s.SSIDs, err = mappings(m["ssid_objects"], "ssid_objects", "guests=Home Guest"); err != nil {
		return s, err
	}
	return s, nil
}

func mappings(v any, key, example string) ([]Mapping, error) {
	l, _ := v.([]any)
	var out []Mapping
	for _, x := range l {
		s, _ := x.(string)
		if strings.TrimSpace(s) == "" {
			continue
		}
		obj, name, ok := strings.Cut(s, "=")
		obj, name = strings.TrimSpace(obj), strings.TrimSpace(name)
		if !ok || obj == "" || name == "" {
			return nil, fmt.Errorf("%s: %q: want object=name, such as %s", key, s, example)
		}
		out = append(out, Mapping{Object: obj, Name: name})
	}
	return out, nil
}
