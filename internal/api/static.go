package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ServeStaticFile serves static files from a directory
func ServeStaticFile(w http.ResponseWriter, r *http.Request, staticDir string) {
	// Get the requested path
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Security check: prevent directory traversal
	if strings.Contains(path, "..") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Construct full file path
	fullPath := filepath.Join(staticDir, path)

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// Try to serve index.html for SPA routing
		fullPath = filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
	}

	// Serve the file
	http.ServeFile(w, r, fullPath)
}

// StaticDir returns a handler serving the web UI from dir (index.html for
// unknown paths, so client-side routes work).
func StaticDir(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ServeStaticFile(w, r, dir) })
}
