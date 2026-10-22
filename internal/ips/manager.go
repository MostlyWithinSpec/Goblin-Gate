package ips

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/suricata"
)

// Paths of the shared inputs.
type Paths struct {
	Source         string // suricata-update output
	Classification string
	Dir            string // rendered per-profile files (suricata.Dir)
	RulesDir       string // per-profile rules (suricata.RulesDir)
	// UpdateDir is suricata-update's state (sources, index cache); "" =
	// leave the rule sources alone.
	UpdateDir string
}

// DefaultPaths are the Debian locations.
var DefaultPaths = Paths{
	Source:         "/var/lib/suricata/rules/suricata.rules",
	Classification: "/etc/suricata/classification.config",
	Dir:            suricata.Dir,
	RulesDir:       suricata.RulesDir,
	UpdateDir:      "/var/lib/suricata/update",
}

// BuildRules writes the rules of one instance (`goblind ips-rules
// <instance>`, run by goblin-suricata@.service before every start) from
// its rendered profile JSON. Without a rule download yet it writes a rules
// file with only the header, so Suricata starts and the updater fills it.
func BuildRules(p Paths, instance string) (Stats, error) {
	raw, err := os.ReadFile(filepath.Join(p.Dir, instance+".profile.json"))
	if err != nil {
		return Stats{}, fmt.Errorf("ips: profile of %s: %w", instance, err)
	}
	var prof config.IPSProfile
	if err := json.Unmarshal(raw, &prof); err != nil {
		return Stats{}, fmt.Errorf("ips: profile of %s: %w", instance, err)
	}
	classes := map[string]int{}
	if f, err := os.Open(p.Classification); err == nil {
		classes, err = ParseClassifications(f)
		f.Close()
		if err != nil {
			return Stats{}, err
		}
	}
	var src io.Reader = strings.NewReader("")
	if f, err := os.Open(p.Source); err == nil {
		defer f.Close()
		src = f
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Stats{}, err
	}
	var out bytes.Buffer
	st, err := ProfileRules(&out, src, prof, classes)
	if err != nil {
		return st, err
	}
	if err := os.MkdirAll(p.RulesDir, 0o755); err != nil {
		return st, err
	}
	dst := filepath.Join(p.RulesDir, instance+".rules")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return st, err
	}
	return st, os.Rename(tmp, dst)
}

// Instances lists the instances that have a rendered profile.
func Instances(p Paths) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(p.Dir, "*.profile.json"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		out = append(out, strings.TrimSuffix(filepath.Base(m), ".profile.json"))
	}
	sort.Strings(out)
	return out, nil
}

// Updater refreshes the ET Open rules with suricata-update every Every
// (default 24 h; at start when the download is missing or older), rebuilds
// every instance's rules and asks it to reload them.
type Updater struct {
	Paths  Paths
	Every  time.Duration
	Logger *slog.Logger
	// Exec runs a command (default os/exec); tests replace it.
	Exec func(ctx context.Context, name string, args ...string) error
	Now  func() time.Time
	// MinRules is how many usable (non-SURICATA) rules a download needs to
	// count as one (default DefaultMinRules). suricata-update exits 0 and
	// writes only Suricata's own rules when it cannot reach the rule
	// servers, e.g. at first boot before the WAN is up.
	MinRules int
	// ETProCode returns the ET Pro code of the running config ("" = ET
	// Open); nil leaves the rule sources alone.
	ETProCode func() string

	state updaterState
}

// DefaultMinRules: ET Open has tens of thousands of rules.
const DefaultMinRules = 1000

// usableRules counts the enabled rules of a download outside the SURICATA
// category (the engine's own rules ship with Suricata, not with ET Open).
func usableRules(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "alert ") && !strings.HasPrefix(line, "drop ") {
			continue
		}
		if m := msgRe.FindStringSubmatch(line); m != nil && Category(m[1]) == "SURICATA" {
			continue
		}
		n++
	}
	return n, sc.Err()
}

func (u *Updater) exec(ctx context.Context, name string, args ...string) error {
	if u.Exec != nil {
		return u.Exec(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, lastLines(string(out), 5))
	}
	return nil
}

func (u *Updater) minRules() int {
	if u.MinRules > 0 {
		return u.MinRules
	}
	return DefaultMinRules
}

func (u *Updater) logger() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}

// Run updates on schedule until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	every := u.Every
	if every <= 0 {
		every = 24 * time.Hour
	}
	now := time.Now
	if u.Now != nil {
		now = u.Now
	}
	// A recent, complete download waits for its schedule; anything else
	// updates now.
	wait := time.Duration(0)
	if st, err := os.Stat(u.Paths.Source); err == nil && now().Sub(st.ModTime()) < every {
		if n, _ := usableRules(u.Paths.Source); n >= u.minRules() {
			wait = every - now().Sub(st.ModTime())
		}
	}
	// failures retry soon (the WAN may still be coming up), backing off
	// from a minute to an hour
	backoff := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-u.kicks():
		}
		if err := u.UpdateOnce(ctx); err != nil {
			u.logger().Warn("ips: rule update failed", "error", err, "retry_in", backoff.String())
			wait = backoff
			backoff = min(2*backoff, time.Hour)
			continue
		}
		wait, backoff = every, time.Minute
	}
}

// UpdateOnce downloads the rules and reloads every instance.
func (u *Updater) UpdateOnce(ctx context.Context) error {
	ruleset := u.wantRuleset()
	code, n, err := u.download(ctx)
	err = scrub(err, code)
	u.setStatus(func(st *UpdateStatus) {
		st.Ruleset = ruleset
		if err != nil {
			st.LastError = err.Error()
			return
		}
		st.LastError, st.LastUpdate, st.Rules = "", time.Now(), n
	})
	if err != nil {
		return err
	}
	u.logger().Info("ips: rule download done", "ruleset", ruleset, "rules", n)
	insts, err := Instances(u.Paths)
	if err != nil {
		return err
	}
	for _, inst := range insts {
		st, err := BuildRules(u.Paths, inst)
		if err != nil {
			u.logger().Error("ips: building rules failed", "instance", inst, "error", err)
			continue
		}
		if err := u.exec(ctx, "systemctl", "reload", "goblin-suricata@"+inst+".service"); err != nil {
			u.logger().Warn("ips: reload failed", "instance", inst, "error", err)
			continue
		}
		u.logger().Info("ips: rules updated", "instance", inst, "drop", st.Drop, "alert", st.Alert)
	}
	return nil
}

// download fetches the rules with suricata-update and counts them; it
// returns the ET Pro code in use so errors can be scrubbed of it.
func (u *Updater) download(ctx context.Context) (string, int, error) {
	uctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	code, err := u.syncSource(uctx)
	if err != nil {
		return code, 0, err
	}
	if err := u.exec(uctx, "suricata-update", "--no-test", "--no-reload", "-o", filepath.Dir(u.Paths.Source)); err != nil {
		return code, 0, err
	}
	n, err := usableRules(u.Paths.Source)
	if err != nil {
		return code, 0, fmt.Errorf("read downloaded rules: %w", err)
	}
	if n < u.minRules() {
		return code, n, fmt.Errorf("the download has only %d usable rules (are the rule servers reachable?)", n)
	}
	return code, n, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
