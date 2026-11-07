// Package nicinfo reads what usually explains a slow network card: its
// driver, link speed and duplex, offload settings and queue counts (sysfs
// and ethtool), and turns them into hints.
package nicinfo

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Info describes one network device.
type Info struct {
	Device   string `json:"device"`
	Driver   string `json:"driver,omitempty"`
	Link     bool   `json:"link"`             // carrier
	SpeedMbs int    `json:"speed_mbps"`       // 0 = unknown (no link, virtual device)
	Duplex   string `json:"duplex,omitempty"` // full | half
	MTU      int    `json:"mtu,omitempty"`
	RXQueues int    `json:"rx_queues"`
	TXQueues int    `json:"tx_queues"`
	// Supported link speeds in Mbit/s (ethtool "Supported link modes").
	Supported []int `json:"supported_mbps,omitempty"`
	// Offloads by ethtool -k name ("generic-receive-offload", ...).
	Offloads map[string]Feature `json:"offloads,omitempty"`
	Hints    []Hint             `json:"hints"`
}

// Feature is one offload: on or off, and fixed when the driver does not let
// it change.
type Feature struct {
	On    bool `json:"on"`
	Fixed bool `json:"fixed,omitempty"`
}

// Hint is a likely cause of slow throughput. Code is stable (the web UI
// translates by it); Message is English.
type Hint struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Runner runs a command and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec runs real commands.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Reader reads devices below SysRoot ("/" in production).
type Reader struct {
	SysRoot string
	Run     Runner
}

// Read describes dev. Missing pieces stay empty: virtual devices have no
// driver or speed, and a box without ethtool still gets the sysfs facts.
func (r Reader) Read(ctx context.Context, dev string) Info {
	root := r.SysRoot
	if root == "" {
		root = "/"
	}
	base := filepath.Join(root, "sys", "class", "net", dev)
	in := Info{Device: dev}
	if l, err := os.Readlink(filepath.Join(base, "device", "driver")); err == nil {
		in.Driver = filepath.Base(l)
	}
	in.Link = readString(filepath.Join(base, "carrier")) == "1"
	in.MTU = readInt(filepath.Join(base, "mtu"))
	if s := readInt(filepath.Join(base, "speed")); s > 0 && s < 1_000_000 {
		in.SpeedMbs = s
	}
	in.Duplex = readString(filepath.Join(base, "duplex"))
	if in.Duplex == "unknown" {
		in.Duplex = ""
	}
	if ents, err := os.ReadDir(filepath.Join(base, "queues")); err == nil {
		for _, e := range ents {
			switch {
			case strings.HasPrefix(e.Name(), "rx-"):
				in.RXQueues++
			case strings.HasPrefix(e.Name(), "tx-"):
				in.TXQueues++
			}
		}
	}
	if r.Run != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if out, err := r.Run(ctx, "ethtool", dev); err == nil {
			speed, duplex, supported := ParseLink(string(out))
			if in.SpeedMbs == 0 {
				in.SpeedMbs = speed
			}
			if in.Duplex == "" {
				in.Duplex = duplex
			}
			in.Supported = supported
		}
		if out, err := r.Run(ctx, "ethtool", "-k", dev); err == nil {
			in.Offloads = ParseFeatures(string(out))
		}
	}
	in.Hints = Hints(in)
	return in
}

func readString(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(p string) int {
	n, _ := strconv.Atoi(readString(p))
	return n
}

var featureRe = regexp.MustCompile(`^\s*([a-z0-9-]+): (on|off)( \[fixed\])?`)

// ParseFeatures reads `ethtool -k <dev>`. Sub-features (indented twice)
// are left out, as are the "[requested ...]" annotations.
func ParseFeatures(out string) map[string]Feature {
	m := map[string]Feature{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "  ") {
			continue // tx-checksum-ipv4 and other parts of a feature
		}
		if f := featureRe.FindStringSubmatch(line); f != nil {
			m[f[1]] = Feature{On: f[2] == "on", Fixed: f[3] != ""}
		}
	}
	return m
}

var (
	speedRe  = regexp.MustCompile(`^\s*Speed:\s*(\d+)Mb/s`)
	duplexRe = regexp.MustCompile(`^\s*Duplex:\s*(Full|Half)`)
	modeRe   = regexp.MustCompile(`(\d+)base[A-Za-z0-9]*/(Full|Half)`)
)

// ParseLink reads `ethtool <dev>`: the current speed (Mbit/s, 0 = unknown),
// duplex ("full"/"half") and the supported speeds, fastest last.
func ParseLink(out string) (speed int, duplex string, supported []int) {
	seen := map[int]bool{}
	inModes := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := speedRe.FindStringSubmatch(line); m != nil {
			speed, _ = strconv.Atoi(m[1])
		}
		if m := duplexRe.FindStringSubmatch(line); m != nil {
			duplex = strings.ToLower(m[1])
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Supported link modes:"):
			inModes = true
			trimmed = strings.TrimPrefix(trimmed, "Supported link modes:")
		case strings.Contains(trimmed, ":"):
			inModes = false
		}
		if inModes {
			for _, m := range modeRe.FindAllStringSubmatch(trimmed, -1) {
				if n, err := strconv.Atoi(m[1]); err == nil && !seen[n] {
					seen[n] = true
					supported = append(supported, n)
				}
			}
		}
	}
	sort.Ints(supported)
	return speed, duplex, supported
}

// slowDrivers are drivers known for limiting throughput, with why.
var slowDrivers = map[string]string{
	"r8152":   "a USB Realtek adapter: USB and its driver often cap it well below the link speed",
	"ax88179": "a USB ASIX adapter: USB and its driver often cap it well below the link speed",
	"e1000":   "an emulated Intel e1000 card: in a VM, the virtio (virtio_net) card is much faster",
	"8139too": "a Realtek 8139 card, which tops out at 100 Mbit/s",
}

// Offload names as ethtool -k prints them, for the hints.
const (
	featGRO = "generic-receive-offload"
	featGSO = "generic-segmentation-offload"
	featTSO = "tcp-segmentation-offload"
)

// Hints lists the likely causes of slow throughput on in.
func Hints(in Info) []Hint {
	out := []Hint{}
	if !in.Link && in.Driver != "" {
		out = append(out, Hint{"no_link", "No link: check the cable and the device on the other end."})
	}
	if in.Link && in.SpeedMbs > 0 && in.SpeedMbs < 1000 {
		if len(in.Supported) > 0 && in.Supported[len(in.Supported)-1] > in.SpeedMbs {
			out = append(out, Hint{"slow_link", "The link runs at " + strconv.Itoa(in.SpeedMbs) + " Mbit/s although the card can do " +
				strconv.Itoa(in.Supported[len(in.Supported)-1]) + ": often a damaged cable, a cable with only 4 wires, or a slow switch port."})
		} else {
			out = append(out, Hint{"slow_link", "The link runs at " + strconv.Itoa(in.SpeedMbs) + " Mbit/s."})
		}
	}
	if in.Link && in.Duplex == "half" {
		out = append(out, Hint{"half_duplex", "The link is half duplex, which slows it down a lot: usually a speed/duplex mismatch with the switch."})
	}
	var off []string
	for _, f := range []string{featGRO, featGSO, featTSO} {
		if ft, ok := in.Offloads[f]; ok && !ft.On && !ft.Fixed {
			off = append(off, f)
		}
	}
	if len(off) > 0 {
		out = append(out, Hint{"offloads_off", "Offloads are off (" + strings.Join(off, ", ") + "): the processor does that work for every packet."})
	}
	if why, ok := slowDrivers[in.Driver]; ok {
		out = append(out, Hint{"slow_driver", "This is " + why + "."})
	}
	if in.RXQueues == 1 && in.SpeedMbs >= 2500 {
		out = append(out, Hint{"single_queue", "One receive queue on a fast link: all its traffic is handled by one processor core."})
	}
	return out
}
