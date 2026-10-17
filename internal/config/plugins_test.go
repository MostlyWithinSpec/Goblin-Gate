package config

import (
	"strings"
	"testing"
)

func pluginErrors(errs []ValidationError) []string {
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e.Path, "plugins") {
			out = append(out, e.Path+": "+e.Message)
		}
	}
	return out
}

func TestValidatePluginFormat(t *testing.T) {
	cfg := Default()
	cfg.Plugins = []Plugin{
		{Name: "hello", Enabled: true, Capabilities: []string{"events:read", "root:all"}},
		{Name: "hello"},
		{Name: "Bad_Name"},
	}
	got := pluginErrors(Validate(cfg))
	want := []string{
		`plugins[0].capabilities[1]: unknown capability "root:all"`,
		`plugins[1].name: plugin "hello" is listed twice`,
		`plugins[2].name: plugin name must be 1-32 of a-z, 0-9 and -, starting with a letter`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCheckInstalledPlugins(t *testing.T) {
	cfg := Default()
	cfg.Plugins = []Plugin{
		{Name: "hello", Enabled: true, Capabilities: []string{"events:read", "config:read"}, Settings: map[string]any{"x": 1}},
		{Name: "missing", Enabled: true},
		{Name: "missing-off"},
		{Name: "broken", Enabled: true},
	}
	if errs := CheckInstalledPlugins(cfg); errs != nil {
		t.Fatalf("no catalog: %v", errs)
	}
	SetPluginCatalog(func(name string) (PluginSpec, bool) {
		switch name {
		case "hello":
			return PluginSpec{Capabilities: []string{"events:read"}, CheckSettings: func(s map[string]any) []string {
				if s["x"] != nil {
					return []string{"x: unknown setting"}
				}
				return nil
			}}, true
		case "broken":
			return PluginSpec{Problem: "plugin.json: bad"}, true
		}
		return PluginSpec{}, false
	})
	t.Cleanup(func() { SetPluginCatalog(nil) })

	if errs := pluginErrors(Validate(cfg)); len(errs) != 0 {
		t.Fatalf("Validate must not look at the installed plugins: %v", errs)
	}
	got := pluginErrors(CheckInstalledPlugins(cfg))
	want := []string{
		`plugins[0].capabilities[1]: plugin "hello" does not ask for "config:read"`,
		`plugins[0].settings: x: unknown setting`,
		`plugins[1].name: plugin "missing" is not installed`,
		`plugins[3].name: plugin "broken" cannot run: plugin.json: bad`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := len(pluginErrors(ValidateCandidate(cfg))); n != len(want) {
		t.Fatalf("ValidateCandidate: %d plugin errors", n)
	}
}
