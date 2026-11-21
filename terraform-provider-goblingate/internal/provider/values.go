package provider

import (
	"maps"
	"math"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

// Helpers between the config's generic JSON and Terraform values.

// list returns cfg[key] as a list of objects.
func list(cfg client.Config, keys ...string) []map[string]any {
	var cur any = map[string]any(cfg)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	items, _ := cur.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// object returns cfg[keys...] as an object.
func object(cfg client.Config, keys ...string) map[string]any {
	var cur any = map[string]any(cfg)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	m, _ := cur.(map[string]any)
	return m
}

// findBy finds the item of a list whose field equals value.
func findBy(items []map[string]any, field, value string, fold bool) (map[string]any, bool) {
	for _, it := range items {
		s, _ := it[field].(string)
		if s == value || fold && strings.EqualFold(s, value) {
			return it, true
		}
	}
	return nil, false
}

// merged copies existing (an object or nil) and replaces the managed
// fields with set; a nil value in set drops the field.
func merged(existing any, set map[string]any) map[string]any {
	out := map[string]any{}
	if m, ok := existing.(map[string]any); ok {
		maps.Copy(out, m)
	}
	for k, v := range set {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

// optString is a string for JSON: nil (the field is dropped) when unset or "".
func optString(v types.String) any {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return nil
	}
	return v.ValueString()
}

// optInt is an int for JSON: nil when unset or 0.
func optInt(v types.Int64) any {
	if v.IsNull() || v.IsUnknown() || v.ValueInt64() == 0 {
		return nil
	}
	return v.ValueInt64()
}

// optStrings is a string set or list for JSON: nil when unset or empty.
func optStrings(v interface {
	IsNull() bool
	IsUnknown() bool
	Elements() []attr.Value
}) any {
	s := stringsOf(v)
	if len(s) == 0 {
		return nil
	}
	return s
}

func stringsOf(v interface {
	IsNull() bool
	IsUnknown() bool
	Elements() []attr.Value
}) []string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	var out []string
	for _, e := range v.Elements() {
		if s, ok := e.(types.String); ok && !s.IsNull() && !s.IsUnknown() {
			out = append(out, s.ValueString())
		}
	}
	return out
}

// readString reads a string field; "" or missing is null unless the prior
// value was "" (the configuration said "").
func readString(raw map[string]any, key string, prior types.String) types.String {
	s, _ := raw[key].(string)
	if s == "" {
		if !prior.IsNull() && !prior.IsUnknown() && prior.ValueString() == "" {
			return prior
		}
		return types.StringNull()
	}
	return types.StringValue(s)
}

// reqString reads a string field that is always there.
func reqString(raw map[string]any, key string) types.String {
	s, _ := raw[key].(string)
	return types.StringValue(s)
}

func readBool(raw map[string]any, key string) types.Bool {
	b, _ := raw[key].(bool)
	return types.BoolValue(b)
}

func readInt(raw map[string]any, key string) int64 {
	switch n := raw[key].(type) {
	case float64:
		if n == math.Trunc(n) {
			return int64(n)
		}
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// readOptInt reads a number field; 0 or missing is null unless the prior
// value was 0.
func readOptInt(raw map[string]any, key string, prior types.Int64) types.Int64 {
	n := readInt(raw, key)
	if n == 0 {
		if !prior.IsNull() && !prior.IsUnknown() && prior.ValueInt64() == 0 {
			return prior
		}
		return types.Int64Null()
	}
	return types.Int64Value(n)
}

func jsonStrings(raw map[string]any, key string) []string {
	items, _ := raw[key].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// readSet reads a string list as a set; empty or missing is null unless
// the prior value was an empty set.
func readSet(raw map[string]any, key string, prior types.Set) types.Set {
	s := jsonStrings(raw, key)
	if len(s) == 0 {
		if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
			return prior
		}
		if !prior.IsNull() && !prior.IsUnknown() {
			return types.SetValueMust(types.StringType, []attr.Value{})
		}
		return types.SetNull(types.StringType)
	}
	sort.Strings(s)
	return setOf(s)
}

func setOf(s []string) types.Set {
	vals := make([]attr.Value, len(s))
	for i, v := range s {
		vals[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, vals)
}

func listOf(s []string) types.List {
	vals := make([]attr.Value, len(s))
	for i, v := range s {
		vals[i] = types.StringValue(v)
	}
	return types.ListValueMust(types.StringType, vals)
}
