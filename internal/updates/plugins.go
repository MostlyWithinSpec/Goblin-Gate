package updates

import (
	"context"
	"strings"
)

// PluginPrefix names the first-party plugin packages. They are released with
// goblin-gate, under the same version, and update together with it.
const PluginPrefix = "goblin-gate-plugin-"

// PluginUpdate is an installed first-party plugin with a newer version in the
// channel.
type PluginUpdate struct {
	Package   string `json:"package"`
	Installed string `json:"installed"`
	Available string `json:"available"`
}

// installedPlugins returns the installed first-party plugin packages and
// their versions.
func (m *Manager) installedPlugins(ctx context.Context) map[string]string {
	out, err := m.exec(ctx, "dpkg-query", "-W", "-f=${Package} ${Version} ${db:Status-Abbrev}\n", PluginPrefix+"*")
	if err != nil && len(out) == 0 {
		return nil
	}
	plugins := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && strings.HasPrefix(f[0], PluginPrefix) && strings.HasPrefix(f[2], "ii") {
			plugins[f[0]] = f[1]
		}
	}
	return plugins
}

// candidate returns the version apt would install for pkg ("" = none).
func (m *Manager) candidate(ctx context.Context, pkg string) string {
	out, err := m.exec(ctx, "apt-cache", "policy", pkg)
	if err != nil {
		return ""
	}
	if mm := candidateRe.FindStringSubmatch(string(out)); mm != nil && mm[1] != "(none)" {
		return mm[1]
	}
	return ""
}

// pluginsBehind lists the installed plugins whose channel has a newer version.
func (m *Manager) pluginsBehind(ctx context.Context) []PluginUpdate {
	var out []PluginUpdate
	for pkg, v := range m.installedPlugins(ctx) {
		c := m.candidate(ctx, pkg)
		if c == "" || c == v {
			continue
		}
		if newer, _ := m.newer(ctx, c, v); newer {
			out = append(out, PluginUpdate{Package: pkg, Installed: v, Available: c})
		}
	}
	return out
}
