package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var updateOpenAPI = flag.Bool("update", false, "rewrite docs/api/openapi.json")

const openAPIFile = "../../docs/api/openapi.json"

var refRe = regexp.MustCompile(`"\$ref":"#/components/schemas/([^"]+)"`)

// TestOpenAPICoverage: every route is documented, and every documented
// route exists.
func TestOpenAPICoverage(t *testing.T) {
	routes := map[string]bool{}
	for _, p := range apiPatterns() {
		routes[p] = true
		d, ok := routeDocs[p]
		if !ok {
			t.Errorf("route %q has no entry in routeDocs (internal/api/openapi_routes.go)", p)
			continue
		}
		if d.Tag == "" || d.Summary == "" {
			t.Errorf("route %q needs a tag and a summary", p)
		}
		if tagDescriptions[d.Tag] == "" {
			t.Errorf("route %q: tag %q has no entry in tagDescriptions", p, d.Tag)
		}
		if d.Resp != nil && d.Content != "" {
			t.Errorf("route %q has both a JSON and a %s response", p, d.Content)
		}
	}
	for p := range routeDocs {
		if !routes[p] {
			t.Errorf("routeDocs documents %q, which is not registered", p)
		}
	}
}

// TestOpenAPIDocument checks the document's structure: refs resolve, every
// operation has an id (unique), responses and a scope or open security.
func TestOpenAPIDocument(t *testing.T) {
	doc := buildOpenAPI()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, m := range refRe.FindAllStringSubmatch(string(b), -1) {
		if _, ok := schemas[m[1]]; !ok {
			t.Errorf("$ref to a missing schema %q", m[1])
		}
	}
	ids := map[string]string{}
	for path, item := range doc["paths"].(map[string]any) {
		for method, o := range item.(map[string]any) {
			op := o.(map[string]any)
			id, _ := op["operationId"].(string)
			if prev, dup := ids[id]; dup || id == "" {
				t.Errorf("%s %s: operationId %q (also %s)", method, path, id, prev)
			}
			ids[id] = method + " " + path
			if _, ok := op["responses"].(map[string]any)["default"]; !ok {
				t.Errorf("%s %s: no error response", method, path)
			}
			_, open := op["security"]
			if _, scoped := op["x-goblin-scope"]; open == scoped {
				t.Errorf("%s %s: needs exactly one of security: [] (open) or x-goblin-scope", method, path)
			}
		}
	}
	// a few known shapes
	cfg, _ := schemas["config.Config"].(map[string]any)
	if props, _ := cfg["properties"].(map[string]any); props["policies"] == nil || props["interfaces"] == nil {
		t.Errorf("config.Config schema is missing sections: %v", cfg)
	}
	login := doc["paths"].(map[string]any)["/api/v1/auth/login"].(map[string]any)["post"].(map[string]any)
	if s, _ := login["security"].([]any); s == nil || len(s) != 0 {
		t.Errorf("login should be open: %v", login["security"])
	}
	commit := doc["paths"].(map[string]any)["/api/v1/commit"].(map[string]any)["post"].(map[string]any)
	if commit["x-goblin-scope"] != "commit" {
		t.Errorf("commit scope: %v", commit["x-goblin-scope"])
	}
	if !strings.Contains(string(b), `"format":"cidr"`) {
		t.Error("netip.Prefix fields should be strings with format cidr")
	}
}

// TestOpenAPIFile: docs/api/openapi.json is the document goblind serves.
func TestOpenAPIFile(t *testing.T) {
	b, err := json.MarshalIndent(buildOpenAPI(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if *updateOpenAPI {
		if err := os.MkdirAll(filepath.Dir(openAPIFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(openAPIFile, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(openAPIFile)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), b) {
		t.Fatalf("docs/api/openapi.json is out of date (%v): run go test ./internal/api -run TestOpenAPIFile -update", err)
	}
}

// TestOpenAPIServed: the document is served without authentication.
func TestOpenAPIServed(t *testing.T) {
	a := newTestAPI(t)
	out := a.expect("GET", "/api/v1/openapi.json", nil, 200)
	if out["openapi"] != "3.1.0" || out["paths"] == nil {
		t.Fatalf("served document: %v", out["openapi"])
	}
}
