package auth

import (
	"slices"
	"testing"
)

func TestIdentityScopes(t *testing.T) {
	all := Scopes
	for _, c := range []struct {
		name      string
		id        Identity
		want      []string
		canWrite  bool
		isLimited bool
	}{
		{"admin", Identity{Role: "admin"}, all, true, false},
		{"readonly", Identity{Role: "readonly"}, []string{ScopeRead}, false, false},
		{"custom", Identity{Role: "helpdesk", RoleScopes: []string{ScopeRead, ScopeActions}}, []string{ScopeRead, ScopeActions}, false, true},
		{"custom without extra scopes", Identity{Role: "looker", RoleScopes: []string{ScopeRead}}, []string{ScopeRead}, false, true},
		// a token holds the intersection of its scopes and its owner's role
		{"admin token", Identity{Role: "admin", Scopes: []string{ScopeRead, ScopeCommit}}, []string{ScopeRead, ScopeCommit}, false, true},
		{"custom token", Identity{Role: "helpdesk", RoleScopes: []string{ScopeRead, ScopeActions}, Scopes: []string{ScopeRead, ScopeCommit}}, []string{ScopeRead}, false, true},
		{"demoted token", Identity{Role: "readonly", Scopes: []string{ScopeRead, ScopeCommit}}, []string{ScopeRead}, false, true},
		// a custom role named "admin" can't pass validation; if it did, its scopes rule
		{"custom named admin", Identity{Role: "admin", RoleScopes: []string{ScopeRead}}, []string{ScopeRead}, false, true},
		{"nobody", Identity{}, []string{}, false, false},
	} {
		if got := c.id.Effective(); !slices.Equal(got, c.want) {
			t.Errorf("%s: Effective = %v, want %v", c.name, got, c.want)
		}
		if c.id.CanWrite() != c.canWrite || c.id.Limited() != c.isLimited {
			t.Errorf("%s: CanWrite %v Limited %v", c.name, c.id.CanWrite(), c.id.Limited())
		}
	}
}
