package pluginsdk

import (
	"reflect"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/api"
	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/gateways"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/stats"
)

// jsonFields maps a struct's JSON field names (embedded structs flattened) to
// their Go kinds.
func jsonFields(t reflect.Type) map[string]reflect.Kind {
	out := map[string]reflect.Kind{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" {
			for k, v := range jsonFields(f.Type) {
				out[k] = v
			}
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type.Kind()
	}
	return out
}

// TestTypesMatchGoblind checks every SDK response field against the type
// goblind actually answers with: same JSON name, same Go kind. A field
// renamed or retyped in goblind breaks this test before it breaks a plugin.
func TestTypesMatchGoblind(t *testing.T) {
	type blockAnswer struct { // GET /api/v1/blocks entries (api.blockView)
		blocks.Block
		ExpiresIn int `json:"expires_in"`
	}
	pairs := []struct {
		sdk, server any
	}{
		{Event{}, logs.StoredEvent{}},
		{System{}, stats.System{}},
		{WAN{}, api.WANStatus{}},
		{Gateway{}, gateways.Status{}},
		{GatewayReport{}, gateways.Report{}},
		{Block{}, blockAnswer{}},
		{Device{}, api.DeviceView{}},
		{Policy{}, api.PluginPolicy{}},
		{LiveObject{}, api.LiveObjectView{}},
	}
	for _, p := range pairs {
		st, gt := reflect.TypeOf(p.sdk), reflect.TypeOf(p.server)
		server := jsonFields(gt)
		for name, kind := range jsonFields(st) {
			sk, ok := server[name]
			if !ok {
				t.Errorf("%s.%s: goblind's %s has no such field", st.Name(), name, gt)
				continue
			}
			if sk != kind {
				t.Errorf("%s.%s is a %s, goblind's %s has a %s", st.Name(), name, kind, gt, sk)
			}
		}
	}
}
