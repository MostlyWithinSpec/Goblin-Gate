package config

import "testing"

func TestValidGitRemote(t *testing.T) {
	for s, want := range map[string]bool{
		"git@github.com:me/gate-config.git":           true,
		"ssh://git@gitea.lan:2222/me/gate-config.git": true,
		"ssh://git@10.0.0.5/srv/git/gate.git":         true,
		"git@-oProxyCommand=x:repo":                   false,
		"-oProxyCommand=x@host:repo":                  false,
		"ssh://git@-oProxyCommand=x/repo":             false,
		"ssh://git:secret@host/repo.git":              false, // no passwords in the config
		"https://github.com/me/gate.git":              false, // the deploy key needs SSH
		"git@github.com:me/gate config.git":           false,
		"ssh://host/repo.git":                         false, // a user is required
		"file:///srv/git/gate.git":                    false,
		"git@github.com:":                             false,
		"ssh://git@host/repo.git?x=1":                 false,
	} {
		if got := ValidGitRemote(s); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
	cfg := Default()
	cfg.GitBackup = GitBackup{Enabled: true, Remote: "git@github.com:me/x.git", Branch: "../x"}
	if errs := Validate(cfg); len(errs) != 1 || errs[0].Path != "git_backup.branch" {
		t.Fatalf("errors: %v", errs)
	}
}
