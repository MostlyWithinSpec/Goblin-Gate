package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SegmentKind says how a path segment selects its child.
type SegmentKind int

// Segment kinds.
const (
	SegField SegmentKind = iota // object key:          system, hostname, [my.host]
	SegIndex                    // list position:       [3]
	SegMatch                    // list item by key:    [id=10], [name=lan]
)

// PathSegment is one step of a config path.
type PathSegment struct {
	Kind  SegmentKind
	Key   string // field name (SegField) or match field (SegMatch)
	Value string // match value (SegMatch)
	Index int    // position (SegIndex)
}

// ParsePath parses the path syntax produced by Diff, for example
//
//	system.hostname
//	policies[id=10].action
//	addresses[name=web-server].value
//	interfaces[0].mtu
//	dns.local_records.[nas.home.lan]
//
// Bracket contents are: a non-negative integer (list index), field=value (list
// item whose field equals value; quotes around value are optional), or any
// other text (object key that is not a plain identifier).
func ParsePath(path string) ([]PathSegment, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path: empty path")
	}
	var segs []PathSegment
	i := 0
	for i < len(path) {
		switch c := path[i]; {
		case c == '.':
			if i == 0 || i == len(path)-1 || path[i+1] == '.' {
				return nil, fmt.Errorf("path %q: misplaced '.' at %d", path, i)
			}
			i++
		case c == '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q: missing ']'", path)
			}
			seg, err := parseBracket(path[i+1 : i+end])
			if err != nil {
				return nil, fmt.Errorf("path %q: %w", path, err)
			}
			segs = append(segs, seg)
			i += end + 1
			if i < len(path) && path[i] != '.' && path[i] != '[' {
				return nil, fmt.Errorf("path %q: unexpected %q after ']'", path, path[i])
			}
		default:
			j := i
			for j < len(path) && isPlainByte(path[j]) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("path %q: unexpected %q at %d", path, c, i)
			}
			segs = append(segs, PathSegment{Kind: SegField, Key: path[i:j]})
			i = j
		}
	}
	return segs, nil
}

func isPlainByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

func parseBracket(s string) (PathSegment, error) {
	if s == "" {
		return PathSegment{}, fmt.Errorf("empty []")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return PathSegment{}, fmt.Errorf("negative index %d", n)
		}
		return PathSegment{Kind: SegIndex, Index: n}, nil
	}
	if k, v, ok := strings.Cut(s, "="); ok {
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		if k == "" || v == "" {
			return PathSegment{}, fmt.Errorf("bad match %q (want field=value)", s)
		}
		return PathSegment{Kind: SegMatch, Key: k, Value: v}, nil
	}
	return PathSegment{Kind: SegField, Key: strings.Trim(s, `"`)}, nil
}

// SetPath returns a copy of cfg with the value at path replaced by value
// (any JSON-compatible value: string, number, bool, map, slice). Missing
// object keys are created; a [field=value] match that finds nothing appends a
// new list item (the field is filled in when value is an object); an index
// equal to the list length appends. The result must still decode into a
// Config, so unknown fields and wrong types are rejected.
func SetPath(cfg *Config, path string, value any) (*Config, error) {
	return editPath(cfg, path, func(parent any, last PathSegment) (any, error) {
		return setChild(parent, last, normalize(value))
	})
}

// DeletePath returns a copy of cfg with the value at path removed: an object
// key is dropped, a list item is cut out. Deleting something that does not
// exist is an error.
func DeletePath(cfg *Config, path string) (*Config, error) {
	return editPath(cfg, path, deleteChild)
}

type editFunc func(parent any, last PathSegment) (any, error)

func editPath(cfg *Config, path string, edit editFunc) (*Config, error) {
	segs, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	root := jsonTree(cfg)
	if root == nil {
		return nil, fmt.Errorf("path: cannot encode config")
	}
	newRoot, err := editAt(root, segs, edit)
	if err != nil {
		return nil, fmt.Errorf("path %q: %w", path, err)
	}
	data, err := json.Marshal(newRoot)
	if err != nil {
		return nil, fmt.Errorf("path %q: %w", path, err)
	}
	out, err := Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("path %q: result is not a valid config: %w", path, err)
	}
	return out, nil
}

// editAt walks segs from node and applies edit to the parent of the last
// segment. It returns the (possibly replaced) node so list appends and
// deletions propagate upwards.
func editAt(node any, segs []PathSegment, edit editFunc) (any, error) {
	if len(segs) == 1 {
		return edit(node, segs[0])
	}
	child, err := getChild(node, segs[0], true)
	if err != nil {
		return nil, err
	}
	newChild, err := editAt(child, segs[1:], edit)
	if err != nil {
		return nil, err
	}
	return setChild(node, segs[0], newChild)
}

// getChild returns the child selected by seg. With create, a missing object
// key yields nil and setChild then builds an object or list as needed.
func getChild(node any, seg PathSegment, create bool) (any, error) {
	switch seg.Kind {
	case SegField:
		m, ok := node.(map[string]any)
		if !ok {
			if node == nil && create {
				return nil, nil
			}
			return nil, fmt.Errorf("%s: not an object", seg.Key)
		}
		v, ok := m[seg.Key]
		if !ok || v == nil {
			if create {
				return nil, nil // created by setChild with the right shape
			}
			return nil, fmt.Errorf("%s: not found", seg.Key)
		}
		return v, nil
	case SegIndex:
		l, ok := node.([]any)
		if !ok || seg.Index >= len(l) {
			return nil, fmt.Errorf("[%d]: not found", seg.Index)
		}
		return l[seg.Index], nil
	default:
		l, _ := node.([]any)
		if i := findMatch(l, seg); i >= 0 {
			return l[i], nil
		}
		return nil, fmt.Errorf("[%s=%s]: not found", seg.Key, seg.Value)
	}
}

func setChild(node any, seg PathSegment, value any) (any, error) {
	switch seg.Kind {
	case SegField:
		m, ok := node.(map[string]any)
		if !ok {
			if node != nil {
				return nil, fmt.Errorf("%s: parent is not an object", seg.Key)
			}
			m = map[string]any{}
		}
		m[seg.Key] = value
		return m, nil
	case SegIndex:
		l, ok := node.([]any)
		if !ok && node != nil {
			return nil, fmt.Errorf("[%d]: parent is not a list", seg.Index)
		}
		switch {
		case seg.Index < len(l):
			l[seg.Index] = value
		case seg.Index == len(l):
			l = append(l, value)
		default:
			return nil, fmt.Errorf("[%d]: index out of range (len %d)", seg.Index, len(l))
		}
		return l, nil
	default:
		l, ok := node.([]any)
		if !ok && node != nil {
			return nil, fmt.Errorf("[%s=%s]: parent is not a list", seg.Key, seg.Value)
		}
		if obj, ok := value.(map[string]any); ok {
			if _, has := obj[seg.Key]; !has {
				obj[seg.Key] = matchValue(seg)
			}
		}
		if i := findMatch(l, seg); i >= 0 {
			l[i] = value
		} else {
			l = append(l, value)
		}
		return l, nil
	}
}

func deleteChild(node any, seg PathSegment) (any, error) {
	switch seg.Kind {
	case SegField:
		m, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: not found", seg.Key)
		}
		if _, has := m[seg.Key]; !has {
			return nil, fmt.Errorf("%s: not found", seg.Key)
		}
		delete(m, seg.Key)
		return m, nil
	case SegIndex:
		l, _ := node.([]any)
		if seg.Index >= len(l) {
			return nil, fmt.Errorf("[%d]: not found", seg.Index)
		}
		return append(l[:seg.Index], l[seg.Index+1:]...), nil
	default:
		l, _ := node.([]any)
		i := findMatch(l, seg)
		if i < 0 {
			return nil, fmt.Errorf("[%s=%s]: not found", seg.Key, seg.Value)
		}
		return append(l[:i], l[i+1:]...), nil
	}
}

func findMatch(l []any, seg PathSegment) int {
	for i, item := range l {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch v := m[seg.Key].(type) {
		case string:
			if v == seg.Value {
				return i
			}
		case float64:
			if strconv.FormatFloat(v, 'f', -1, 64) == seg.Value {
				return i
			}
		}
	}
	return -1
}

// matchValue is the JSON value for a match key: numbers stay numbers (policy
// ids), everything else is a string.
func matchValue(seg PathSegment) any {
	if f, err := strconv.ParseFloat(seg.Value, 64); err == nil && seg.Key == "id" {
		return f
	}
	return seg.Value
}

// normalize turns any Go value into the plain JSON tree form (maps, slices,
// float64) so it can be stored in the tree.
func normalize(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}
