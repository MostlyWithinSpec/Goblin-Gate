package dhcpdns

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1_760_000_000, 0)

// Kea 2.2 memfile format (Debian 12).
const header = "address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context\n"

func line(ip, host string, expire int64, state string) string {
	return ip + ",aa:bb:cc:dd:ee:ff,,3600," + strconv.FormatInt(expire, 10) + ",1,0,0," + host + "," + state + ",\n"
}

func TestParseLeases(t *testing.T) {
	future, past := now.Unix()+600, now.Unix()-600
	csv := header +
		line("192.168.1.100", "Mikes-Laptop.local", future, "0") +
		line("192.168.1.101", "old-phone", past, "0") + // expired
		line("192.168.1.102", "declined", future, "1") + // declined
		line("192.168.1.103", "", future, "0") + // no name
		line("192.168.1.104", "tv", future, "0") +
		line("192.168.1.104", "living-room-tv", future, "0") + // later line wins
		"192.168.1.105,aa:bb" // half-written last line
	got, err := ParseLeases(strings.NewReader(csv), now)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, l := range got {
		s = append(s, l.IP.String()+"="+l.Name)
	}
	if strings.Join(s, " ") != "192.168.1.100=mikes-laptop 192.168.1.104=living-room-tv" {
		t.Fatalf("%v", s)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"Mikes-Laptop.local": "mikes-laptop", "Kim's iPhone": "kims-iphone", "--x--": "x",
		"__": "", "日本": "", strings.Repeat("a", 70): strings.Repeat("a", 63), "printer_2": "printer-2",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases.csv")
	write := func(s string) { os.WriteFile(path, []byte(header+s), 0o644) }
	future := now.Unix() + 600
	write(line("192.168.1.100", "laptop", future, "0") + line("192.168.1.10", "printer", future, "0"))

	var calls []string
	s := &Syncer{
		LeaseFile: path,
		Now:       func() time.Time { return now },
		Settings: func() (Settings, error) {
			return Settings{Enabled: true, Domain: "home.arpa", Taken: map[string]bool{"printer.home.arpa": true}}, nil
		},
		Control: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			return nil
		},
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"local_data laptop.home.arpa. 300 IN A 192.168.1.100", "local_data 100.1.168.192.in-addr.arpa. 300 IN PTR laptop.home.arpa."}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("first sync (printer is a static lease, skipped):\n%s", strings.Join(calls, "\n"))
	}

	calls = nil
	if err := s.Sync(context.Background()); err != nil || len(calls) != 0 {
		t.Fatalf("no change, yet: %v %v", calls, err)
	}

	// laptop leaves, a phone arrives
	write(line("192.168.1.101", "phone", future, "0"))
	calls = nil
	s.Sync(context.Background())
	got := strings.Join(calls, "\n")
	for _, w := range []string{"local_data_remove laptop.home.arpa.", "local_data_remove 100.1.168.192.in-addr.arpa.", "local_data phone.home.arpa. 300 IN A 192.168.1.101"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}

	// after a commit restarted Unbound, everything is re-added
	calls = nil
	s.Resync(context.Background())
	if !strings.Contains(strings.Join(calls, "\n"), "local_data phone.home.arpa.") {
		t.Fatalf("resync: %v", calls)
	}

	// turned off: names are removed
	s.Settings = func() (Settings, error) { return Settings{Enabled: false, Domain: "home.arpa"}, nil }
	calls = nil
	s.Sync(context.Background())
	if !strings.Contains(strings.Join(calls, "\n"), "local_data_remove phone.home.arpa.") {
		t.Fatalf("disable: %v", calls)
	}
}
