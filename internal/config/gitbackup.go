package config

import (
	"net/url"
	"regexp"
	"strings"
)

// GitBackup records each committed revision (secrets removed, see
// RedactSecrets) as a commit in a repository on the box and, with a
// remote, pushes it there over SSH with the box's deploy key.
type GitBackup struct {
	Enabled bool `json:"enabled,omitempty"`
	// Remote is an SSH URL: git@github.com:me/gate-config.git or
	// ssh://git@gitea.lan:2222/me/gate-config.git; "" = history on the box only.
	Remote string `json:"remote,omitempty"`
	// Branch to push to; "" = main.
	Branch string `json:"branch,omitempty"`
}

// scpRemote is git's short SSH form user@host:path (no scheme).
// Neither user nor host may start with "-": ssh would read it as an option.
var scpRemote = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9._~][A-Za-z0-9._/~-]*$`)

// validBranch accepts plain branch names (no "..", no leading "-" or "/").
var validBranch = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]{0,99}$`)

// ValidGitRemote reports whether s is an SSH remote the box can push to.
func ValidGitRemote(s string) bool {
	if scpRemote.MatchString(s) {
		return true
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "ssh" || u.User == nil || u.Hostname() == "" || len(u.Path) < 2 || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if _, hasPw := u.User.Password(); hasPw {
		return false
	}
	if strings.HasPrefix(u.Hostname(), "-") || strings.HasPrefix(u.User.Username(), "-") || u.User.Username() == "" {
		return false
	}
	return !strings.ContainsAny(s, " \t\n'\"\\")
}

func (v *validator) checkGitBackup() {
	g := v.cfg.GitBackup
	if g.Remote != "" && !ValidGitRemote(g.Remote) {
		v.add("git_backup.remote", "want an SSH remote like git@github.com:me/gate-config.git or ssh://git@host:2222/me/gate-config.git")
	}
	if g.Branch != "" && (!validBranch.MatchString(g.Branch) || strings.Contains(g.Branch, "..") || strings.HasSuffix(g.Branch, ".lock") || strings.HasSuffix(g.Branch, "/")) {
		v.add("git_backup.branch", "invalid branch name %q", g.Branch)
	}
}
