package config

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
)

// SSO is single sign-on for the web UI with an OpenID Connect identity
// provider (Authentik, Keycloak, Authelia, Entra ID, Google...). Local
// accounts keep working next to it.
type SSO struct {
	Enabled bool `json:"enabled"`
	// Name is shown on the login button ("Sign in with <name>").
	Name string `json:"name,omitempty"`
	// Issuer is the provider's issuer URL; its discovery document is at
	// <issuer>/.well-known/openid-configuration.
	Issuer       string `json:"issuer,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"` // a secret; empty for a public client (PKCE only)
	// Scopes asked for besides openid (default: profile, email, groups).
	Scopes []string `json:"scopes,omitempty"`
	// UsernameClaim names the user in the gate (default preferred_username,
	// then email).
	UsernameClaim string `json:"username_claim,omitempty"`
	// GroupsClaim holds the user's groups (default groups).
	GroupsClaim string `json:"groups_claim,omitempty"`
	// Roles maps provider groups to gate roles; the first match wins.
	Roles []SSORoleMap `json:"roles,omitempty"`
	// DefaultRole is the role of a user in none of the groups; empty refuses
	// them.
	DefaultRole string `json:"default_role,omitempty"`
	// RedirectURL is the callback the provider sends users back to; empty =
	// https://<the address the browser used>/api/v1/auth/sso/callback.
	RedirectURL string `json:"redirect_url,omitempty"`
	// CACertificate is a PEM certificate to trust for the provider besides
	// the system roots: a private CA, or the gate's own internal CA root.
	CACertificate string `json:"ca_certificate,omitempty"`
}

// SSORoleMap gives the members of a provider group a role.
type SSORoleMap struct {
	Group string `json:"group"`
	Role  string `json:"role"`
}

func (v *validator) checkSSO() {
	s := v.cfg.SSO
	if !s.Enabled {
		return
	}
	if u, err := url.Parse(s.Issuer); err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
		v.add("sso.issuer", "issuer must be an https URL")
	}
	if s.ClientID == "" {
		v.add("sso.client_id", "client_id is required")
	}
	if len(s.Name) > 40 {
		v.add("sso.name", "name is longer than 40 characters")
	}
	for i, sc := range s.Scopes {
		if sc == "" || strings.ContainsAny(sc, " \t\r\n") {
			v.add(fmt.Sprintf("sso.scopes[%d]", i), "a scope is one word")
		}
	}
	for _, c := range []struct{ path, claim string }{{"sso.username_claim", s.UsernameClaim}, {"sso.groups_claim", s.GroupsClaim}} {
		if strings.ContainsAny(c.claim, " \t\r\n") {
			v.add(c.path, "a claim name is one word")
		}
	}
	if len(s.Roles) == 0 && s.DefaultRole == "" {
		v.add("sso.roles", "map at least one group to a role, or set default_role: otherwise nobody can sign in")
	}
	for i, m := range s.Roles {
		if m.Group == "" {
			v.add(fmt.Sprintf("sso.roles[%d].group", i), "group is required")
		}
		if !v.cfg.HasRole(m.Role) {
			v.add(fmt.Sprintf("sso.roles[%d].role", i), "unknown role %q", m.Role)
		}
	}
	if s.DefaultRole != "" && !v.cfg.HasRole(s.DefaultRole) {
		v.add("sso.default_role", "unknown role %q", s.DefaultRole)
	}
	if s.RedirectURL != "" {
		if u, err := url.Parse(s.RedirectURL); err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "/api/v1/auth/sso/callback" {
			v.add("sso.redirect_url", "redirect_url must be https://<the gate>/api/v1/auth/sso/callback")
		}
	}
	if s.CACertificate != "" {
		if b, _ := pem.Decode([]byte(s.CACertificate)); b == nil || b.Type != "CERTIFICATE" {
			v.add("sso.ca_certificate", "ca_certificate must be a PEM certificate")
		} else if _, err := x509.ParseCertificate(b.Bytes); err != nil {
			v.add("sso.ca_certificate", "ca_certificate: %v", err)
		}
	}
}

func isLoopbackHost(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }
