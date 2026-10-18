// Package diag runs reachability and name-resolution checks from the box:
// ping, traceroute and DNS lookups (System → Diagnostics).
package diag

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limits on what a caller may ask for.
const (
	MaxPingCount = 10
	MaxHops      = 30
	// Timeout bounds every diagnostic.
	Timeout = 60 * time.Second
)

// Runner runs a program and returns its standard output (tests replace it).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs real programs. A non-zero exit with output is not an
// error here: ping exits 1 when no reply came, which is a result.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(out) > 0 {
		err = nil
	}
	return out, err
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

// ValidHost accepts an IP address or a DNS name; nothing else reaches a
// command line (a leading "-" would be read as an option).
func ValidHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

var devRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)

// PingResult is the answer of Ping.
type PingResult struct {
	Target      string   `json:"target"`
	Sent        int      `json:"sent"`
	Received    int      `json:"received"`
	LossPercent float64  `json:"loss_percent"`
	RTTMinMs    float64  `json:"rtt_min_ms"`
	RTTAvgMs    float64  `json:"rtt_avg_ms"`
	RTTMaxMs    float64  `json:"rtt_max_ms"`
	Lines       []string `json:"lines"`
}

var (
	pingSummary = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received.*?([\d.]+)% packet loss`)
	pingRTT     = regexp.MustCompile(`= ([\d.]+)/([\d.]+)/([\d.]+)/`)
)

// Ping sends count echo requests to host, from device dev when given.
func Ping(ctx context.Context, run Runner, host string, count int, dev string) (PingResult, error) {
	if !ValidHost(host) {
		return PingResult{}, fmt.Errorf("host must be an IP address or a DNS name")
	}
	if count == 0 {
		count = 4
	}
	if count < 1 || count > MaxPingCount {
		return PingResult{}, fmt.Errorf("count must be 1-%d", MaxPingCount)
	}
	args := []string{"-n", "-c", strconv.Itoa(count), "-i", "0.5", "-W", "2", "-w", strconv.Itoa(count + 5)}
	if dev != "" {
		if !devRe.MatchString(dev) {
			return PingResult{}, fmt.Errorf("bad device %q", dev)
		}
		args = append(args, "-I", dev)
	}
	out, err := run(ctx, "ping", append(args, host)...)
	if err != nil {
		return PingResult{}, fmt.Errorf("ping: %w", err)
	}
	return parsePing(host, string(out)), nil
}

func parsePing(host, out string) PingResult {
	r := PingResult{Target: host, Lines: lines(out)}
	if m := pingSummary.FindStringSubmatch(out); m != nil {
		r.Sent, _ = strconv.Atoi(m[1])
		r.Received, _ = strconv.Atoi(m[2])
		r.LossPercent, _ = strconv.ParseFloat(m[3], 64)
	}
	if m := pingRTT.FindStringSubmatch(out); m != nil {
		r.RTTMinMs, _ = strconv.ParseFloat(m[1], 64)
		r.RTTAvgMs, _ = strconv.ParseFloat(m[2], 64)
		r.RTTMaxMs, _ = strconv.ParseFloat(m[3], 64)
	}
	return r
}

func lines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Hop is one line of a traceroute.
type Hop struct {
	Hop     int       `json:"hop"`
	Address string    `json:"address"` // "*" when nothing answered
	RTTMs   []float64 `json:"rtt_ms"`
}

// TraceResult is the answer of Traceroute.
type TraceResult struct {
	Target string `json:"target"`
	Hops   []Hop  `json:"hops"`
}

// Traceroute lists the routers on the way to host.
func Traceroute(ctx context.Context, run Runner, host string, maxHops int) (TraceResult, error) {
	if !ValidHost(host) {
		return TraceResult{}, fmt.Errorf("host must be an IP address or a DNS name")
	}
	if maxHops == 0 {
		maxHops = 20
	}
	if maxHops < 1 || maxHops > MaxHops {
		return TraceResult{}, fmt.Errorf("max_hops must be 1-%d", MaxHops)
	}
	args := []string{"-n", "-q", "3", "-w", "2", "-m", strconv.Itoa(maxHops)}
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
		args = append(args, "-6")
	}
	out, err := run(ctx, "traceroute", append(args, host)...)
	if err != nil {
		return TraceResult{}, fmt.Errorf("traceroute: %w", err)
	}
	return TraceResult{Target: host, Hops: parseTrace(string(out))}, nil
}

// parseTrace reads `traceroute -n` lines such as
// " 2  10.0.0.1  1.204 ms  10.0.0.2  1.388 ms *".
func parseTrace(out string) []Hop {
	hops := []Hop{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue // the "traceroute to ..." header
		}
		h := Hop{Hop: n, Address: "*", RTTMs: []float64{}}
		for i := 1; i < len(f); i++ {
			if _, err := netip.ParseAddr(f[i]); err == nil {
				if h.Address == "*" {
					h.Address = f[i]
				}
				continue
			}
			if i+1 < len(f) && f[i+1] == "ms" {
				if v, err := strconv.ParseFloat(f[i], 64); err == nil {
					h.RTTMs = append(h.RTTMs, v)
				}
				i++
			}
		}
		hops = append(hops, h)
	}
	return hops
}
