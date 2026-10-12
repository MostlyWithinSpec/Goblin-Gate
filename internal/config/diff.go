package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// Change operation values.
const (
	OpAdd    = "add"
	OpRemove = "remove"
	OpChange = "change"
)

// Change is one difference between two configs: the change to apply at Path
// to turn a into b. Old and New hold JSON-native values (objects as
// map[string]any, arrays as []any, numbers as float64).
type Change struct {
	Path string `json:"path"`
	Op   string `json:"op"` // add | remove | change
	Old  any    `json:"old,omitempty"`
	New  any    `json:"new,omitempty"`
}

// Diff compares two configs and returns the changes that turn a into b.
// Both configs are compared as JSON trees, so unexported fields and Go-level
// details never matter. List items that carry a name (or an id, for
// policies) are matched by that key instead of by index, so reordering a
// list produces no changes.
func Diff(a, b *Config) []Change {
	va := jsonTree(a)
	vb := jsonTree(b)
	var out []Change
	diffValue(va, vb, "", &out)
	return out
}

// jsonTree converts cfg to its JSON value tree. Config only contains
// JSON-encodable fields, so failure is impossible in practice; if it ever
// happens the configs compare as equal (no changes reported).
func jsonTree(cfg *Config) any {
	data, err := cfg.Marshal()
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	return v
}

// diffValue appends the changes that turn av into bv at path.
func diffValue(av, bv any, path string, out *[]Change) {
	switch {
	case isMap(av) && isMap(bv):
		diffObject(av.(map[string]any), bv.(map[string]any), path, out)
	case isSlice(av) && isSlice(bv):
		diffArray(av.([]any), bv.([]any), path, out)
	default:
		if !reflect.DeepEqual(av, bv) {
			*out = append(*out, Change{Path: path, Op: OpChange, Old: av, New: bv})
		}
	}
}

func isMap(v any) bool   { _, ok := v.(map[string]any); return ok }
func isSlice(v any) bool { _, ok := v.([]any); return ok }

// diffObject walks the sorted union of both key sets so output is stable.
// Keys present on only one side produce a single add/remove change for the
// whole subtree.
func diffObject(am, bm map[string]any, path string, out *[]Change) {
	keys := make(map[string]bool, len(am)+len(bm))
	for k := range am {
		keys[k] = true
	}
	for k := range bm {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		p := joinPath(path, k)
		aVal, inA := am[k]
		bVal, inB := bm[k]
		switch {
		case !inA:
			*out = append(*out, Change{Path: p, Op: OpAdd, New: bVal})
		case !inB:
			*out = append(*out, Change{Path: p, Op: OpRemove, Old: aVal})
		default:
			diffValue(aVal, bVal, p, out)
		}
	}
}

// diffArray matches items by their key field when both sides (or at least one
// non-empty side) use keyed object items; otherwise it falls back to index
// matching.
func diffArray(al, bl []any, path string, out *[]Change) {
	field, ok := arrayKeyField(al)
	if !ok {
		field, ok = arrayKeyField(bl)
	}
	if ok {
		diffArrayKeyed(al, bl, field, path, out)
		return
	}
	n := len(al)
	if len(bl) > n {
		n = len(bl)
	}
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(bl):
			*out = append(*out, Change{Path: p, Op: OpRemove, Old: al[i]})
		case i >= len(al):
			*out = append(*out, Change{Path: p, Op: OpAdd, New: bl[i]})
		default:
			diffValue(al[i], bl[i], p, out)
		}
	}
}

// arrayKeyField reports the field to use as item key when every element of l
// is an object carrying a usable key: a numeric "id" (policies, which can be
// renamed but keep their id) or else a non-empty string "name". An empty list
// has no key.
func arrayKeyField(l []any) (string, bool) {
	for _, field := range []string{"id", "name"} {
		if len(l) > 0 && allKeyed(l, field) {
			return field, true
		}
	}
	return "", false
}

func allKeyed(l []any, field string) bool {
	for _, v := range l {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if field == "id" {
			if _, ok := m["id"].(float64); !ok {
				return false
			}
		} else if s, ok := m["name"].(string); !ok || s == "" {
			return false
		}
	}
	return true
}

// diffArrayKeyed merges two keyed lists: removes and changes are reported in
// a's order, adds in b's order.
func diffArrayKeyed(al, bl []any, field, path string, out *[]Change) {
	aItems, aOrder := keyedMap(al, field)
	bItems, bOrder := keyedMap(bl, field)
	for _, k := range aOrder {
		if _, inB := bItems[k]; !inB {
			*out = append(*out, Change{Path: itemPath(path, field, k), Op: OpRemove, Old: aItems[k]})
		}
	}
	for _, k := range aOrder {
		if bItem, inB := bItems[k]; inB {
			diffValue(aItems[k], bItem, itemPath(path, field, k), out)
		}
	}
	for _, k := range bOrder {
		if _, inA := aItems[k]; !inA {
			*out = append(*out, Change{Path: itemPath(path, field, k), Op: OpAdd, New: bItems[k]})
		}
	}
}

// keyedMap indexes the list by its key field; the first occurrence of a key
// wins and order is preserved.
func keyedMap(l []any, field string) (map[string]any, []string) {
	items := make(map[string]any, len(l))
	var order []string
	for _, v := range l {
		k := keyOf(v.(map[string]any), field)
		if _, dup := items[k]; !dup {
			order = append(order, k)
		}
		items[k] = v
	}
	return items, order
}

func keyOf(m map[string]any, field string) string {
	if field == "id" {
		f, _ := m["id"].(float64)
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s, _ := m["name"].(string)
	return s
}

func itemPath(path, field, key string) string {
	return fmt.Sprintf("%s[%s=%s]", path, field, key)
}

// joinPath appends a map/struct key to a base path.
func joinPath(base, key string) string {
	if base == "" {
		return seg(key)
	}
	return base + "." + seg(key)
}

// seg renders a key as a path segment; keys that are not plain identifiers
// (e.g. a hostname in dns.local_records) are quoted in brackets so dotted
// names stay unambiguous.
func seg(k string) string {
	if isPlainKey(k) {
		return k
	}
	return "[" + k + "]"
}

func isPlainKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
