// Package upnp reads miniupnpd's active port mappings (task 059) from its
// lease file, for the API and the event log.
package upnp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mapping is one port mapping a LAN client asked for.
type Mapping struct {
	Protocol     string    `json:"protocol"` // tcp | udp
	ExternalPort int       `json:"external_port"`
	InternalIP   string    `json:"internal_ip"`
	InternalPort int       `json:"internal_port"`
	Description  string    `json:"description,omitempty"`
	Expires      time.Time `json:"expires,omitzero"` // zero = until removed
}

// Key identifies a mapping (miniupnpd allows one per protocol and external port).
func (m Mapping) Key() string { return fmt.Sprintf("%s/%d", m.Protocol, m.ExternalPort) }

// Parse reads a miniupnpd lease file: one mapping per line,
// "PROTO:EXT_PORT:INT_IP:INT_PORT:EXPIRY:DESCRIPTION" (EXPIRY is a unix
// time, 0 = no expiry; the description may contain ':'). Malformed lines
// are skipped. Sorted by protocol and external port.
func Parse(data []byte) []Mapping {
	var out []Mapping
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.SplitN(strings.TrimSpace(sc.Text()), ":", 6)
		if len(f) < 5 {
			continue
		}
		ext, err1 := strconv.Atoi(f[1])
		in, err2 := strconv.Atoi(f[3])
		exp, err3 := strconv.ParseInt(f[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		m := Mapping{Protocol: strings.ToLower(f[0]), ExternalPort: ext, InternalIP: f[2], InternalPort: in}
		if len(f) == 6 {
			m.Description = f[5]
		}
		if exp > 0 {
			m.Expires = time.Unix(exp, 0).UTC()
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].ExternalPort < out[j].ExternalPort
	})
	return out
}

// Read returns the mappings in the lease file; a missing file means none.
func Read(path string) ([]Mapping, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b), nil
}

// Watch polls the lease file every interval and calls changed for every
// mapping added (added=true) or removed since the previous poll. The
// mappings present at start are not reported.
func Watch(ctx context.Context, path string, interval time.Duration, changed func(m Mapping, added bool)) {
	index := func() map[string]Mapping {
		ms, _ := Read(path)
		out := make(map[string]Mapping, len(ms))
		for _, m := range ms {
			out[m.Key()] = m
		}
		return out
	}
	prev := index()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cur := index()
		for k, m := range cur {
			if p, ok := prev[k]; !ok || p.InternalIP != m.InternalIP || p.InternalPort != m.InternalPort {
				changed(m, true)
			}
		}
		for k, m := range prev {
			if _, ok := cur[k]; !ok {
				changed(m, false)
			}
		}
		prev = cur
	}
}
