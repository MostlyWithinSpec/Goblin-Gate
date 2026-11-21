package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
)

// Role is a custom admin role: what its users may do besides reading
// everything. The built-in roles admin and readonly are not listed here.
type Role struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"` // from auth.Scopes; read is implied
}

// RoleScopes returns the scopes of role name with read first, and whether it
// is a custom role of cfg. Built-in roles return (nil, false).
func (cfg *Config) RoleScopes(name string) ([]string, bool) {
	for _, r := range cfg.Roles {
		if r.Name == name {
			out := []string{auth.ScopeRead}
			for _, sc := range r.Scopes {
				if sc != auth.ScopeRead {
					out = append(out, sc)
				}
			}
			return out, true
		}
	}
	return nil, false
}

// HasRole reports whether name is a built-in role or a custom role of cfg.
func (cfg *Config) HasRole(name string) bool {
	if auth.BuiltinRole(name) {
		return true
	}
	_, ok := cfg.RoleScopes(name)
	return ok
}

// CheckRole returns the problems of one role as (field, message) pairs, field
// relative to the role ("name", "scopes[1]").
func CheckRole(r Role) [][2]string {
	var out [][2]string
	add := func(field, format string, args ...any) {
		out = append(out, [2]string{field, fmt.Sprintf(format, args...)})
	}
	switch {
	case !ValidName(r.Name):
		add("name", "role name must be 1-63 letters, digits, \".\", \"_\" or \"-\"")
	case auth.BuiltinRole(r.Name):
		add("name", "%q is a built-in role", r.Name)
	}
	if len(r.Description) > 200 {
		add("description", "description is longer than 200 characters")
	}
	dup := map[string]bool{}
	for j, sc := range r.Scopes {
		if !slices.Contains(auth.Scopes, sc) {
			add(fmt.Sprintf("scopes[%d]", j), "unknown scope %q: want %s", sc, strings.Join(auth.Scopes, ", "))
		} else if dup[sc] {
			add(fmt.Sprintf("scopes[%d]", j), "duplicate scope %q", sc)
		}
		dup[sc] = true
	}
	return out
}

func (v *validator) roles() {
	seen := map[string]bool{}
	for i, r := range v.cfg.Roles {
		p := fmt.Sprintf("roles[%d]", i)
		for _, e := range CheckRole(r) {
			v.add(p+"."+e[0], "%s", e[1])
		}
		if seen[r.Name] {
			v.add(p+".name", "duplicate role %q", r.Name)
		}
		seen[r.Name] = true
	}
}
