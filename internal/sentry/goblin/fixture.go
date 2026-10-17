package goblin

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// Fixture returns n answered incidents of a simulated admin, oldest first,
// the same for the same seed: the dataset both models are compared on.
//
// The admin is consistent but not perfect: exploits against the NAS and
// web server and block-listed sources are blocked, their own Plex server's
// alerts and stream noise are ignored, SQL injection alerts from their
// pentest service (203.0.113.0/24) are ignored, scanners are watched the
// first time and blocked when they come back, internal hosts are watched
// or reported. About 4% of answers are random, STUN alerts switch from
// "watch" to "ignore" after 60% of the time, and log4j attempts only
// appear in the last 20% (a signature never seen in training).
func Fixture(seed uint64, n int) []sentry.Example {
	r := rand.New(rand.NewPCG(seed, 0x60b11))
	type host struct{ ip, name, intf string }
	nas := host{"192.168.1.10", "nas", "lan"}
	plex := host{"192.168.1.20", "plex", "lan"}
	web := host{"192.168.1.30", "web", "lan"}
	box := host{"192.168.1.1", "", "box"}
	laptop := host{"192.168.1.50", "laptop", "lan"}
	cams := []host{{"192.168.20.11", "cam-door", "iot"}, {"192.168.20.12", "cam-yard", "iot"}, {"192.168.20.13", "cam-garage", "iot"}}
	anyHost := []host{nas, plex, web, box}
	type ctx struct {
		remote                  string
		seen, remoteSeen, watch bool
		late                    bool
	}
	type family struct {
		sid, sig, cat string
		sev           int
		dir           string
		port          int
		proto         string
		hosts         []host
		pool          string // remote pool: "a.b.c" + host number
		poolSize      int
		weight        int
		from          float64 // share of time before the family appears
		answer        func(c ctx) (string, int)
	}
	block := func(m int) func(ctx) (string, int) { return func(ctx) (string, int) { return LabelBlock, m } }
	fixed := func(l string) func(ctx) (string, int) { return func(ctx) (string, int) { return l, 0 } }
	families := []family{
		{"2024217", "ET EXPLOIT Possible ETERNALBLUE MS17-010 Echo Response", "Attempted Administrator Privilege Gain", 1, "inbound", 445, "tcp", []host{nas}, "45.155.205", 40, 6, 0, block(10080)},
		{"2001219", "ET SCAN Potential SSH Scan", "Attempted Information Leak", 2, "inbound", 22, "tcp", []host{box}, "45.155.205", 40, 12, 0,
			func(c ctx) (string, int) {
				if c.remoteSeen {
					return LabelBlock, 1440
				}
				return LabelWatch, 0
			}},
		{"2010937", "ET SCAN Suspicious inbound to mySQL port 3306", "Potentially Bad Traffic", 2, "inbound", 3306, "tcp", []host{web}, "193.142.146", 60, 5, 0, block(60)},
		{"2013504", "ET POLICY Plex Media Server external access", "Potential Corporate Privacy Violation", 2, "inbound", 32400, "tcp", []host{plex}, "73.21.88", 25, 10, 0, fixed(LabelIgnore)},
		{"2031502", "ET WEB_SERVER Plex path traversal attempt", "Web Application Attack", 1, "inbound", 32400, "tcp", []host{plex}, "185.220.101", 50, 2, 0, block(1440)},
		{"2402000", "ET DROP Dshield Block Listed Source group 1", "Misc Attack", 2, "inbound", 0, "tcp", anyHost, "89.248.165", 80, 8, 0, block(10080)},
		{"2400001", "ET DROP Spamhaus DROP Listed Traffic Inbound group 2", "Misc Attack", 2, "inbound", 0, "tcp", anyHost, "5.188.10", 80, 5, 0, block(10080)},
		{"2210044", "SURICATA STREAM Packet with invalid timestamp", "Generic Protocol Command Decode", 2, "inbound", 443, "tcp", []host{web}, "66.249.66", 200, 8, 0, fixed(LabelIgnore)},
		{"2027758", "ET INFO DNS Query for .cloud TLD", "Potentially Bad Traffic", 2, "outbound", 53, "udp", []host{laptop}, "1.1.1", 3, 5, 0, fixed(LabelIgnore)},
		{"2034567", "ET WEB_SERVER Possible SQL Injection Attempt UNION SELECT", "Web Application Attack", 1, "inbound", 443, "tcp", []host{web}, "", 0, 7, 0,
			func(c ctx) (string, int) {
				if strings.HasPrefix(c.remote, "203.0.113.") { // the pentest service
					return LabelIgnore, 0
				}
				return LabelBlock, 1440
			}},
		{"2019401", "ET POLICY Session Traversal Utilities for NAT (STUN Binding Request)", "Attempted User Privilege Gain", 2, "inbound", 3478, "udp", cams, "142.250.80", 40, 5, 0,
			func(c ctx) (string, int) {
				if c.late {
					return LabelIgnore, 0
				}
				return LabelWatch, 0
			}},
		{"2025883", "ET MALWARE Possible Mirai Variant CnC Beacon", "A Network Trojan was detected", 1, "outbound", 23, "tcp", cams, "176.65.137", 10, 2, 0, fixed(LabelWatch)},
		{"2009582", "ET SCAN NMAP -sS window 1024", "Attempted Information Leak", 2, "inbound", 0, "tcp", anyHost, "45.155.205", 40, 10, 0,
			func(c ctx) (string, int) {
				if c.watch || c.remoteSeen {
					return LabelBlock, 240
				}
				return LabelWatch, 0
			}},
		{"2025701", "ET POLICY SMB2 NT Create AndX Request For an Executable File", "Potential Corporate Privacy Violation", 2, "lateral", 445, "tcp", []host{laptop}, "", 0, 4, 0, fixed(LabelIgnore)},
		{"2023883", "ET DNS Query to a *.top domain - Likely Hostile", "Potentially Bad Traffic", 2, "outbound", 53, "udp", []host{laptop}, "1.1.1", 3, 3, 0, fixed(LabelNotify)},
		{"2034647", "ET EXPLOIT Apache log4j RCE Attempt (jndi:ldap)", "Attempted Administrator Privilege Gain", 1, "inbound", 443, "tcp", []host{web}, "194.26.29", 60, 6, 0.8, block(10080)},
	}
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	span := 90 * 24 * time.Hour
	type past struct {
		sig, remote, label string
		t                  time.Time
	}
	var hist []past
	var out []sentry.Example
	var alertID int64
	for i := 0; i < n; i++ {
		frac := float64(i) / float64(n)
		t := start.Add(time.Duration(frac * float64(span))).Add(time.Duration(r.IntN(3600)) * time.Second)
		total := 0
		for _, f := range families {
			if frac >= f.from {
				total += f.weight
			}
		}
		pick := r.IntN(total)
		var f family
		for _, fam := range families {
			if frac < fam.from {
				continue
			}
			if pick < fam.weight {
				f = fam
				break
			}
			pick -= fam.weight
		}
		h := f.hosts[r.IntN(len(f.hosts))]
		var remote string
		switch {
		case f.dir == "lateral":
			remote = ""
		case f.sid == "2034567": // SQL injection: a quarter from the pentest service
			if r.IntN(4) == 0 {
				remote = fmt.Sprintf("203.0.113.%d", 10+r.IntN(5))
			} else {
				remote = fmt.Sprintf("%d.%d.%d.%d", 20+r.IntN(200), r.IntN(256), r.IntN(256), 1+r.IntN(254))
			}
		default:
			remote = fmt.Sprintf("%s.%d", f.pool, 1+r.IntN(f.poolSize))
		}
		alertID++
		a := sentry.Alert{ID: alertID, Time: t.Unix(), Action: "alert", Signature: f.sig, SignatureID: f.sid,
			Category: f.cat, Severity: f.sev, Proto: f.proto}
		en := sentry.Enrichment{Direction: f.dir, Remote: remote, Local: h.ip, LocalName: h.name, LocalIntf: h.intf}
		port := f.port
		if port == 0 {
			port = []int{22, 80, 443, 445, 3389, 8080}[r.IntN(6)]
		}
		switch f.dir {
		case "inbound":
			a.SrcIP, a.SrcPort, a.DstIP, a.DstPort = remote, 1024+r.IntN(60000), h.ip, port
		case "outbound":
			a.SrcIP, a.SrcPort, a.DstIP, a.DstPort = h.ip, 1024+r.IntN(60000), remote, port
		default:
			a.SrcIP, a.SrcPort, a.DstIP, a.DstPort = h.ip, 1024+r.IntN(60000), nas.ip, port
			en.Remote = ""
		}
		c := ctx{remote: remote, late: frac >= 0.6}
		for _, p := range hist {
			if p.sig == f.sig && p.remote == remote {
				en.Seen++
			}
			if remote != "" && p.remote == remote {
				en.RemoteSeen++
				if p.label == LabelWatch && t.Sub(p.t) < 24*time.Hour {
					en.Watched = true
				}
			}
			if p.sig == f.sig {
				if en.PastAnswers == nil {
					en.PastAnswers = map[string]int{}
				}
				en.PastAnswers[answerOf(p.label)]++
			}
		}
		c.seen, c.remoteSeen, c.watch = en.Seen > 0, en.RemoteSeen > 0, en.Watched
		label, minutes := f.answer(c)
		if r.IntN(100) < 4 { // a careless or changed-mind answer
			label = []string{LabelBlock, LabelIgnore, LabelWatch}[r.IntN(3)]
			minutes = 60
		}
		if label != LabelBlock {
			minutes = 0
		}
		x := sentry.Example{Time: t, Alert: a, Enrichment: en, Answer: answerOf(label)}
		x.Action = sentry.Action{Tool: label, Target: remote, Minutes: minutes}
		if label == LabelIgnore {
			x.Action.Target = ""
		}
		x.Recommendation = x.Action
		if x.Answer == sentry.AnswerBlock || x.Answer == sentry.AnswerIgnore {
			x.Recommendation = sentry.Action{Tool: sentry.ToolBlock, Target: remote, Minutes: 60}
		}
		out = append(out, x)
		hist = append(hist, past{f.sig, remote, label, t})
	}
	return out
}

// answerOf is the admin's answer that leads to a label.
func answerOf(label string) string {
	switch label {
	case LabelBlock:
		return sentry.AnswerBlock
	case LabelIgnore:
		return sentry.AnswerIgnore
	}
	return sentry.AnswerApprove
}
