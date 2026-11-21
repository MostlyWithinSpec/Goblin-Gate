package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestRolesLifecycle(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")

	out := b.expect("GET", "/api/v1/roles", nil, 200)
	if roles := out["roles"].([]any); len(roles) != 2 || len(out["scopes"].([]any)) != 6 {
		t.Fatalf("roles %v", out)
	}

	// validation
	b.expect("POST", "/api/v1/roles", map[string]any{"name": "admin", "scopes": []string{}}, 422)
	b.expect("POST", "/api/v1/roles", map[string]any{"name": "bad name", "scopes": []string{}}, 422)
	b.expect("POST", "/api/v1/roles", map[string]any{"name": "helpdesk", "scopes": []string{"root"}}, 422)
	b.expect("POST", "/api/v1/roles", map[string]any{"name": "helpdesk", "scopes": []string{"actions", "actions"}}, 422)

	b.expect("POST", "/api/v1/roles", map[string]any{"name": "helpdesk", "description": "Look and block", "scopes": []string{"actions"}}, 201)
	b.expect("POST", "/api/v1/roles", map[string]any{"name": "helpdesk", "scopes": []string{}}, 409)
	run, _ := a.store.Running()
	cand, _ := a.store.Candidate()
	if len(run.Roles) != 1 || len(cand.Roles) != 1 {
		t.Fatalf("running %v candidate %v", run.Roles, cand.Roles)
	}

	// give it to someone, then it can't be deleted
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "role": "helpdesk", "password": testPassword}, 201)
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "eve", "role": "nosuch", "password": testPassword}, 422)
	b.expect("DELETE", "/api/v1/roles/helpdesk", nil, 409)
	b.expect("DELETE", "/api/v1/roles/admin", nil, 409)
	b.expect("PATCH", "/api/v1/roles/readonly", map[string]any{"scopes": []string{"commit"}}, 409)
	b.expect("PATCH", "/api/v1/roles/nosuch", map[string]any{"scopes": []string{}}, 404)
	b.expect("PATCH", "/api/v1/roles/helpdesk", map[string]any{"scopes": []string{"bogus"}}, 422)

	out = b.expect("GET", "/api/v1/roles", nil, 200)
	hd := out["roles"].([]any)[2].(map[string]any)
	if hd["name"] != "helpdesk" || jsonString(hd["users"]) != `["kim"]` || jsonString(hd["scopes"]) != `["read","actions"]` {
		t.Fatalf("helpdesk %v", hd)
	}

	b.expect("PATCH", "/api/v1/admins/kim", map[string]string{"role": "readonly"}, 200)
	// single sign-on mapping to it also keeps it
	run, _ = a.store.Running()
	run.SSO = config.SSO{Roles: []config.SSORoleMap{{Group: "help", Role: "helpdesk"}}}
	a.store.SaveRevision(run, "test", "sso")
	b.expect("DELETE", "/api/v1/roles/helpdesk", nil, 409)
	run.SSO = config.SSO{}
	a.store.SaveRevision(run, "test", "no sso")
	b.expect("DELETE", "/api/v1/roles/helpdesk", nil, 200)
	b.expect("DELETE", "/api/v1/roles/helpdesk", nil, 404)

	// a non-admin can list roles (the web needs labels) but not change them
	v := a.browser()
	v.login("viewer")
	v.expect("GET", "/api/v1/roles", nil, 200)
	v.expect("POST", "/api/v1/roles", map[string]any{"name": "x", "scopes": []string{}}, 403)
}

// TestCustomRoleAccess: a custom role gets exactly its scopes through the
// same checks as scoped tokens, keeps self-service, and follows changes to
// the role at once.
func TestCustomRoleAccess(t *testing.T) {
	a := newAuthAPI(t)
	admin := a.browser()
	admin.login("admin")
	withBlocks(t, a.server)
	admin.expect("POST", "/api/v1/roles", map[string]any{"name": "helpdesk", "scopes": []string{"actions"}}, 201)
	admin.expect("POST", "/api/v1/roles", map[string]any{"name": "fwops", "scopes": []string{"write:firewall", "commit"}}, 201)
	admin.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "role": "helpdesk", "password": testPassword}, 201)
	admin.expect("POST", "/api/v1/admins", map[string]string{"username": "ops", "role": "fwops", "password": testPassword}, 201)

	kim := a.browser()
	kim.login("kim")
	me := kim.expect("GET", "/api/v1/auth/me", nil, 200)
	if me["role"] != "helpdesk" || jsonString(me["permissions"]) != `["read","actions"]` || me["full_admin"] != false {
		t.Fatalf("me %v", me)
	}
	kim.expect("GET", "/api/v1/config/running", nil, 200)
	kim.expect("POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.7", "minutes": 60, "reason": "test"}, 201)
	kim.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "schedules", "value": []any{}}, 403)
	kim.expect("POST", "/api/v1/commit", map[string]any{}, 403)
	kim.expect("POST", "/api/v1/admins", map[string]string{"username": "eve", "password": testPassword}, 403)
	kim.expect("POST", "/api/v1/roles", map[string]any{"name": "x", "scopes": []string{}}, 403)
	kim.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "escalate"}, 403)
	kim.expect("POST", "/api/v1/system/updates/apply", nil, 403)
	kim.expect("POST", "/api/v1/config/validate", nil, 200)
	if got := kim.expect("GET", "/api/v1/config/candidate", nil, 200); strings.Contains(jsonString(got), "$argon2") {
		t.Fatal("a custom role sees password hashes")
	}
	// self-service still works
	kim.expect("POST", "/api/v1/auth/2fa/setup", nil, 200)

	ops := a.browser()
	ops.login("ops")
	ops.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "schedules", "value": []any{}}, 200)
	ops.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "interfaces[name=lan].mtu", "value": 1400}, 403)
	ops.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "roles", "value": []any{}}, 403)
	ops.expect("POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.8", "minutes": 60, "reason": "test"}, 403)

	// the role changes: kim gets write:firewall with her next request, no new login
	admin.expect("PATCH", "/api/v1/roles/helpdesk", map[string]any{"scopes": []string{"write:firewall"}}, 200)
	kim.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "schedules", "value": []any{}}, 200)
	kim.expect("POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.9", "minutes": 60, "reason": "test"}, 403)

	// giving kim another role ends her sessions
	admin.expect("PATCH", "/api/v1/admins/kim", map[string]string{"role": "fwops"}, 200)
	kim.expect("GET", "/api/v1/auth/me", nil, 401)
}

func TestCustomRoleConfigValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Admins = []config.Admin{{Username: "admin", Role: "admin", PasswordHash: "x"}, {Username: "kim", Role: "helpdesk", PasswordHash: "x"}}
	errs := config.Validate(cfg)
	if !slices.ContainsFunc(errs, func(e config.ValidationError) bool { return e.Path == "admins[1].role" }) {
		t.Fatalf("unknown role accepted: %v", errs)
	}
	cfg.Roles = []config.Role{{Name: "helpdesk", Scopes: []string{"actions"}}, {Name: "helpdesk", Scopes: []string{}}, {Name: "readonly", Scopes: []string{}}}
	errs = config.Validate(cfg)
	var paths []string
	for _, e := range errs {
		if strings.HasPrefix(e.Path, "roles") || strings.HasPrefix(e.Path, "admins") {
			paths = append(paths, e.Path)
		}
	}
	if jsonString(paths) != `["roles[1].name","roles[2].name"]` {
		t.Fatalf("errors %v", errs)
	}
}
