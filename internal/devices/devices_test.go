package devices

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var now = time.Unix(1_760_000_000, 0)

const leaseHeader = "address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context\n"

func leaseLine(ip, mac, host string, expire int64) string {
	return ip + "," + mac + ",,3600," + strings.TrimSpace(strings.TrimPrefix(itoa(expire), " ")) + ",1,0,0," + host + ",0,\n"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func writeLease(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "leases.csv")
	data := leaseHeader + strings.Join(lines, "")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func neighJSON(entries ...map[string]string) string {
	if len(entries) == 0 {
		return "[]"
	}
	var parts []string
	for _, e := range entries {
		parts = append(parts, "{"+joinKV(e)+"}")
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func joinKV(m map[string]string) string {
	keys := []string{"dst", "dev", "lladdr", "state"}
	var parts []string
	for _, k := range keys {
		if v, ok := m[k]; ok {
			parts = append(parts, "\""+k+"\":\""+v+"\"")
		}
	}
	return strings.Join(parts, ",")
}

func TestRefreshMergesLeaseAndNeighbour(t *testing.T) {
	dir := t.TempDir()
	leasePath := writeLease(t, dir,
		leaseLine("192.168.1.10", "aa:bb:cc:dd:ee:01", "mikes-laptop", now.Unix()+3600),
	)
	s := &Store{
		Path:      filepath.Join(dir, "devices.json"),
		LeaseFile: leasePath,
		Now:       func() time.Time { return now },
		Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(neighJSON(
				map[string]string{"dst": "192.168.1.10", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:01", "state": "REACHABLE"},
				map[string]string{"dst": "192.168.1.20", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:02", "state": "STALE"},
			)), nil
		},
		Interfaces: func() map[string]string { return map[string]string{"eth0": "lan"} },
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(list))
	}
	// Sorted by LastSeen (both same), so order is arbitrary; find by MAC.
	var laptop *Device
	for i := range list {
		if list[i].MAC == "aabbccddee01" {
			laptop = &list[i]
		}
	}
	if laptop == nil {
		t.Fatal("laptop not found")
	}
	if laptop.Hostname != "mikes-laptop" {
		t.Fatalf("hostname = %q", laptop.Hostname)
	}
	if len(laptop.IPs) != 1 || laptop.IPs[0] != "192.168.1.10" {
		t.Fatalf("ips = %v", laptop.IPs)
	}
	if laptop.Interface != "lan" {
		t.Fatalf("interface = %q", laptop.Interface)
	}
	if !laptop.FirstSeen.Equal(now) || !laptop.LastSeen.Equal(now) {
		t.Fatalf("first/last: %v / %v", laptop.FirstSeen, laptop.LastSeen)
	}
}

func TestRefreshSkipsBadNeighbours(t *testing.T) {
	dir := t.TempDir()
	s := &Store{
		Path: filepath.Join(dir, "devices.json"),
		Now:  func() time.Time { return now },
		Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(neighJSON(
				map[string]string{"dst": "10.0.0.1", "dev": "eth0", "lladdr": "", "state": "REACHABLE"},                   // no MAC
				map[string]string{"dst": "10.0.0.2", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:03", "state": "FAILED"},     // FAILED
				map[string]string{"dst": "10.0.0.3", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:04", "state": "INCOMPLETE"}, // INCOMPLETE
				map[string]string{"dst": "10.0.0.4", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:05", "state": "REACHABLE"},  // good
			)), nil
		},
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 device, got %d: %v", len(list), list)
	}
	if list[0].MAC != "aabbccddee05" {
		t.Fatalf("mac = %q", list[0].MAC)
	}
}

func TestNoNotifyOnFirstPoll(t *testing.T) {
	dir := t.TempDir()
	var events []string
	s := &Store{
		Path:   filepath.Join(dir, "devices.json"),
		Now:    func() time.Time { return now },
		Notify: func(kind, key, title, message string) { events = append(events, kind+":"+key) },
		Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(neighJSON(
				map[string]string{"dst": "10.0.0.1", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:10", "state": "REACHABLE"},
			)), nil
		},
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("first poll emitted events: %v", events)
	}

	// Second poll with a new MAC should notify.
	s.Runner = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(neighJSON(
			map[string]string{"dst": "10.0.0.1", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:10", "state": "REACHABLE"},
			map[string]string{"dst": "10.0.0.2", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:11", "state": "REACHABLE"},
		)), nil
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "new_device:aabbccddee11" {
		t.Fatalf("second poll events: %v", events)
	}
}

func TestVendorLookup(t *testing.T) {
	tests := []struct {
		mac    string
		vendor string
		rand   bool
	}{
		{"005056aabbcc", "VMware, Inc.", false},
		{"b827eb123456", "Raspberry Pi Foundation", false},
		{"a48d3bd7be0b", "Vizio, Inc", false},
		{"c8aabbccddeeff", "Unknown", false},
		// Locally administered: second hex digit 2, 6, a, e
		{"02aabbccddeeff", "Private (randomized MAC)", true},
		{"06aabbccddeeff", "Private (randomized MAC)", true},
		{"0aaabbccddeeff", "Private (randomized MAC)", true},
		{"0eaabbccddeeff", "Private (randomized MAC)", true},
	}
	for _, tc := range tests {
		v, r := vendorOf(tc.mac)
		if v != tc.vendor || r != tc.rand {
			t.Errorf("vendorOf(%s) = (%q, %v), want (%q, %v)", tc.mac, v, r, tc.vendor, tc.rand)
		}
	}
}

func TestFirstSeenLastSeenUpdate(t *testing.T) {
	dir := t.TempDir()
	t1 := now
	t2 := now.Add(120 * time.Second)
	cur := t1
	s := &Store{
		Path: filepath.Join(dir, "devices.json"),
		Now:  func() time.Time { return cur },
		Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(neighJSON(
				map[string]string{"dst": "10.0.0.1", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:20", "state": "REACHABLE"},
			)), nil
		},
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if !list[0].FirstSeen.Equal(t1) || !list[0].LastSeen.Equal(t1) {
		t.Fatalf("first refresh: %v / %v", list[0].FirstSeen, list[0].LastSeen)
	}

	cur = t2
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if !list[0].FirstSeen.Equal(t1) {
		t.Fatalf("first_seen changed: %v", list[0].FirstSeen)
	}
	if !list[0].LastSeen.Equal(t2) {
		t.Fatalf("last_seen = %v, want %v", list[0].LastSeen, t2)
	}
}

func TestMaxDevicesCap(t *testing.T) {
	dir := t.TempDir()
	s := &Store{
		Path: filepath.Join(dir, "devices.json"),
		Now:  func() time.Time { return now },
	}
	// Insert MaxDevices+10 devices directly (bypassing Refresh for speed).
	s.loadLocked()
	for i := 0; i < MaxDevices+10; i++ {
		mac := strings.Repeat("ab", 3) + itoa6(i)
		s.devices[mac] = &Device{MAC: mac, FirstSeen: now, LastSeen: now.Add(time.Duration(i) * time.Second)}
	}
	// Trigger cap logic via a Refresh with no new data.
	s.Runner = func(_ context.Context, _ string, _ ...string) ([]byte, error) { return []byte("[]"), nil }
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != MaxDevices {
		t.Fatalf("len = %d, want %d", len(list), MaxDevices)
	}
	// The oldest 10 should have been dropped; the newest should remain.
	oldest := list[MaxDevices-1] // last in sorted-by-lastSeen-descending order
	if oldest.LastSeen.Before(now.Add(time.Duration(10) * time.Second)) {
		t.Fatalf("oldest kept = %v, want >= %v", oldest.LastSeen, now.Add(10*time.Second))
	}
}

func itoa6(n int) string {
	if n < 10 {
		return "00000" + string(rune('0'+n))
	}
	s := ""
	for i := 5; i >= 0; i-- {
		digits := "0123456789abcdef"
		s = string(digits[n%16]) + s
		n /= 16
	}
	return s
}

func TestIPsCappedAtMax(t *testing.T) {
	dir := t.TempDir()
	t1 := now
	cur := &t1
	s := &Store{
		Path: filepath.Join(dir, "devices.json"),
		Now:  func() time.Time { return *cur },
	}
	mac := "aabbccddee30"
	for i := 0; i < MaxIPs+3; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		s.Runner = func(ip string, mac string) func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return []byte(neighJSON(map[string]string{"dst": ip, "dev": "eth0", "lladdr": mac, "state": "REACHABLE"})), nil
			}
		}(ip, mac)
		if err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.List()
	if len(list[0].IPs) > MaxIPs {
		t.Fatalf("ips len = %d, want <= %d", len(list[0].IPs), MaxIPs)
	}
}

func TestSetNameAndRemove(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Path: filepath.Join(dir, "devices.json"), Now: func() time.Time { return now }}
	s.loadLocked()
	mac := "aabbccddee40"
	s.devices[mac] = &Device{MAC: mac, FirstSeen: now, LastSeen: now}

	d, err := s.SetName(mac, "Office Printer")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Office Printer" {
		t.Fatalf("name = %q", d.Name)
	}

	if err := s.Remove(mac); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %v", list)
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devices.json")
	s1 := &Store{Path: p, Now: func() time.Time { return now }}
	s1.loadLocked()
	s1.devices["aabbccddee50"] = &Device{MAC: "aabbccddee50", FirstSeen: now, LastSeen: now, Name: "test"}
	if err := s1.saveLocked(); err != nil {
		t.Fatal(err)
	}

	s2 := &Store{Path: p}
	list, _ := s2.List()
	if len(list) != 1 || list[0].Name != "test" {
		t.Fatalf("reloaded: %v", list)
	}
}

func TestLeaseFileMissing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{
		Path:      filepath.Join(dir, "devices.json"),
		LeaseFile: filepath.Join(dir, "nonexistent.csv"),
		Now:       func() time.Time { return now },
		Runner:    func(_ context.Context, _ string, _ ...string) ([]byte, error) { return []byte("[]"), nil },
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("missing lease file should not error: %v", err)
	}
}

func TestLeaseParsing(t *testing.T) {
	dir := t.TempDir()
	future := now.Unix() + 3600
	past := now.Unix() - 100
	leasePath := writeLease(t, dir,
		"192.168.1.5,aa:bb:cc:dd:ee:60,,3600,"+itoa(future)+",1,0,0,laptop,0,\n",
		"192.168.1.6,aa:bb:cc:dd:ee:61,,3600,"+itoa(past)+",1,0,0,old,0,\n",        // expired
		"192.168.1.7,aa:bb:cc:dd:ee:62,,3600,"+itoa(future)+",1,0,0,declined,1,\n", // declined
	)
	s := &Store{
		Path:      filepath.Join(dir, "devices.json"),
		LeaseFile: leasePath,
		Now:       func() time.Time { return now },
		Runner:    func(_ context.Context, _ string, _ ...string) ([]byte, error) { return []byte("[]"), nil },
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 active lease, got %d", len(list))
	}
	if list[0].Hostname != "laptop" || list[0].MAC != "aabbccddee60" {
		t.Fatalf("device: %+v", list[0])
	}
}

func TestInterfacesNil(t *testing.T) {
	dir := t.TempDir()
	s := &Store{
		Path: filepath.Join(dir, "devices.json"),
		Now:  func() time.Time { return now },
		Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(neighJSON(
				map[string]string{"dst": "10.0.0.1", "dev": "eth0", "lladdr": "aa:bb:cc:dd:ee:70", "state": "REACHABLE"},
			)), nil
		},
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if list[0].Interface != "eth0" {
		t.Fatalf("interface = %q, want kernel name when Interfaces is nil", list[0].Interface)
	}
}

var _ = cmp.Diff // keep import if unused in some builds
