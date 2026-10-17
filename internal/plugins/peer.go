package plugins

import (
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// unitFromCgroup returns the plugin whose unit's cgroup a /proc/<pid>/cgroup
// file names (cgroup v2: the "0::" line), e.g.
// "0::/system.slice/system-goblin\x2dplugin.slice/goblin-plugin@hello.service".
// The unit must be the last path element: plugin units are not delegated
// cgroups, so their processes never sit below it.
func unitFromCgroup(data string) (string, bool) {
	for _, line := range strings.Split(data, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		last := path[strings.LastIndex(path, "/")+1:]
		name, ok := strings.CutPrefix(last, "goblin-plugin@")
		if !ok {
			return "", false
		}
		name, ok = strings.CutSuffix(name, ".service")
		if !ok || !config.ValidPluginName(name) {
			return "", false
		}
		return name, true
	}
	return "", false
}
