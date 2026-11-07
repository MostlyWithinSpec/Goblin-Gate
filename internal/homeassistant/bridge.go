// Package homeassistant is the Home Assistant plugin: it publishes the box's
// state to an MQTT broker with Home Assistant's MQTT discovery (entities
// appear by themselves in Home Assistant's MQTT integration) and switches
// the policies the admin allowed when Home Assistant asks.
//
// Entities, each only with the capability it needs:
//   - stats:read: per WAN a connectivity sensor and its IPv4 address; per
//     monitored gateway connectivity, latency and loss; CPU, memory,
//     sessions and boot time.
//   - devices:read: a presence tracker (home / not_home) per device the
//     admin listed by MAC address.
//   - policies:toggle: a switch per policy the admin allowed.
package homeassistant

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Settings are the plugin's settings (plugin.json).
type Settings struct {
	Broker          string // host[:port]
	Username        string
	Password        string
	TLS             bool
	TLSSkipVerify   bool
	DiscoveryPrefix string
	BaseTopic       string
	DeviceName      string
	Interval        time.Duration
	Devices         []string // MAC addresses, normalized
	Away            time.Duration
}

// SettingsFrom reads the settings goblind hands over (defaults filled in).
func SettingsFrom(m map[string]any) (Settings, error) {
	str := func(k, def string) string {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
		return def
	}
	num := func(k string, def float64) float64 {
		if v, ok := m[k].(float64); ok {
			return v
		}
		return def
	}
	flag := func(k string) bool { v, _ := m[k].(bool); return v }
	s := Settings{
		Broker: strings.TrimSpace(str("broker", "")), Username: str("username", ""), Password: str("password", ""),
		TLS: flag("tls"), TLSSkipVerify: flag("tls_skip_verify"),
		DiscoveryPrefix: strings.Trim(str("discovery_prefix", "homeassistant"), "/"),
		BaseTopic:       strings.Trim(str("base_topic", "goblin_gate"), "/"),
		DeviceName:      str("device_name", "Goblin Gate"),
		Interval:        time.Duration(num("interval_seconds", 30)) * time.Second,
		Away:            time.Duration(num("away_minutes", 10)) * time.Minute,
	}
	if s.Broker == "" {
		return s, errors.New("no broker set: enter the MQTT broker's address in the plugin settings")
	}
	if _, _, err := net.SplitHostPort(s.Broker); err != nil {
		port := "1883"
		if s.TLS {
			port = "8883"
		}
		s.Broker = net.JoinHostPort(strings.Trim(s.Broker, "[]"), port)
	}
	for _, t := range []string{s.DiscoveryPrefix, s.BaseTopic} {
		if t == "" || strings.ContainsAny(t, "+#\x00") {
			return s, fmt.Errorf("topic %q: no empty topics and no MQTT wildcards (+, #)", t)
		}
	}
	if list, ok := m["devices"].([]any); ok {
		for _, v := range list {
			str, _ := v.(string)
			mac, err := normalizeMAC(str)
			if err != nil {
				return s, fmt.Errorf("devices: %w", err)
			}
			if !slices.Contains(s.Devices, mac) {
				s.Devices = append(s.Devices, mac)
			}
		}
	}
	return s, nil
}

func normalizeMAC(s string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil || len(hw) != 6 {
		if h, herr := hex.DecodeString(strings.TrimSpace(s)); herr == nil && len(h) == 6 {
			return net.HardwareAddr(h).String(), nil
		}
		return "", fmt.Errorf("%q is not a MAC address", s)
	}
	return hw.String(), nil
}

// API is what the bridge reads from goblind (Client).
type API interface {
	WANs(context.Context) ([]WAN, error)
	Gateways(context.Context) ([]Gateway, error)
	System(context.Context) (System, error)
	Devices(context.Context) ([]Device, error)
	Policies(context.Context) ([]Policy, error)
	SwitchPolicy(context.Context, uint32, bool) (Policy, error)
}

// Conn is a broker connection (MQTT).
type Conn interface {
	Publish(topic string, payload []byte, retain bool) error
	Subscribe(filters ...string) error
	Messages() <-chan Message
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Bridge connects goblind and the broker.
type Bridge struct {
	API          API
	Settings     Settings
	Capabilities []string
	Version      string // the plugin's version, shown in Home Assistant
	StateDir     string
	Logger       *slog.Logger
	// Dial connects to the broker; nil = DialMQTT.
	Dial func(context.Context, MQTTOptions) (Conn, error)
	Now  func() time.Time

	st       persisted
	sections map[string][]entity // last good entities per section
	sentCfg  map[string]string   // discovery config topic -> payload, this session
	sentVal  map[string]string   // state topic -> payload, this session
}

type persisted struct {
	BoxID     string   `json:"box_id"`
	Published []string `json:"published"` // discovery config topics
}

// entity is one Home Assistant entity and its current values.
type entity struct {
	component string // binary_sensor, sensor, device_tracker, switch
	object    string // a-z0-9_
	config    map[string]any
	values    map[string]string // topic -> payload (state, attributes)
}

func (b *Bridge) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bridge) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *Bridge) can(c string) bool { return slices.Contains(b.Capabilities, c) }

func (b *Bridge) node() string { return "goblin_gate_" + b.st.BoxID }

func (b *Bridge) topic(parts ...string) string {
	return b.Settings.BaseTopic + "/" + strings.Join(parts, "/")
}

func (b *Bridge) availability() string { return b.topic("status") }

// Run keeps a broker connection until ctx ends, reconnecting with backoff.
func (b *Bridge) Run(ctx context.Context) error {
	if err := b.loadState(); err != nil {
		return err
	}
	dial := b.Dial
	if dial == nil {
		dial = func(ctx context.Context, o MQTTOptions) (Conn, error) { return DialMQTT(ctx, o) }
	}
	opts := MQTTOptions{
		Addr: b.Settings.Broker, ClientID: "goblin-gate-" + b.st.BoxID,
		Username: b.Settings.Username, Password: b.Settings.Password,
		WillTopic: b.availability(), WillMsg: []byte("offline"),
	}
	if b.Settings.TLS {
		host, _, _ := net.SplitHostPort(b.Settings.Broker)
		opts.TLS = &tls.Config{ServerName: host, InsecureSkipVerify: b.Settings.TLSSkipVerify, MinVersion: tls.VersionTLS12}
	}
	backoff := 5 * time.Second
	for {
		conn, err := dial(ctx, opts)
		if err == nil {
			b.log().Info("homeassistant: connected", "broker", b.Settings.Broker)
			backoff = 5 * time.Second
			err = b.session(ctx, conn)
			if ctx.Err() != nil {
				_ = conn.Publish(b.availability(), []byte("offline"), true)
				conn.Close()
				return nil
			}
			conn.Close()
		}
		b.log().Warn("homeassistant: broker connection", "broker", b.Settings.Broker, "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// session serves one connection until it drops.
func (b *Bridge) session(ctx context.Context, c Conn) error {
	b.sentCfg, b.sentVal = map[string]string{}, map[string]string{}
	if err := c.Publish(b.availability(), []byte("online"), true); err != nil {
		return err
	}
	filters := []string{b.Settings.DiscoveryPrefix + "/status"}
	if b.can("policies:toggle") {
		filters = append(filters, b.topic("policy", "+", "set"))
	}
	if err := c.Subscribe(filters...); err != nil {
		return err
	}
	if err := b.refresh(ctx, c); err != nil {
		return err
	}
	tick := time.NewTicker(b.Settings.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.Done():
			return c.Err()
		case <-tick.C:
			if err := b.refresh(ctx, c); err != nil {
				return err
			}
		case m, ok := <-c.Messages():
			if !ok {
				return c.Err()
			}
			if err := b.handle(ctx, c, m); err != nil {
				return err
			}
		}
	}
}

// handle answers Home Assistant's birth message and switch commands.
func (b *Bridge) handle(ctx context.Context, c Conn, m Message) error {
	if m.Topic == b.Settings.DiscoveryPrefix+"/status" {
		if string(m.Payload) == "online" { // Home Assistant restarted: send everything again
			b.sentCfg, b.sentVal = map[string]string{}, map[string]string{}
			return b.refresh(ctx, c)
		}
		return nil
	}
	rest, ok := strings.CutPrefix(m.Topic, b.topic("policy")+"/")
	idStr, ok2 := strings.CutSuffix(rest, "/set")
	if !ok || !ok2 || !b.can("policies:toggle") {
		return nil
	}
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return nil
	}
	var on bool
	switch strings.ToUpper(strings.TrimSpace(string(m.Payload))) {
	case "ON":
		on = true
	case "OFF":
	default:
		return nil
	}
	p, err := b.API.SwitchPolicy(ctx, uint32(id), on)
	if err != nil {
		b.log().Warn("homeassistant: switching a policy failed", "policy", id, "on", on, "error", err)
	} else {
		b.log().Info("homeassistant: switched a policy", "policy", p.Name, "on", p.Enabled)
	}
	// either way, report what the box has now (a refused switch flips back)
	delete(b.sentVal, b.topic("policy", idStr, "state"))
	return b.refresh(ctx, c)
}

// refresh collects the entities and publishes what changed: discovery for
// new or changed entities, removal for gone ones, and the values.
func (b *Bridge) refresh(ctx context.Context, c Conn) error {
	ents := b.collect(ctx)
	current := map[string]bool{}
	for _, e := range ents {
		topic := fmt.Sprintf("%s/%s/%s/%s/config", b.Settings.DiscoveryPrefix, e.component, b.node(), e.object)
		current[topic] = true
		payload, _ := json.Marshal(e.config)
		if b.sentCfg[topic] != string(payload) {
			if err := c.Publish(topic, payload, true); err != nil {
				return err
			}
			b.sentCfg[topic] = string(payload)
		}
	}
	var keep []string
	changed := false
	for _, topic := range b.st.Published {
		if current[topic] {
			keep = append(keep, topic)
			continue
		}
		if err := c.Publish(topic, nil, true); err != nil { // an empty retained config removes it
			return err
		}
		delete(b.sentCfg, topic)
		changed = true
	}
	for topic := range current {
		if !slices.Contains(keep, topic) {
			keep = append(keep, topic)
			changed = true
		}
	}
	if changed {
		sort.Strings(keep)
		b.st.Published = keep
		if err := b.saveState(); err != nil {
			b.log().Warn("homeassistant: saving the state failed", "error", err)
		}
	}
	for _, e := range ents {
		topics := make([]string, 0, len(e.values))
		for t := range e.values {
			topics = append(topics, t)
		}
		sort.Strings(topics)
		for _, t := range topics {
			if v := e.values[t]; b.sentVal[t] != v {
				if err := c.Publish(t, []byte(v), true); err != nil {
					return err
				}
				b.sentVal[t] = v
			}
		}
	}
	return nil
}

// collect builds the entities from goblind. A section whose call fails for
// another reason than a refused capability keeps its last entities, so a
// hiccup does not remove them from Home Assistant.
func (b *Bridge) collect(ctx context.Context) []entity {
	if b.sections == nil {
		b.sections = map[string][]entity{}
	}
	section := func(name, capability string, build func() ([]entity, error)) {
		if !b.can(capability) {
			delete(b.sections, name)
			return
		}
		ents, err := build()
		switch {
		case err == nil:
			b.sections[name] = ents
		case Denied(err):
			delete(b.sections, name)
		case ctx.Err() != nil: // stopping
		default:
			b.log().Warn("homeassistant: reading from goblind failed", "what", name, "error", err)
		}
	}
	section("wan", "stats:read", func() ([]entity, error) { return b.wanEntities(ctx) })
	section("gateways", "stats:read", func() ([]entity, error) { return b.gatewayEntities(ctx) })
	section("system", "stats:read", func() ([]entity, error) { return b.systemEntities(ctx) })
	if len(b.Settings.Devices) > 0 {
		section("presence", "devices:read", func() ([]entity, error) { return b.presenceEntities(ctx) })
	} else {
		delete(b.sections, "presence")
	}
	section("policies", "policies:toggle", func() ([]entity, error) { return b.policyEntities(ctx) })
	var out []entity
	for _, name := range []string{"wan", "gateways", "system", "presence", "policies"} {
		out = append(out, b.sections[name]...)
	}
	return out
}

var objectRe = regexp.MustCompile(`[^a-z0-9]+`)

func objectID(parts ...string) string {
	return strings.Trim(objectRe.ReplaceAllString(strings.ToLower(strings.Join(parts, "_")), "_"), "_")
}

// base is the config every entity shares: its ids, the box as the device,
// and the availability topic (the last will marks it offline).
func (b *Bridge) base(object, name string) map[string]any {
	return map[string]any{
		"name":               name,
		"unique_id":          b.node() + "_" + object,
		"availability_topic": b.availability(),
		"device": map[string]any{
			"identifiers":  []string{b.node()},
			"name":         b.Settings.DeviceName,
			"manufacturer": "Network Goblin Labs",
			"model":        "Goblin Gate",
			"sw_version":   b.Version,
		},
		"origin": map[string]any{"name": "Goblin Gate", "sw_version": b.Version, "support_url": "https://github.com/MostlyWithinSpec/Goblin-Gate"},
	}
}

func onOff(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

func (b *Bridge) wanEntities(ctx context.Context) ([]entity, error) {
	wans, err := b.API.WANs(ctx)
	if err != nil {
		return nil, err
	}
	var out []entity
	for _, w := range wans {
		obj := objectID("wan", w.Name)
		state, attrs := b.topic("wan", obj[4:], "state"), b.topic("wan", obj[4:], "attributes")
		cfg := b.base(obj, "WAN "+w.Name)
		cfg["device_class"] = "connectivity"
		cfg["state_topic"] = state
		cfg["json_attributes_topic"] = attrs
		a, _ := json.Marshal(map[string]any{"device": w.Device, "addresses": w.Addresses, "gateway": w.Gateway})
		out = append(out, entity{component: "binary_sensor", object: obj, config: cfg, values: map[string]string{state: onOff(w.Up), attrs: string(a)}})

		addrObj := obj + "_ipv4"
		addrTopic := b.topic("wan", obj[4:], "ipv4")
		acfg := b.base(addrObj, "WAN "+w.Name+" IPv4 address")
		acfg["state_topic"] = addrTopic
		acfg["icon"] = "mdi:ip-network"
		addr := "None" // unknown in Home Assistant
		for _, a := range w.Addresses {
			if p, err := netip.ParsePrefix(a); err == nil && p.Addr().Is4() {
				addr = p.Addr().String()
				break
			}
		}
		out = append(out, entity{component: "sensor", object: addrObj, config: acfg, values: map[string]string{addrTopic: addr}})
	}
	return out, nil
}

func (b *Bridge) gatewayEntities(ctx context.Context) ([]entity, error) {
	gws, err := b.API.Gateways(ctx)
	if err != nil {
		return nil, err
	}
	var out []entity
	for _, g := range gws {
		obj := objectID("gateway", g.Name)
		key := obj[len("gateway_"):]
		state := b.topic("gateway", key, "state")
		cfg := b.base(obj, "Gateway "+g.Name)
		cfg["device_class"] = "connectivity"
		cfg["state_topic"] = state
		v := "None"
		switch g.State {
		case "up":
			v = "ON"
		case "down":
			v = "OFF"
		}
		out = append(out, entity{component: "binary_sensor", object: obj, config: cfg, values: map[string]string{state: v}})
		for _, m := range []struct {
			suffix, name, unit string
			value              int
		}{{"latency", "latency", "ms", g.LatencyMs}, {"loss", "packet loss", "%", g.LossPercent}} {
			t := b.topic("gateway", key, m.suffix)
			c := b.base(obj+"_"+m.suffix, "Gateway "+g.Name+" "+m.name)
			c["state_topic"] = t
			c["unit_of_measurement"] = m.unit
			c["state_class"] = "measurement"
			if m.suffix == "latency" {
				c["device_class"] = "duration"
			}
			val := strconv.Itoa(m.value)
			if g.State == "unknown" {
				val = "None"
			}
			out = append(out, entity{component: "sensor", object: obj + "_" + m.suffix, config: c, values: map[string]string{t: val}})
		}
	}
	return out, nil
}

func (b *Bridge) systemEntities(ctx context.Context) ([]entity, error) {
	s, err := b.API.System(ctx)
	if err != nil {
		return nil, err
	}
	mem := 0.0
	if s.MemTotalBytes > 0 {
		mem = float64(s.MemUsedBytes) * 100 / float64(s.MemTotalBytes)
	}
	// boot time rather than uptime: it only changes when the box restarts
	boot := b.now().Add(-time.Duration(s.UptimeSeconds) * time.Second).UTC().Truncate(time.Minute).Format(time.RFC3339)
	var out []entity
	for _, m := range []struct {
		object, name, unit, class, value string
	}{
		{"cpu", "CPU", "%", "", strconv.FormatFloat(math.Round(s.CPUPercent*10)/10, 'f', -1, 64)},
		{"memory", "Memory used", "%", "", strconv.FormatFloat(math.Round(mem*10)/10, 'f', -1, 64)},
		{"sessions", "Sessions", "", "", strconv.FormatInt(s.Sessions, 10)},
		{"boot_time", "Boot time", "", "timestamp", boot},
	} {
		t := b.topic("system", m.object)
		c := b.base(m.object, m.name)
		c["state_topic"] = t
		if m.unit != "" {
			c["unit_of_measurement"] = m.unit
		}
		if m.class != "" {
			c["device_class"] = m.class
			c["entity_category"] = "diagnostic"
		} else {
			c["state_class"] = "measurement"
		}
		out = append(out, entity{component: "sensor", object: m.object, config: c, values: map[string]string{t: m.value}})
	}
	return out, nil
}

func (b *Bridge) presenceEntities(ctx context.Context) ([]entity, error) {
	devs, err := b.API.Devices(ctx)
	if err != nil {
		return nil, err
	}
	byMAC := map[string]Device{}
	for _, d := range devs {
		if mac, err := normalizeMAC(d.MAC); err == nil {
			byMAC[mac] = d
		}
	}
	var out []entity
	for _, mac := range b.Settings.Devices {
		key := strings.ReplaceAll(mac, ":", "")
		obj := "presence_" + key
		d, known := byMAC[mac]
		name := mac
		switch {
		case d.Name != "":
			name = d.Name
		case d.Hostname != "":
			name = d.Hostname
		}
		state, attrs := b.topic("presence", key, "state"), b.topic("presence", key, "attributes")
		cfg := b.base(obj, name)
		// a phone is not part of the box: no device, so Home Assistant names
		// the tracker after the phone alone
		delete(cfg, "device")
		cfg["state_topic"] = state
		cfg["json_attributes_topic"] = attrs
		cfg["payload_home"] = "home"
		cfg["payload_not_home"] = "not_home"
		cfg["source_type"] = "router"
		home := known && !d.LastSeen.IsZero() && b.now().Sub(d.LastSeen) <= b.Settings.Away
		v := "not_home"
		if home {
			v = "home"
		}
		a := map[string]any{"mac": mac}
		if known {
			a["interface"] = d.Interface
			a["last_seen"] = d.LastSeen.UTC().Format(time.RFC3339)
			if len(d.IPs) > 0 {
				a["ip"] = d.IPs[0]
			}
		}
		ab, _ := json.Marshal(a)
		out = append(out, entity{component: "device_tracker", object: obj, config: cfg, values: map[string]string{state: v, attrs: string(ab)}})
	}
	return out, nil
}

func (b *Bridge) policyEntities(ctx context.Context) ([]entity, error) {
	pols, err := b.API.Policies(ctx)
	if err != nil {
		return nil, err
	}
	var out []entity
	for _, p := range pols {
		id := strconv.FormatUint(uint64(p.ID), 10)
		obj := "policy_" + id
		state := b.topic("policy", id, "state")
		cfg := b.base(obj, "Policy "+p.Name)
		cfg["state_topic"] = state
		cfg["command_topic"] = b.topic("policy", id, "set")
		cfg["payload_on"], cfg["payload_off"] = "ON", "OFF"
		cfg["icon"] = "mdi:shield-sync"
		out = append(out, entity{component: "switch", object: obj, config: cfg, values: map[string]string{state: onOff(p.Enabled)}})
	}
	return out, nil
}

func (b *Bridge) statePath() string { return filepath.Join(b.StateDir, "state.json") }

func (b *Bridge) loadState() error {
	data, err := os.ReadFile(b.statePath())
	if err == nil {
		if err := json.Unmarshal(data, &b.st); err != nil {
			b.log().Warn("homeassistant: state file unreadable, starting over", "error", err)
			b.st = persisted{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if b.st.BoxID == "" {
		id := make([]byte, 4)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		b.st.BoxID = hex.EncodeToString(id)
		return b.saveState()
	}
	return nil
}

func (b *Bridge) saveState() error {
	data, _ := json.MarshalIndent(b.st, "", "  ")
	tmp := b.statePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.statePath())
}
