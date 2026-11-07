package api

import (
	"encoding"
	"encoding/json"
	"net/http"
	"net/netip"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
)

// The OpenAPI document of /api/v1 is built from the route table itself:
// routeDocs describes every route (TestOpenAPICoverage fails for a route
// without one), schemas come from the Go types the handlers decode and
// encode, and scopes from routeScopes. docs/api/openapi.json is the same
// document, kept in the repo (TestOpenAPIFile fails when it is stale; run
// `go test ./internal/api -run TestOpenAPIFile -update` to write it).

// routeDoc describes one route for the OpenAPI document.
type routeDoc struct {
	Tag     string
	Summary string
	Desc    string
	Query   []queryParam
	Body    any    // JSON request body: a schema spec (see schemaGen.spec); nil = none
	Upload  string // a non-JSON request body instead, e.g. "application/zip"
	Resp    any    // JSON response body (schema spec); nil with Content "" = no body
	Status  int    // success status; 0 = 200
	Content string // a non-JSON response instead, e.g. "text/event-stream"
	Open    bool   // no authentication
}

type queryParam struct {
	Name string
	Desc string
	Type any // schema spec; nil = string
}

// field and obj describe a JSON object built in a handler (map literals):
// obj{{"status", str}, {"revision", integer}}.
type field struct {
	Name string
	Spec any
}
type obj []field

// arrayOf is a JSON array of spec.
type arrayOf struct{ Spec any }

// mapOf is a JSON object with spec values under any key.
type mapOf struct{ Spec any }

// anyJSON is any JSON value; anyObject any JSON object.
type (
	anyJSON   struct{}
	anyObject struct{}
)

// common schema specs
var (
	str     = reflect.TypeFor[string]()
	integer = reflect.TypeFor[int]()
	boolean = reflect.TypeFor[bool]()
	number  = reflect.TypeFor[float64]()
	strs    = reflect.TypeFor[[]string]()
	when    = reflect.TypeFor[time.Time]()
)

// typ is the schema of T; ret the schema of what a function returns first
// (a method expression like (*updates.Manager).Check): the spec follows the
// code when its types change.
func typ[T any]() reflect.Type { return reflect.TypeFor[T]() }

func ret(fn any) reflect.Type { return reflect.TypeOf(fn).Out(0) }

// status is the {"status": "..."} answer of most actions.
var status = obj{{"status", str}}

// openAPIVersion is the API's version in the document (not goblind's).
const openAPIVersion = "1"

var (
	openAPIOnce sync.Once
	openAPIJSON []byte
)

// GET /api/v1/openapi.json (no authentication) → the OpenAPI 3.1 document.
func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	openAPIOnce.Do(func() {
		b, err := json.MarshalIndent(buildOpenAPI(), "", "  ")
		if err != nil {
			panic("openapi: " + err.Error()) // static data: caught by the tests
		}
		openAPIJSON = append(b, '\n')
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(openAPIJSON)
}

// apiPatterns lists every route: the authenticated ones and the open ones.
func apiPatterns() []string {
	var out []string
	s := &Server{}
	s.register(func(p string, _ http.HandlerFunc) { out = append(out, p) })
	s.openRoutes(func(p string, _ http.Handler) { out = append(out, p) })
	sort.Strings(out)
	return out
}

var pathParam = regexp.MustCompile(`\{(\w+)(\.\.\.)?\}`)

// buildOpenAPI assembles the document.
func buildOpenAPI() map[string]any {
	g := &schemaGen{comps: map[string]any{}, names: map[reflect.Type]string{}}
	paths := map[string]any{}
	tags := map[string]bool{}
	for _, p := range apiPatterns() {
		doc, ok := routeDocs[p]
		if !ok {
			continue // TestOpenAPICoverage reports it
		}
		method, route, hasMethod := strings.Cut(p, " ")
		methods := []string{method}
		if !hasMethod { // any method (the plugin API proxy)
			route = p
			methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
		}
		oaPath := pathParam.ReplaceAllString(route, "{$1}")
		item, _ := paths[oaPath].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[oaPath] = item
		}
		for _, m := range methods {
			item[strings.ToLower(m)] = g.operation(p, m, route, doc)
		}
		tags[doc.Tag] = true
	}
	var tagList []any
	for _, t := range sortedKeys(tags) {
		tagList = append(tagList, map[string]any{"name": t, "description": tagDescriptions[t]})
	}
	g.comps["ErrorBody"] = g.spec(typ[ErrorBody]())
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Goblin Gate API",
			"version": openAPIVersion,
			"description": "The API the Goblin Gate web UI uses. Authenticate with an API token " +
				"(System → API Tokens) as `Authorization: Bearer <token>`, or with the web UI's session cookie " +
				"plus the `" + CSRFHeader + "` header (the value of the `" + CSRFCookie + "` cookie) on every " +
				"request that changes something. Configuration changes go to the candidate configuration and take " +
				"effect with POST /api/v1/commit. `x-goblin-scope` names the token scope an operation needs: " +
				"`admin` = full admin tokens only, `candidate` = the scope of the configuration section changed.",
			"license": map[string]any{"name": "GPL-3.0-or-later", "identifier": "GPL-3.0-or-later"},
		},
		"servers":  []any{map[string]any{"url": "/", "description": "the box itself"}},
		"tags":     tagList,
		"paths":    paths,
		"security": []any{map[string]any{"token": []any{}}, map[string]any{"session": []any{}}},
		"components": map[string]any{
			"schemas": g.comps,
			"securitySchemes": map[string]any{
				"token":   map[string]any{"type": "http", "scheme": "bearer", "description": "An API token (System → API Tokens)."},
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": SessionCookie, "description": "The web UI's login session."},
			},
		},
	}
}

func (g *schemaGen) operation(pattern, method, route string, doc routeDoc) map[string]any {
	op := map[string]any{
		"tags":        []any{doc.Tag},
		"summary":     doc.Summary,
		"operationId": operationID(method, route),
	}
	if doc.Desc != "" {
		op["description"] = doc.Desc
	}
	var params []any
	for _, m := range pathParam.FindAllStringSubmatch(route, -1) {
		d := ""
		if m[2] != "" {
			d = "the rest of the path, slashes included"
		}
		params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "description": d, "schema": map[string]any{"type": "string"}})
	}
	for _, q := range doc.Query {
		t := q.Type
		if t == nil {
			t = str
		}
		params = append(params, map[string]any{"name": q.Name, "in": "query", "description": q.Desc, "schema": g.spec(t)})
	}
	if params != nil {
		op["parameters"] = params
	}
	switch {
	case doc.Upload != "":
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{doc.Upload: map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}
	case doc.Body != nil:
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": g.spec(doc.Body)}}}
	}
	code := doc.Status
	if code == 0 {
		code = http.StatusOK
	}
	ok := map[string]any{"description": http.StatusText(code)}
	switch {
	case doc.Content != "":
		ok["content"] = map[string]any{doc.Content: map[string]any{"schema": map[string]any{"type": "string"}}}
	case doc.Resp != nil:
		ok["content"] = map[string]any{"application/json": map[string]any{"schema": g.spec(doc.Resp)}}
	}
	op["responses"] = map[string]any{
		strconv.Itoa(code): ok,
		"4XX":              errorResponse("The request was refused: see error.code"),
		"default":          errorResponse("An error"),
	}
	if doc.Open {
		op["security"] = []any{}
	} else {
		op["x-goblin-scope"] = scopeOf(pattern, method)
	}
	return op
}

func errorResponse(desc string) map[string]any {
	return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/ErrorBody"}}}}
}

// tagDescriptions groups the operations in the document.
var tagDescriptions = map[string]string{
	"Admins":      "Admin accounts.",
	"Auth":        "Logging in, passwords, two-factor login, API tokens and sessions.",
	"Checks":      "Goblin Checks, the network audit.",
	"Config":      "The candidate and running configuration, commits, revisions, snapshots and imports.",
	"Diagnostics": "Ping, traceroute, DNS lookups, connections and packet captures.",
	"Logs":        "Stored and live events, statistics and log export.",
	"Meta":        "This document and the Prometheus metrics.",
	"Network":     "Live network state: DHCP leases, UPnP, gateways, WAN, devices, dynamic DNS.",
	"Plugins":     "Plugins and their own APIs.",
	"Policy":      "Policy tools.",
	"Security":    "Threat feeds, GeoIP, CrowdSec, IPS, web filter, application control and temporary blocks.",
	"Setup":       "The first-boot setup.",
	"System":      "Status, backups, updates, restarts, UPS and configuration history in git.",
	"Templates":   "Configuration templates.",
	"UI":          "Per-admin documents of the web UI.",
	"VPN":         "Tailscale and WireGuard.",
}

// scopeOf is the token scope an operation needs (see scopes.go).
func scopeOf(pattern, method string) string {
	if sc, ok := routeScopes[pattern]; ok {
		return sc
	}
	if method == http.MethodGet {
		return auth.ScopeRead
	}
	return scopeAdmin
}

// operationID turns "GET /api/v1/vpn/wireguard/{name}/peers" into
// "getVpnWireguardNamePeers".
func operationID(method, route string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(route, "/api/v1/"), func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '{' || r == '}' || r == '.'
	}) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// schemaGen turns Go types into JSON Schema, with named structs as shared
// components.
type schemaGen struct {
	comps map[string]any
	names map[reflect.Type]string
}

var (
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	rawMessage    = reflect.TypeFor[json.RawMessage]()
	formats       = map[reflect.Type]string{
		reflect.TypeFor[time.Time]():      "date-time",
		reflect.TypeFor[netip.Addr]():     "ip",
		reflect.TypeFor[netip.Prefix]():   "cidr",
		reflect.TypeFor[netip.AddrPort](): "ip:port",
	}
)

// spec is the schema of a spec: a reflect.Type, obj, arrayOf, mapOf,
// anyJSON, anyObject or a ready schema (map[string]any).
func (g *schemaGen) spec(s any) map[string]any {
	switch v := s.(type) {
	case reflect.Type:
		return g.typeSchema(v)
	case obj:
		props := map[string]any{}
		for _, f := range v {
			props[f.Name] = g.spec(f.Spec)
		}
		return map[string]any{"type": "object", "properties": props}
	case arrayOf:
		return map[string]any{"type": "array", "items": g.spec(v.Spec)}
	case mapOf:
		return map[string]any{"type": "object", "additionalProperties": g.spec(v.Spec)}
	case anyJSON:
		return map[string]any{}
	case anyObject:
		return map[string]any{"type": "object"}
	case map[string]any:
		return v
	}
	panic("openapi: unknown schema spec " + reflect.TypeOf(s).String())
}

func (g *schemaGen) typeSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if f, ok := formats[t]; ok {
		return map[string]any{"type": "string", "format": f}
	}
	if t == rawMessage {
		return map[string]any{}
	}
	if t.Kind() != reflect.String && (t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textMarshaler)) &&
		!t.Implements(jsonMarshaler) && !reflect.PointerTo(t).Implements(jsonMarshaler) {
		return map[string]any{"type": "string"}
	}
	if t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler) {
		return map[string]any{"description": "custom JSON (" + t.String() + ")"}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.typeSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.typeSchema(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		if t.Name() == "" {
			return g.structSchema(t)
		}
		name, ok := g.names[t]
		if !ok {
			name = componentName(t)
			g.names[t] = name
			g.comps[name] = map[string]any{} // placeholder for recursive types
			g.comps[name] = g.structSchema(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

// componentName is "<package>.<Type>", e.g. config.Policy.
func componentName(t reflect.Type) string {
	n := path.Base(t.PkgPath()) + "." + t.Name()
	return strings.NewReplacer("[", "_", "]", "", "/", "_", "*", "", " ", "").Replace(n)
}

func (g *schemaGen) structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	g.fields(t, props, &required)
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		sort.Strings(required)
		out["required"] = required
	}
	return out
}

// fields adds t's JSON fields as encoding/json writes them: tag names,
// embedded structs inlined, "-" skipped. A field without omitempty or
// omitzero is always written, so it is listed as required.
func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				g.fields(et, props, required)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		s := g.typeSchema(f.Type)
		if strings.Contains(","+opts+",", ",string,") {
			s = map[string]any{"type": "string"}
		}
		props[name] = s
		if !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,") {
			*required = append(*required, name)
		}
	}
}
