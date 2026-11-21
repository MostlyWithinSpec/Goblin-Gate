package updates

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSources is the APT sources file goblind renders for Goblin Gate's own
// repository (deb822: URIs, Suites = the update channel, Signed-By).
const DefaultSources = "/etc/apt/sources.list.d/goblin-gate.sources"

// DefaultAptCache is where apt keeps the packages it downloaded.
const DefaultAptCache = "/var/cache/apt/archives"

// fullSuite carries every release (stable releases are published to it too).
const fullSuite = "beta"

// repoSource is the box's Goblin Gate repository as configured.
type repoSource struct {
	URI, Suite, SignedBy string
}

func (m *Manager) sourcesPath() string {
	if m.Sources != "" {
		return m.Sources
	}
	return DefaultSources
}

func (m *Manager) aptCache() string {
	if m.AptCache != "" {
		return m.AptCache
	}
	return DefaultAptCache
}

// readSource parses the deb822 sources file; zero fields when it is missing.
func (m *Manager) readSource() repoSource {
	var src repoSource
	f, err := os.Open(m.sourcesPath())
	if err != nil {
		return src
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.HasPrefix(strings.TrimSpace(k), "#") {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "uris":
			src.URI = strings.Fields(v + " ")[0]
		case "suites":
			src.Suite = strings.Fields(v + " ")[0]
		case "signed-by":
			src.SignedBy = v
		}
	}
	return src
}

// rollbackSaver finds the installed version's .deb of a package, trying the
// current channel, apt's cache, then the full (beta) suite of the same
// repository through a private APT state, so the box's own lists and channel
// stay untouched.
type rollbackSaver struct {
	m       *Manager
	ctx     context.Context
	keep    string
	src     repoSource
	betaDir string
	betaErr error // nil until tried; then the outcome of the private update
	betaRan bool
}

func (m *Manager) newSaver(ctx context.Context, keep string) *rollbackSaver {
	return &rollbackSaver{m: m, ctx: ctx, keep: keep, src: m.readSource(), betaDir: filepath.Join(m.dir(), "full-suite")}
}

// channel names the configured channel for messages.
func (r *rollbackSaver) channel() string {
	if r.src.Suite == "" {
		return "current"
	}
	return r.src.Suite
}

// debFile names a package file as apt and dpkg do (an epoch's ":" becomes %3a).
func debGlob(dir, pkg, version string) string {
	return filepath.Join(dir, pkg+"_"+strings.ReplaceAll(version, ":", "%3a")+"_*.deb")
}

// save puts pkg=version into the keep dir and returns its path. Every failed
// attempt is logged.
func (r *rollbackSaver) save(pkg, version string) (string, error) {
	found := func() string {
		debs, _ := filepath.Glob(debGlob(r.keep, pkg, version))
		if len(debs) == 1 {
			return debs[0]
		}
		return ""
	}
	warn := func(from string, err error) {
		r.m.logger().Warn("updates: not saved for a rollback", "package", pkg, "version", version, "from", from, "error", err)
	}

	// 1. the current channel
	_, err := r.m.execIn(r.ctx, r.keep, "apt-get", "download", pkg+"="+version)
	if p := found(); err == nil && p != "" {
		return p, nil
	}
	if err == nil {
		err = fmt.Errorf("apt-get download wrote no %s file", pkg)
	}
	warn(r.channel()+" channel", err)

	// 2. apt's cache (the file name is only a hint: check the control fields)
	if p, err := r.fromCache(pkg, version); err == nil {
		return p, nil
	} else {
		warn("apt cache", err)
	}

	// 3. the full suite of the same repository
	if r.src.Suite == fullSuite {
		return "", fmt.Errorf("%s %s is not in the %s channel", pkg, version, r.channel())
	}
	if err := r.fullSuite(); err != nil {
		warn(fullSuite+" suite", err)
		return "", err
	}
	_, err = r.m.execIn(r.ctx, r.keep, "apt-get", append(r.privateOpts(), "download", pkg+"="+version)...)
	if p := found(); err == nil && p != "" {
		return p, nil
	}
	if err == nil {
		err = fmt.Errorf("apt-get download wrote no %s file", pkg)
	}
	warn(fullSuite+" suite", err)
	return "", err
}

func (r *rollbackSaver) fromCache(pkg, version string) (string, error) {
	debs, _ := filepath.Glob(debGlob(r.m.aptCache(), pkg, version))
	if len(debs) == 0 {
		return "", fmt.Errorf("no %s in %s", filepath.Base(debGlob("", pkg, version)), r.m.aptCache())
	}
	for _, deb := range debs {
		out, err := r.m.exec(r.ctx, "dpkg-deb", "-f", deb, "Package", "Version")
		if err != nil {
			continue
		}
		var gotPkg, gotVer string
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok {
				switch strings.TrimSpace(k) {
				case "Package":
					gotPkg = strings.TrimSpace(v)
				case "Version":
					gotVer = strings.TrimSpace(v)
				}
			}
		}
		if gotPkg != pkg || gotVer != version {
			continue
		}
		b, err := os.ReadFile(deb)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(r.keep, filepath.Base(deb))
		if err := os.WriteFile(dst, b, 0o640); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("no file in %s holds %s %s", r.m.aptCache(), pkg, version)
}

// privateOpts point apt at the full suite only, with its own lists and no
// package caches, so nothing of the box's APT state changes.
func (r *rollbackSaver) privateOpts() []string {
	return []string{
		"-o", "Dir::Etc::SourceList=" + filepath.Join(r.betaDir, "sources.list"),
		"-o", "Dir::Etc::SourceParts=-",
		"-o", "Dir::State::Lists=" + filepath.Join(r.betaDir, "lists"),
		"-o", "Dir::Cache::pkgcache=",
		"-o", "Dir::Cache::srcpkgcache=",
	}
}

// fullSuite prepares the private APT state once per update.
func (r *rollbackSaver) fullSuite() error {
	if r.betaRan {
		return r.betaErr
	}
	r.betaRan = true
	if r.src.URI == "" {
		r.betaErr = fmt.Errorf("no repository in %s", r.m.sourcesPath())
		return r.betaErr
	}
	os.RemoveAll(r.betaDir)
	if err := os.MkdirAll(filepath.Join(r.betaDir, "lists", "partial"), 0o750); err != nil {
		r.betaErr = err
		return err
	}
	opt := ""
	if r.src.SignedBy != "" {
		opt = "[signed-by=" + r.src.SignedBy + "] "
	}
	line := "deb " + opt + r.src.URI + " " + fullSuite + " main\n"
	if err := os.WriteFile(filepath.Join(r.betaDir, "sources.list"), []byte(line), 0o640); err != nil {
		r.betaErr = err
		return err
	}
	_, r.betaErr = r.m.exec(r.ctx, "apt-get", append(r.privateOpts(), "-q", "update")...)
	return r.betaErr
}

func (r *rollbackSaver) cleanup() { os.RemoveAll(r.betaDir) }
