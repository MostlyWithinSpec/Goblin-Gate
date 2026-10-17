package goblin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

func incidentOf(x sentry.Example) sentry.Incident {
	return sentry.Incident{ID: 1, First: x.Time, Last: x.Time, Count: 1, Alert: x.Alert, Enrichment: x.Enrichment}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	xs := Fixture(1, 600)
	later := Fixture(1, 700)[600:] // after every training answer
	probe := incidentOf(later[50])
	if _, ok := s.Advise(probe); ok {
		t.Fatal("advice without a model")
	}
	if _, err := s.Train(xs[:10]); err == nil || !strings.Contains(err.Error(), "need at least") {
		t.Fatalf("too few: %v", err)
	}
	v1, err := s.Train(xs)
	if err != nil || v1.Version != 1 || v1.Kind != KindKNN || v1.Report.Test != 150 || !strings.Contains(v1.Params, "half-life") {
		t.Fatalf("train: %+v %v", v1, err)
	}
	if _, ok := s.Advise(probe); ok {
		t.Fatal("advice before the version was accepted")
	}
	if _, err := s.Accept(1); err != nil {
		t.Fatal(err)
	}
	adv, ok := s.Advise(probe)
	if !ok || adv.Model != "v1 (knn)" || adv.Act || adv.Why == "" {
		t.Fatalf("suggest: %+v %v", adv, ok)
	}

	// act: only non-disruptive tools above their threshold
	if err := s.SetPolicy(Policy{Use: UseAct, Thresholds: map[string]float64{sentry.ToolBlock: 0.9}}); err == nil {
		t.Fatal("a block threshold was accepted")
	}
	if err := s.SetPolicy(Policy{Use: "yolo"}); err == nil {
		t.Fatal("bad use accepted")
	}
	all := map[string]float64{sentry.ToolFalsePositive: 0.5, sentry.ToolWatch: 0.5, sentry.ToolNotify: 0.5}
	if err := s.SetPolicy(Policy{Use: UseAct, Thresholds: all}); err != nil {
		t.Fatal(err)
	}
	acted, blocks := 0, 0
	for _, x := range later {
		adv, ok := s.Advise(incidentOf(x))
		if !ok {
			continue
		}
		if adv.Act {
			acted++
			if adv.Action.Tool == sentry.ToolBlock || adv.Confidence < 0.5 {
				t.Fatalf("acted: %+v", adv)
			}
		}
		if adv.Action.Tool == sentry.ToolBlock {
			blocks++
		}
	}
	if acted == 0 || blocks == 0 {
		t.Fatalf("acted %d, blocks %d", acted, blocks)
	}
	if err := s.SetPolicy(Policy{Use: UseOff}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Advise(probe); ok {
		t.Fatal("advice while off")
	}

	// a second version, accepted, rolled back; state survives a restart
	v2, _ := s.Train(Fixture(2, 600))
	if _, err := s.Accept(v2.Version); err != nil {
		t.Fatal(err)
	}
	s = &Store{Dir: dir}
	idx, _ := s.Index()
	if idx.Current() != 2 || idx.Previous() != 1 || idx.Policy.Use != UseOff {
		t.Fatalf("index: %+v", idx)
	}
	if cur, err := s.Rollback(); err != nil || cur != 1 {
		t.Fatalf("rollback: %d %v", cur, err)
	}
	if cur, _ := s.Rollback(); cur != 0 {
		t.Fatalf("second rollback: %d", cur)
	}
	if _, err := s.Rollback(); err == nil {
		t.Fatal("rollback with no model")
	}
	if _, err := s.Accept(99); err == nil {
		t.Fatal("accepted a missing version")
	}

	// old versions that were never accepted are dropped
	for i := 0; i < keepVersions+2; i++ {
		if _, err := s.Train(xs); err != nil {
			t.Fatal(err)
		}
	}
	idx, _ = s.Index()
	if len(idx.Versions) != keepVersions {
		t.Fatalf("versions: %d", len(idx.Versions))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "models", "v*.json"))
	if len(files) != keepVersions {
		t.Fatalf("files: %d", len(files))
	}
}

func TestAPI(t *testing.T) {
	dir := t.TempDir()
	a := &API{Store: &Store{Dir: dir}, Examples: filepath.Join(dir, "examples.jsonl")}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != want || resp.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%s %s: %d %v", method, path, resp.StatusCode, out)
		}
		return out
	}
	if out := call("GET", "/models/examples", "", 200); len(out["examples"].([]any)) != 0 {
		t.Fatalf("examples: %v", out)
	}
	call("POST", "/models/train", `{}`, 400) // not enough answers
	f, _ := os.Create(a.Examples)
	enc := json.NewEncoder(f)
	for _, x := range Fixture(1, 300) {
		enc.Encode(x)
	}
	f.Close()
	call("POST", "/models/train", `{"kind":"llm"}`, 400)
	call("POST", "/models/train", `{"kind":"gpt"}`, 400)
	call("POST", "/models/train", `{"surprise":1}`, 400)
	call("POST", "/models/train", `{}`, 202)
	var st map[string]any
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		st = call("GET", "/models", "", 200)
		if job, _ := st["job"].(map[string]any); job != nil && job["finished"] != nil {
			if job["error"] != nil || job["version"].(float64) != 1 {
				t.Fatalf("job: %v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("training did not finish: %v", st)
		}
	}
	if st["current"].(float64) != 0 || len(st["versions"].([]any)) != 1 {
		t.Fatalf("models: %v", st)
	}
	if v := call("GET", "/models/1", "", 200); v["report"].(map[string]any)["accuracy"].(float64) <= 0 {
		t.Fatalf("version: %v", v)
	}
	call("GET", "/models/7", "", 404)
	call("POST", "/models/1/accept", "", 200)
	call("POST", "/models/7/accept", "", 400)
	if p := call("PUT", "/models/policy", `{"use":"act","thresholds":{"mark_false_positive":0.95}}`, 200); p["use"] != "act" {
		t.Fatalf("policy: %v", p)
	}
	call("PUT", "/models/policy", `{"use":"act","thresholds":{"temporary_block_ip":0.95}}`, 400)
	if out := call("POST", "/models/rollback", "", 200); out["current"].(float64) != 0 {
		t.Fatalf("rollback: %v", out)
	}
	call("POST", "/models/rollback", "", 400)

	// forgetting an answer goes through the engine
	var removed []int64
	a.Remove = func(id int64) (bool, error) { removed = append(removed, id); return id == 5, nil }
	req, _ := http.NewRequest("DELETE", srv.URL+"/models/examples/5", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("delete: %v %v", resp, err)
	}
	call("DELETE", "/models/examples/6", "", 404)
	call("DELETE", "/models/examples/x", "", 404)
	if len(removed) != 2 {
		t.Fatalf("removed: %v", removed)
	}
}
