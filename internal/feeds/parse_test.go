package feeds

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseFormats(t *testing.T) {
	for _, tc := range []struct {
		name, format, typ, body string
		want                    []string
	}{
		{"plain ip", "", "ip", "# comment\n; also\n1.2.3.4\n10.0.0.0/8 # inline\n10.1.2.3\nbogus\n2001:db8::1\n::ffff:5.6.7.8\n1.2.3.4\n",
			[]string{"1.2.3.4", "5.6.7.8", "10.0.0.0/8", "2001:db8::1"}},
		{"plain merge siblings", "plain", "ip", "192.0.2.0/25\n192.0.2.128/25\n198.51.100.7/32\n",
			[]string{"192.0.2.0/24", "198.51.100.7"}},
		{"plain domains and hosts file", "", "domain", bom + "Example.COM.\n0.0.0.0 ads.example.net # tracker\n*.wild.example.org\nlocalhost\n1.2.3.4\nbad_domain-.com\n",
			[]string{"ads.example.net", "example.com", "wild.example.org"}},
		{"csv by index", "csv:1", "ip", "id,ip,score\n1,203.0.113.5,9\n2,203.0.113.6,3\n3\n",
			[]string{"203.0.113.5", "203.0.113.6"}},
		{"csv by header", "csv:Domain", "domain", "rank,domain\n1,evil.example\n2,\"bad.example\"\n",
			[]string{"bad.example", "evil.example"}},
		{"json path through arrays", "json:data.ip", "ip", `{"data":[{"ip":"192.0.2.1"},{"ip":"192.0.2.2"},{"other":1}]}`,
			[]string{"192.0.2.1", "192.0.2.2"}},
		{"json string array", "json:", "domain", `["a.example","B.example",3]`,
			[]string{"a.example", "b.example"}},
		{"urls", "", "url", "HTTPS://Evil.Example/Path?q=1#frag\nftp://x.example/\nnot a url\n",
			[]string{"https://evil.example/Path?q=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.body), tc.format, tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.want, got); d != "" {
				t.Fatalf("(-want +got)\n%s", d)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ format, typ, body string }{
		{"xml", "ip", ""},
		{"", "hash", "x"},
		{"csv:missing", "ip", "a,b\n1,2\n"},
		{"json:x", "ip", "{not json"},
	} {
		if _, err := Parse([]byte(tc.body), tc.format, tc.typ); err == nil {
			t.Errorf("Parse(%q, %q) succeeded", tc.format, tc.typ)
		}
	}
}

func TestCollapseCovered(t *testing.T) {
	got, _ := Normalize([]string{"10.1.0.0/16", "10.0.0.0/8", "10.2.3.4", "11.0.0.1"}, "ip")
	if d := cmp.Diff([]string{"10.0.0.0/8", "11.0.0.1"}, got); d != "" {
		t.Fatal(d)
	}
}
