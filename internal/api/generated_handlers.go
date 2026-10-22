package api

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Generated config viewer (task 074): the files a commit writes, rendered in
// memory for the running or the candidate config.

// maxGeneratedContent caps one file in a response (a DNS blocklist zone
// can hold hundreds of thousands of names).
const maxGeneratedContent = 2 << 20

// secretLine matches "key = value" lines whose key names a secret. The
// renderers write no secrets (private keys stay in key files they only point
// to), so this is a safety net for a future renderer.
var secretLine = regexp.MustCompile(`(?im)^(\s*(?:private[-_ ]?key|preshared[-_ ]?key|psk|password|passwd|secret|auth[-_ ]?key|api[-_ ]?key)\s*[=:]\s*)(\S.*)$`)

// monitorLine matches NUT's "MONITOR <ups> <power> <user> <password> <role>"
// (upsmon.conf), whose password has no key of its own.
var monitorLine = regexp.MustCompile(`(?im)^(\s*MONITOR\s+\S+\s+\S+\s+\S+\s+)("[^"\n]*"|\S+)`)

// redact replaces secret values; it reports whether it changed anything.
func redact(content string) (string, bool) {
	out := secretLine.ReplaceAllString(content, "${1}<redacted>")
	out = monitorLine.ReplaceAllString(out, "${1}<redacted>")
	return out, out != content
}

// generatedConfig picks the config named by ?config= (running by default).
func (s *Server) generatedConfig(w http.ResponseWriter, r *http.Request) (*config.Config, bool) {
	var (
		cfg *config.Config
		err error
	)
	switch r.URL.Query().Get("config") {
	case "", "running":
		cfg, err = s.Store.Running()
	case "candidate":
		cfg, err = s.Store.Candidate()
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "config must be running or candidate", nil)
		return nil, false
	}
	if err != nil {
		s.internal(w, "read config", err)
		return nil, false
	}
	return cfg, true
}

// renderGenerated renders cfg; a config the renderers refuse is a 422.
func (s *Server) renderGenerated(w http.ResponseWriter, cfg *config.Config) (map[string][]byte, map[string]string, bool) {
	files, owner, err := s.Commit.RenderAll(cfg)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "render_failed", err.Error(), nil)
		return nil, nil, false
	}
	return files, owner, true
}

// GET /api/v1/system/generated?config= → {files: [{path, renderer, size, redacted}]}
func (s *Server) generatedList(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.generatedConfig(w, r)
	if !ok {
		return
	}
	files, owner, ok := s.renderGenerated(w, cfg)
	if !ok {
		return
	}
	type entry struct {
		Path     string `json:"path"`
		Renderer string `json:"renderer"`
		Size     int    `json:"size"`
		Redacted bool   `json:"redacted"`
	}
	list := make([]entry, 0, len(files))
	for p, b := range files {
		_, red := redact(string(b))
		list = append(list, entry{Path: p, Renderer: owner[p], Size: len(b), Redacted: red})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Renderer != list[j].Renderer {
			return list[i].Renderer < list[j].Renderer
		}
		return list[i].Path < list[j].Path
	})
	writeJSON(w, http.StatusOK, map[string]any{"files": list})
}

// GET /api/v1/system/generated/file?path=&config= → {path, renderer, content, redacted, truncated}
func (s *Server) generatedFile(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.generatedConfig(w, r)
	if !ok {
		return
	}
	files, owner, ok := s.renderGenerated(w, cfg)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	b, found := files[path]
	if !found {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no generated file %q", path), nil)
		return
	}
	content, red := redact(string(b))
	truncated := false
	if len(content) > maxGeneratedContent {
		content, truncated = content[:maxGeneratedContent], true
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "renderer": owner[path], "content": content, "redacted": red, "truncated": truncated})
}

// GET /api/v1/system/generated/diff?path= → {path, diff}: unified diff of
// the file from the running to the candidate config ("" = unchanged).
func (s *Server) generatedDiff(w http.ResponseWriter, r *http.Request) {
	running, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	candidate, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	before, _, ok := s.renderGenerated(w, running)
	if !ok {
		return
	}
	after, _, ok := s.renderGenerated(w, candidate)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	a, inA := before[path]
	b, inB := after[path]
	if !inA && !inB {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no generated file %q", path), nil)
		return
	}
	ra, _ := redact(string(a))
	rb, _ := redact(string(b))
	diff, err := unifiedDiff(path, ra, rb)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "too_large", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "diff": diff})
}

// maxDiffLines bounds the changed middle part unifiedDiff compares line by
// line (quadratic): blocklist zones differ by far more.
const maxDiffLines = 5000

// unifiedDiff returns a unified diff (3 lines of context) of a and b.
func unifiedDiff(name, a, b string) (string, error) {
	if a == b {
		return "", nil
	}
	x, y := splitLines(a), splitLines(b)
	// trim the common head and tail, compare the middle
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	mx, my := x[pre:len(x)-suf], y[pre:len(y)-suf]
	if len(mx) > maxDiffLines || len(my) > maxDiffLines {
		return "", fmt.Errorf("%s changed in more than %d lines: compare the files instead", name, maxDiffLines)
	}
	// LCS table over the middle
	lcs := make([][]int, len(mx)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(my)+1)
	}
	for i := len(mx) - 1; i >= 0; i-- {
		for j := len(my) - 1; j >= 0; j-- {
			if mx[i] == my[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int // line number in a (0-based) of ' ' and '-'
		bi   int
	}
	var ops []op
	for i := 0; i < pre; i++ {
		ops = append(ops, op{' ', x[i], i, i})
	}
	i, j := 0, 0
	for i < len(mx) || j < len(my) {
		switch {
		case i < len(mx) && j < len(my) && mx[i] == my[j]:
			ops = append(ops, op{' ', mx[i], pre + i, pre + j})
			i++
			j++
		case i < len(mx) && (j == len(my) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', mx[i], pre + i, pre + j}) // deletions first, like diff -u
			i++
		default:
			ops = append(ops, op{'+', my[j], pre + i, pre + j})
			j++
		}
	}
	for k := 0; k < suf; k++ {
		ops = append(ops, op{' ', x[len(x)-suf+k], len(x) - suf + k, len(y) - suf + k})
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- running/%s\n+++ candidate/%s\n", name, name)
	const ctx = 3
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		// a hunk: from ctx lines before this change to ctx lines after the
		// last change that is within 2*ctx of the previous one
		start := max(k-ctx, 0)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*ctx {
				end = min(end+ctx, len(ops))
				break
			}
			end = run
		}
		aStart, bStart, aLen, bLen := ops[start].ai, ops[start].bi, 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				aLen++
			}
			if o.kind != '-' {
				bLen++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart+1, aLen, bStart+1, bLen)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String(), nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
