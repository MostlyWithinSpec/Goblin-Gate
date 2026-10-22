package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticStaysInItsDirectory(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, "web")
	os.MkdirAll(filepath.Join(web, "assets"), 0o755)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("index"), 0o644)
	os.WriteFile(filepath.Join(web, "assets", "app.js"), []byte("app"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644)
	h := StaticDir(web)
	for path, want := range map[string]string{
		"/assets/app.js":           "app",
		"/some/client/route":       "index",
		"/../secret.txt":           "",
		"/assets/../../secret.txt": "",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = path // as a raw client could send it
		h.ServeHTTP(rec, req)
		if body := rec.Body.String(); want != "" && body != want || want == "" && body == "secret" {
			t.Errorf("%s: %d %q", path, rec.Code, body)
		}
	}
}
