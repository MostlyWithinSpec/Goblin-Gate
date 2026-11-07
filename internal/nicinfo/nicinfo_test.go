package nicinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const ethtoolLink = `Settings for eth1:
	Supported ports: [ TP ]
	Supported link modes:   10baseT/Half 10baseT/Full
	                        100baseT/Half 100baseT/Full
	                        1000baseT/Full
	                        2500baseT/Full
	Supported pause frame use: Symmetric
	Supports auto-negotiation: Yes
	Advertised link modes:  10baseT/Half 10baseT/Full
	                        100baseT/Half 100baseT/Full
	Speed: 100Mb/s
	Duplex: Full
	Link detected: yes
`

const ethtoolFeatures = `Features for eth1:
rx-checksumming: on
tx-checksumming: on
	tx-checksum-ipv4: on
	tx-checksum-ip-generic: off [fixed]
scatter-gather: on
tcp-segmentation-offload: off
generic-segmentation-offload: on
generic-receive-offload: off
large-receive-offload: off [fixed]
rx-vlan-offload: on [requested off]
`

func TestParseLink(t *testing.T) {
	speed, duplex, sup := ParseLink(ethtoolLink)
	if speed != 100 || duplex != "full" || !reflect.DeepEqual(sup, []int{10, 100, 1000, 2500}) {
		t.Fatalf("%d %q %v", speed, duplex, sup)
	}
	if s, d, sup := ParseLink("Settings for wg0:\n\tLink detected: yes\n"); s != 0 || d != "" || sup != nil {
		t.Fatalf("virtual: %d %q %v", s, d, sup)
	}
}

func TestParseFeatures(t *testing.T) {
	f := ParseFeatures(ethtoolFeatures)
	want := map[string]Feature{
		"rx-checksumming": {On: true}, "tx-checksumming": {On: true}, "scatter-gather": {On: true},
		"tcp-segmentation-offload": {}, "generic-segmentation-offload": {On: true}, "generic-receive-offload": {},
		"large-receive-offload": {Fixed: true}, "rx-vlan-offload": {On: true},
	}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("%v", f)
	}
}

func TestReadAndHints(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "sys", "class", "net", "eth1")
	for _, d := range []string{"queues/rx-0", "queues/tx-0", "queues/tx-1"} {
		os.MkdirAll(filepath.Join(base, d), 0o755)
	}
	for name, v := range map[string]string{"carrier": "1", "mtu": "1500", "speed": "100", "duplex": "full"} {
		os.WriteFile(filepath.Join(base, name), []byte(v+"\n"), 0o644)
	}
	drv := filepath.Join(root, "drivers", "r8152")
	os.MkdirAll(drv, 0o755)
	os.MkdirAll(filepath.Join(base, "device"), 0o755)
	haveDriver := os.Symlink(drv, filepath.Join(base, "device", "driver")) == nil // needs privileges on Windows

	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "eth1":
			return []byte(ethtoolLink), nil
		case "-k eth1":
			return []byte(ethtoolFeatures), nil
		}
		return nil, errors.New("unexpected " + name)
	}
	in := Reader{SysRoot: root, Run: run}.Read(context.Background(), "eth1")
	if !in.Link || in.SpeedMbs != 100 || in.Duplex != "full" || in.MTU != 1500 || in.RXQueues != 1 || in.TXQueues != 2 {
		t.Fatalf("%+v", in)
	}
	codes := map[string]string{}
	for _, h := range in.Hints {
		codes[h.Code] = h.Message
	}
	if !strings.Contains(codes["slow_link"], "can do 2500") {
		t.Errorf("slow link hint: %v", codes)
	}
	if !strings.Contains(codes["offloads_off"], "generic-receive-offload") || !strings.Contains(codes["offloads_off"], "tcp-segmentation-offload") {
		t.Errorf("offload hint: %v", codes)
	}
	if haveDriver && (in.Driver != "r8152" || codes["slow_driver"] == "") {
		t.Errorf("driver %q, hints %v", in.Driver, codes)
	}

	// without ethtool: the sysfs facts only, and no error
	in = Reader{SysRoot: root, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not found") }}.Read(context.Background(), "eth1")
	if in.SpeedMbs != 100 || in.Offloads != nil {
		t.Fatalf("no ethtool: %+v", in)
	}
}

func TestHintsHalfDuplexAndNoLink(t *testing.T) {
	h := Hints(Info{Driver: "igc", Link: true, SpeedMbs: 1000, Duplex: "half"})
	if len(h) != 1 || h[0].Code != "half_duplex" {
		t.Fatalf("%v", h)
	}
	if h := Hints(Info{Driver: "igc"}); len(h) != 1 || h[0].Code != "no_link" {
		t.Fatalf("%v", h)
	}
	if h := Hints(Info{Device: "wg0"}); len(h) != 0 { // virtual: nothing to say
		t.Fatalf("%v", h)
	}
}
