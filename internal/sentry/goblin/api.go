package goblin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// MaxExamples: training uses the newest answers up to this many.
const MaxExamples = 5000

// Job is a training run in the background (an llm evaluation takes
// minutes; goblind's proxy waits 30 seconds).
type Job struct {
	Kind     string    `json:"kind"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Version  int       `json:"version,omitempty"` // the new version when done
	Error    string    `json:"error,omitempty"`
}

// API serves the model endpoints under /models on Sentry's API socket
// (the admin UI reaches them through goblind's plugin proxy):
//
//	GET  /models                  {current, previous, policy, versions (newest first), job?}
//	GET    /models/examples       {examples: [Example]} oldest first - the admin's answers (also for a desktop tool)
//	DELETE /models/examples/{id}  204: forget the answer to the incident that began with alert id (404 if none)
//	POST /models/train            {kind?: knn|llm, endpoint?, name?} -> 202 {job}; 409 while one runs
//	GET  /models/{v}              Version (with its report)
//	POST /models/{v}/accept       {current}: put the version in use
//	POST /models/rollback         {current}: back to the version accepted before
//	PUT  /models/policy           {use: off|suggest|act, thresholds: {tool: 0.5-1}} -> Policy
type API struct {
	Store    *Store
	Examples string // Sentry's examples.jsonl
	// Remove drops an answer from the examples (sentry.Engine.RemoveExample).
	Remove func(alertID int64) (bool, error)

	mu  sync.Mutex
	job *Job
}

// Handler returns the endpoints.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		idx, err := a.Store.Index()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		vs := slices.Clone(idx.Versions)
		slices.Reverse(vs)
		out := map[string]any{"current": idx.Current(), "previous": idx.Previous(), "policy": idx.Policy, "versions": vs}
		a.mu.Lock()
		if a.job != nil {
			out["job"] = *a.job
		}
		a.mu.Unlock()
		reply(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /models/examples", func(w http.ResponseWriter, r *http.Request) {
		xs, err := a.examples()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		reply(w, http.StatusOK, map[string]any{"examples": xs})
	})
	mux.HandleFunc("DELETE /models/examples/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || a.Remove == nil {
			fail(w, http.StatusNotFound, "no such answer")
			return
		}
		found, err := a.Remove(id)
		switch {
		case err != nil:
			fail(w, http.StatusInternalServerError, err.Error())
		case !found:
			fail(w, http.StatusNotFound, "no such answer")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /models/train", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind     string `json:"kind"`
			Endpoint string `json:"endpoint"`
			Name     string `json:"name"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		if req.Kind == "" {
			req.Kind = KindKNN
		}
		if req.Kind != KindKNN && req.Kind != KindLLM {
			fail(w, http.StatusBadRequest, "kind: knn or llm")
			return
		}
		if req.Kind == KindLLM && (req.Endpoint == "" || req.Name == "") {
			fail(w, http.StatusBadRequest, "an llm needs endpoint (llama-server URL) and name")
			return
		}
		xs, err := a.examples()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n := len(Cases(xs)); n < MinExamples {
			fail(w, http.StatusBadRequest, "not enough answers yet: "+strconv.Itoa(n)+" of "+strconv.Itoa(MinExamples))
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.job != nil && a.job.Finished.IsZero() {
			fail(w, http.StatusConflict, "a training run is in progress")
			return
		}
		job := &Job{Kind: req.Kind, Started: time.Now().UTC()}
		a.job = job
		go func() {
			var v Version
			var err error
			if req.Kind == KindLLM {
				v, err = a.Store.TrainLLM(xs, req.Endpoint, req.Name)
			} else {
				v, err = a.Store.Train(xs)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			job.Finished, job.Version = time.Now().UTC(), v.Version
			if err != nil {
				job.Error = err.Error()
			}
		}()
		reply(w, http.StatusAccepted, map[string]any{"job": *job})
	})
	mux.HandleFunc("GET /models/{v}", func(w http.ResponseWriter, r *http.Request) {
		v, _ := strconv.Atoi(r.PathValue("v"))
		idx, err := a.Store.Index()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, ver := range idx.Versions {
			if ver.Version == v {
				reply(w, http.StatusOK, ver)
				return
			}
		}
		fail(w, http.StatusNotFound, "no such version")
	})
	mux.HandleFunc("POST /models/{v}/accept", func(w http.ResponseWriter, r *http.Request) {
		v, err := strconv.Atoi(r.PathValue("v"))
		if err != nil {
			fail(w, http.StatusNotFound, "no such version")
			return
		}
		if _, err := a.Store.Accept(v); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		reply(w, http.StatusOK, map[string]int{"current": v})
	})
	mux.HandleFunc("POST /models/rollback", func(w http.ResponseWriter, r *http.Request) {
		cur, err := a.Store.Rollback()
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		reply(w, http.StatusOK, map[string]int{"current": cur})
	})
	mux.HandleFunc("PUT /models/policy", func(w http.ResponseWriter, r *http.Request) {
		var p Policy
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			fail(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		if err := a.Store.SetPolicy(p); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		idx, _ := a.Store.Index()
		reply(w, http.StatusOK, idx.Policy)
	})
	return mux
}

func (a *API) examples() ([]sentry.Example, error) {
	xs, err := ReadExamples(a.Examples)
	if err != nil {
		if errorsIsNotExist(err) {
			return []sentry.Example{}, nil
		}
		return nil, err
	}
	if len(xs) > MaxExamples {
		xs = xs[len(xs)-MaxExamples:]
	}
	return xs, nil
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}
