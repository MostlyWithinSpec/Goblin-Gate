// Package checks is Goblin Checks: an audit of the running config and a
// little live state against what usually goes wrong on a home or small
// office firewall. Every finding says what is risky, why, and where to fix
// it. Checks are pure functions so they are cheap to run on every request
// and easy to test.
package checks

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Severity of a finding.
type Severity string

const (
	High   Severity = "high"
	Medium Severity = "medium"
	Low    Severity = "low"
	Info   Severity = "info"
)

// weight is how many points a finding of a severity takes off the score.
var weight = map[Severity]int{High: 20, Medium: 8, Low: 3, Info: 0}

var order = map[Severity]int{High: 0, Medium: 1, Low: 2, Info: 3}

// Finding is one thing worth fixing.
type Finding struct {
	// Key identifies the finding across runs ("<check>:<subject>"); it is
	// what a dismissal stores.
	Key      string   `json:"key"`
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	// Paths are the config paths involved, e.g. "interfaces[0].allow_access".
	Paths []string `json:"paths,omitempty"`
	// Link opens the web UI page where it is fixed.
	Link string `json:"link"`
}

// Passed is a check that found nothing.
type Passed struct {
	Check string `json:"check"`
	Title string `json:"title"`
}

// Report is the result of a run.
type Report struct {
	// Score is 100 minus the weight of every finding that is not dismissed,
	// never below 0. Passed checks add nothing: only real findings count.
	Score     int       `json:"score"`
	Findings  []Finding `json:"findings"`
	Dismissed []Finding `json:"dismissed"`
	Passed    []Passed  `json:"passed"`
}

// State is the live state some checks look at; the zero value means
// "unknown" and those checks stay quiet.
type State struct {
	Now            time.Time
	RebootRequired bool
	// UpdateAvailable is a newer Goblin Gate version, "" when up to date or
	// unknown.
	UpdateAvailable string
	// PluginUpdates lists installed first-party plugins with a newer version,
	// as "sentry 0.1.0~beta.8 → 0.1.0~beta.9".
	PluginUpdates []string
	// RebootSince is when the pending restart was first needed (zero = none
	// or unknown); it keys the finding, so a dismissal covers that restart
	// only.
	RebootSince time.Time
	// GitLastPush and GitLastError describe config history pushes.
	GitLastPush  time.Time
	GitLastError string
}

// check is one audit.
type check struct {
	id    string
	title string // shown in the passed list
	run   func(c *ctx) []Finding
}

// ctx is what a check sees: the config with its interfaces resolved.
type ctx struct {
	cfg   *config.Config
	state State
	// role of every interface and VLAN by name; a VLAN takes its parent's.
	role map[string]string
}

func (c *ctx) wan(name string) bool { return c.role[name] == "wan" }

// Checks lists every check id and title, in the order they run.
func Checks() []Passed {
	out := make([]Passed, 0, len(all))
	for _, ch := range all {
		out = append(out, Passed{Check: ch.id, Title: ch.title})
	}
	return out
}

// Run audits cfg. Findings whose key is in dismissed are reported apart and
// do not count against the score.
func Run(cfg *config.Config, st State, dismissed map[string]bool) Report {
	if st.Now.IsZero() {
		st.Now = time.Now()
	}
	c := &ctx{cfg: cfg, state: st, role: map[string]string{}}
	for _, i := range cfg.Interfaces {
		c.role[i.Name] = i.Role
	}
	for _, v := range cfg.VLANs {
		c.role[v.Name] = c.role[v.Parent]
	}
	r := Report{Score: 100, Findings: []Finding{}, Dismissed: []Finding{}, Passed: []Passed{}}
	for _, ch := range all {
		fs := ch.run(c)
		if len(fs) == 0 {
			r.Passed = append(r.Passed, Passed{Check: ch.id, Title: ch.title})
			continue
		}
		for _, f := range fs {
			f.Check = ch.id
			if dismissed[f.Key] {
				r.Dismissed = append(r.Dismissed, f)
				continue
			}
			r.Findings = append(r.Findings, f)
			r.Score -= weight[f.Severity]
		}
	}
	r.Score = max(r.Score, 0)
	sort.SliceStable(r.Findings, func(i, j int) bool { return order[r.Findings[i].Severity] < order[r.Findings[j].Severity] })
	return r
}

func edit(page, name string) string { return page + "?edit=" + url.QueryEscape(name) }

// riskyPorts are services that should never face the internet directly.
var riskyPorts = map[int]string{
	21: "FTP", 22: "SSH", 23: "Telnet", 445: "SMB (Windows file sharing)", 1433: "SQL Server",
	3306: "MySQL", 3389: "Remote Desktop", 5432: "PostgreSQL", 5900: "VNC", 6379: "Redis",
}

// portsIn lists the risky ports a "443" or "8000-8100" range contains.
func portsIn(spec string) []string {
	lo, hi := 0, 0
	if a, b, ok := strings.Cut(spec, "-"); ok {
		fmt.Sscan(a, &lo)
		fmt.Sscan(b, &hi)
	} else {
		fmt.Sscan(spec, &lo)
		hi = lo
	}
	var out []string
	for p, name := range riskyPorts {
		if p >= lo && p <= hi {
			out = append(out, fmt.Sprintf("%d (%s)", p, name))
		}
	}
	slices.Sort(out)
	return out
}

// wildcard reports whether an address, service or source list matches
// everything: empty, or "all" / "any" in any case (both work everywhere,
// as in config validation).
func wildcard(list []string) bool {
	return len(list) == 0 || slices.ContainsFunc(list, func(s string) bool {
		return strings.EqualFold(s, "all") || strings.EqualFold(s, "any")
	})
}

var all = []check{
	{"wan-admin", "No admin access from the internet", func(c *ctx) []Finding {
		var out []Finding
		look := func(path, name string, access []string) {
			if !c.wan(name) {
				return
			}
			for _, a := range access {
				if a != "https" && a != "ssh" {
					continue
				}
				what := map[string]string{"https": "the web admin page", "ssh": "SSH"}[a]
				out = append(out, Finding{
					Key: "wan-admin:" + name + ":" + a, Severity: High,
					Title: fmt.Sprintf("%s is open on the WAN interface %s", what, name),
					Detail: "Anyone on the internet can reach the login and try passwords. Use Tailscale or WireGuard " +
						"to manage the box from outside instead, and turn off " + a + " under allowed access on " + name + ".",
					Paths: []string{path + ".allow_access"}, Link: edit("/network/interfaces", name),
				})
			}
		}
		for i, ifc := range c.cfg.Interfaces {
			look(fmt.Sprintf("interfaces[%d]", i), ifc.Name, ifc.AllowAccess)
		}
		for i, v := range c.cfg.VLANs {
			look(fmt.Sprintf("vlans[%d]", i), v.Name, v.AllowAccess)
		}
		return out
	}},
	{"open-forward", "Port forwards limited to known sources", func(c *ctx) []Finding {
		var out []Finding
		for i, pf := range c.cfg.NAT.PortForwards {
			if !wildcard(pf.Source) {
				continue
			}
			f := Finding{
				Key: "open-forward:" + pf.Name, Severity: Medium,
				Title: fmt.Sprintf("Port forward %s is open to the whole internet", pf.Name),
				Detail: fmt.Sprintf("Port %s/%s reaches %s from any address. If only you or a few places need it, "+
					"limit the forward to their addresses or countries under allowed sources.", pf.ExtPort, pf.Protocol, pf.ToAddr),
				Paths: []string{fmt.Sprintf("nat.port_forwards[%d].source", i)}, Link: edit("/network/port-forwards", pf.Name),
			}
			if risky := portsIn(pf.ExtPort); len(risky) > 0 {
				f.Severity = High
				f.Detail = fmt.Sprintf("It exposes %s, which is attacked constantly on the internet. Reach it over "+
					"Tailscale or WireGuard instead, or at least limit the forward to known source addresses.", strings.Join(risky, ", "))
			}
			out = append(out, f)
		}
		return out
	}},
	{"upnp", "UPnP is off", func(c *ctx) []Finding {
		if !c.cfg.UPnP.Enabled {
			return nil
		}
		return []Finding{{
			Key: "upnp:on", Severity: Low, Title: "UPnP lets devices open ports by themselves",
			Detail: "Game consoles like it, but so does malware: any device on the allowed networks can open a port " +
				"to itself without asking. Keep it limited to the networks that need it, or turn it off.",
			Paths: []string{"upnp.enabled"}, Link: "/network/upnp",
		}}
	}},
	{"wan-accept", "No policy lets the whole internet in", func(c *ctx) []Finding {
		var out []Finding
		for i, p := range c.cfg.Policies {
			if !p.Enabled || p.Action != "accept" || !slices.ContainsFunc(p.SrcIntf, c.wan) {
				continue
			}
			if !wildcard(p.SrcAddr) || !wildcard(p.DstAddr) || !wildcard(p.Service) {
				continue
			}
			out = append(out, Finding{
				Key: fmt.Sprintf("wan-accept:%d", p.ID), Severity: High,
				Title: fmt.Sprintf("Policy %s accepts any traffic from the internet", policyName(p)),
				Detail: "It matches every source, destination and service coming in on a WAN interface, so everything " +
					"behind it is exposed. Narrow it to the service and destination that need to be reachable.",
				Paths: []string{fmt.Sprintf("policies[%d]", i)}, Link: fmt.Sprintf("/policy?edit=%d", p.ID),
			})
		}
		return out
	}},
	{"guest-access", "Guest networks can't reach your LAN", func(c *ctx) []Finding {
		var out []Finding
		for i, p := range c.cfg.Policies {
			if !p.Enabled || p.Action != "accept" || !wildcard(p.Service) {
				continue
			}
			guest := slices.ContainsFunc(p.SrcIntf, func(n string) bool { return c.role[n] == "guest" })
			inner := slices.ContainsFunc(p.DstIntf, func(n string) bool { r := c.role[n]; return r == "lan" || r == "mgmt" })
			if !guest || !inner {
				continue
			}
			out = append(out, Finding{
				Key: fmt.Sprintf("guest-access:%d", p.ID), Severity: Medium,
				Title: fmt.Sprintf("Policy %s lets a guest network into your LAN", policyName(p)),
				Detail: "Guests' devices are not yours to trust. Allow only the one service they need (a printer, " +
					"a Chromecast) instead of every service.",
				Paths: []string{fmt.Sprintf("policies[%d]", i)}, Link: fmt.Sprintf("/policy?edit=%d", p.ID),
			})
		}
		return out
	}},
	{"admin-2fa", "Every admin uses two-factor login", func(c *ctx) []Finding {
		var out []Finding
		for i, a := range c.cfg.Admins {
			if a.Role != "admin" || a.PasswordHash == "" || a.TOTPSecret != "" {
				continue
			}
			out = append(out, Finding{
				Key: "admin-2fa:" + a.Username, Severity: Medium,
				Title: fmt.Sprintf("Admin %s logs in without a second factor", a.Username),
				Detail: "A leaked or guessed password is all it takes to change the firewall. Turn on two-factor login " +
					"with an authenticator app under System → Admins.",
				Paths: []string{fmt.Sprintf("admins[%d]", i)}, Link: "/system?tab=Admins",
			})
		}
		return out
	}},
	{"backup", "Config history is kept off the box", func(c *ctx) []Finding {
		gb := c.cfg.GitBackup
		if !gb.Enabled {
			return []Finding{{
				Key: "backup:off", Severity: Low, Title: "The config is only stored on the box",
				Detail: "If the disk dies, every policy and setting goes with it. Push the config history to your own " +
					"GitHub or Gitea repository (System → Backup), or download a backup now and then.",
				Paths: []string{"git_backup.enabled"}, Link: "/system?tab=Backup",
			}}
		}
		st := c.state
		switch {
		case st.GitLastError != "":
			return []Finding{{
				Key: "backup:failing", Severity: Medium, Title: "Pushing the config history fails",
				Detail: "The last push to " + gb.Remote + " failed: " + st.GitLastError + ". New revisions stay on the box until it works again.",
				Paths:  []string{"git_backup"}, Link: "/system?tab=Backup",
			}}
		case !st.GitLastPush.IsZero() && st.Now.Sub(st.GitLastPush) > 30*24*time.Hour:
			return []Finding{{
				Key: "backup:stale", Severity: Low, Title: "The config history was not pushed for over 30 days",
				Detail: "The last successful push was " + st.GitLastPush.Format("2006-01-02") + ". If nothing changed since, this is fine; otherwise check the deploy key.",
				Paths:  []string{"git_backup"}, Link: "/system?tab=Backup",
			}}
		}
		return nil
	}},
	{"security-updates", "Security updates install automatically", func(c *ctx) []Finding {
		if c.cfg.System.SecurityUpdates != "manual" {
			return nil
		}
		return []Finding{{
			Key: "security-updates:manual", Severity: Medium, Title: "Debian security updates are not installed automatically",
			Detail: "Fixes for the kernel, OpenSSL and other parts of the system then wait for you to install them. " +
				"Turn automatic security updates back on unless you have a routine for it.",
			Paths: []string{"system.security_updates"}, Link: "/system/updates",
		}}
	}},
	{"reboot", "No restart pending", func(c *ctx) []Finding {
		if !c.state.RebootRequired {
			return nil
		}
		key := "reboot:pending"
		if !c.state.RebootSince.IsZero() {
			key = "reboot:" + strconv.FormatInt(c.state.RebootSince.Unix(), 10)
		}
		return []Finding{{
			Key: key, Severity: Low, Title: "A restart is needed to finish a security update",
			Detail: "An updated kernel or system library is installed but the old one is still running. Restart the " +
				"box at a quiet moment: your name at the top right → Restart Goblin Gate. Installing updates does not restart it.",
			Link: "/system/updates",
		}}
	}},
	{"goblin-update", "Goblin Gate is up to date", func(c *ctx) []Finding {
		var out []Finding
		if len(c.state.PluginUpdates) > 0 {
			out = append(out, Finding{
				Key: "goblin-update:plugins", Severity: Low,
				Title:  "Plugin updates are available: " + strings.Join(c.state.PluginUpdates, ", "),
				Detail: "Plugins are updated together with Goblin Gate. Install the update on the Updates page.",
				Link:   "/system/updates",
			})
		}
		if c.state.UpdateAvailable == "" {
			return out
		}
		return append(out, Finding{
			Key: "goblin-update:" + c.state.UpdateAvailable, Severity: Low,
			Title:  "Goblin Gate " + c.state.UpdateAvailable + " is available",
			Detail: "Updates bring fixes, including security fixes. Installing one keeps the old version to roll back to if anything goes wrong.",
			Link:   "/system/updates",
		})
	}},
	{"dns-filter", "Guest networks have a DNS filter", func(c *ctx) []Finding {
		var out []Finding
		for i, p := range c.cfg.Policies {
			if !p.Enabled || p.Action != "accept" || p.DNSFilterProfile != "" {
				continue
			}
			guest := slices.ContainsFunc(p.SrcIntf, func(n string) bool { return c.role[n] == "guest" })
			if !guest || !slices.ContainsFunc(p.DstIntf, c.wan) {
				continue
			}
			out = append(out, Finding{
				Key: fmt.Sprintf("dns-filter:%d", p.ID), Severity: Low,
				Title: fmt.Sprintf("Guests reach the internet through %s without a DNS filter", policyName(p)),
				Detail: "A DNS filter profile blocks known malware, phishing and ad domains for everyone on the guest " +
					"network at almost no cost. Pick one on the policy.",
				Paths: []string{fmt.Sprintf("policies[%d].dnsfilter_profile", i)}, Link: fmt.Sprintf("/policy?edit=%d", p.ID),
			})
		}
		return out
	}},
	{"wan-ips", "Traffic from the internet is inspected", func(c *ctx) []Finding {
		var out []Finding
		for i, p := range c.cfg.Policies {
			if !p.Enabled || p.Action != "accept" || p.IPSProfile != "" || !slices.ContainsFunc(p.SrcIntf, c.wan) {
				continue
			}
			if wildcard(p.SrcAddr) && wildcard(p.DstAddr) && wildcard(p.Service) {
				continue // already a wan-accept finding; fixing that fixes this
			}
			out = append(out, Finding{
				Key: fmt.Sprintf("wan-ips:%d", p.ID), Severity: Medium,
				Title: fmt.Sprintf("Policy %s lets internet traffic in without IPS", policyName(p)),
				Detail: "Traffic someone on the internet starts is where exploits come from. An IPS profile on this " +
					"policy blocks known attacks against the service it opens.",
				Paths: []string{fmt.Sprintf("policies[%d].ips_profile", i)}, Link: fmt.Sprintf("/policy?edit=%d", p.ID),
			})
		}
		return out
	}},
	{"custom-rules", "No custom nftables rules to review", func(c *ctx) []Finding {
		n := 0
		for _, r := range c.cfg.CustomRules {
			if r.Enabled {
				n++
			}
		}
		if n == 0 {
			return nil
		}
		return []Finding{{
			Key: "custom-rules:on", Severity: Info, Title: fmt.Sprintf("%d custom nftables rule(s) are active", n),
			Detail: "Goblin Checks can't tell what custom rules allow. Review them now and then, and remove the ones " +
				"you no longer need.",
			Paths: []string{"custom_rules"}, Link: "/policy/custom-rules",
		}}
	}},
}

func policyName(p config.Policy) string {
	if p.Name != "" {
		return fmt.Sprintf("%q", p.Name)
	}
	return fmt.Sprintf("#%d", p.ID)
}
