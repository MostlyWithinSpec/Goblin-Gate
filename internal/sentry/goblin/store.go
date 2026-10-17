package goblin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// Model kinds.
const (
	KindKNN = "knn" // nearest neighbours, built in
	KindLLM = "llm" // a language model behind llama-server
)

// Uses: how much the admin lets the accepted model do.
const (
	UseOff     = "off"     // not asked
	UseSuggest = "suggest" // its answer is the suggestion shown with the question
	UseAct     = "act"     // above the threshold it acts (never blocks); below it suggests
)

// keepVersions is how many trained versions are kept (besides accepted ones).
const keepVersions = 10

// Policy is what the admin lets the model do.
type Policy struct {
	Use string `json:"use"`
	// Thresholds per tool the model may run on its own; a tool without a
	// threshold always asks.
	Thresholds map[string]float64 `json:"thresholds"`
}

// DefaultPolicy: suggest only; when allowed to act, only when quite sure.
func DefaultPolicy() Policy {
	return Policy{Use: UseSuggest, Thresholds: map[string]float64{
		sentry.ToolFalsePositive: 0.9, sentry.ToolWatch: 0.8, sentry.ToolNotify: 0.8}}
}

// Check validates a policy.
func (p Policy) Check() error {
	if !slices.Contains([]string{UseOff, UseSuggest, UseAct}, p.Use) {
		return fmt.Errorf("use: %s, %s or %s", UseOff, UseSuggest, UseAct)
	}
	for tool, t := range p.Thresholds {
		if !modelMayRun(tool) {
			return fmt.Errorf("thresholds: %s cannot run without the admin", tool)
		}
		if t < 0.5 || t > 1 {
			return fmt.Errorf("thresholds.%s: 0.5-1", tool)
		}
	}
	return nil
}

// Version is one trained model.
type Version struct {
	Version  int        `json:"version"`
	Kind     string     `json:"kind"`
	Created  time.Time  `json:"created"`
	Examples int        `json:"examples"`
	Params   string     `json:"params"` // e.g. "k 7, half-life 30 days"
	Report   Report     `json:"report"`
	Accepted *time.Time `json:"accepted,omitempty"`
}

// Index is <Dir>/models/index.json.
type Index struct {
	Next int `json:"next"`
	// History of accepted versions; the last one is in use.
	History  []int     `json:"history"`
	Policy   Policy    `json:"policy"`
	Versions []Version `json:"versions"`
}

// Current is the version in use (0 = none).
func (x *Index) Current() int {
	if len(x.History) == 0 {
		return 0
	}
	return x.History[len(x.History)-1]
}

// Previous is the version a rollback returns to (0 = none).
func (x *Index) Previous() int {
	if len(x.History) < 2 {
		return 0
	}
	return x.History[len(x.History)-2]
}

type modelFile struct {
	Kind string   `json:"kind"`
	KNN  *KNN     `json:"knn,omitempty"`
	LLM  *llmFile `json:"llm,omitempty"`
}

type llmFile struct {
	Endpoint string `json:"endpoint"`
	Name     string `json:"name"`
	Shots    int    `json:"shots"`
	Past     []Case `json:"past"`
}

// Store keeps the model versions in <Dir>/models and is Sentry's Advisor.
type Store struct {
	Dir string // Sentry's state directory
	Now func() time.Time

	mu     sync.Mutex
	idx    *Index
	cur    Predictor
	curVer *Version
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) dir() string { return filepath.Join(s.Dir, "models") }

func (s *Store) file(v int) string { return filepath.Join(s.dir(), fmt.Sprintf("v%d.json", v)) }

// loadLocked reads the index once.
func (s *Store) loadLocked() error {
	if s.idx != nil {
		return nil
	}
	idx := &Index{Next: 1, Policy: DefaultPolicy()}
	data, err := os.ReadFile(filepath.Join(s.dir(), "index.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, idx); err != nil {
			return fmt.Errorf("models index: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	s.idx = idx
	return s.loadCurrentLocked()
}

func (s *Store) loadCurrentLocked() error {
	s.cur, s.curVer = nil, nil
	v := s.idx.Current()
	if v == 0 {
		return nil
	}
	data, err := os.ReadFile(s.file(v))
	if err != nil {
		return err
	}
	var mf modelFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("model v%d: %w", v, err)
	}
	switch {
	case mf.Kind == KindKNN && mf.KNN != nil:
		s.cur = mf.KNN
	case mf.Kind == KindLLM && mf.LLM != nil:
		s.cur = &LLM{Endpoint: mf.LLM.Endpoint, Name: mf.LLM.Name, Shots: mf.LLM.Shots, Past: mf.LLM.Past}
	default:
		return fmt.Errorf("model v%d: unknown kind %q", v, mf.Kind)
	}
	for i := range s.idx.Versions {
		if s.idx.Versions[i].Version == v {
			s.curVer = &s.idx.Versions[i]
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.idx, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.dir(), "index.json"), data)
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Index returns a copy of the index.
func (s *Store) Index() (Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Index{}, err
	}
	out := *s.idx
	out.History = slices.Clone(s.idx.History)
	out.Versions = slices.Clone(s.idx.Versions)
	return out, nil
}

// Train trains and evaluates a new knn version from examples. It is not
// used until accepted.
func (s *Store) Train(xs []sentry.Example) (Version, error) {
	xs = newest(xs)
	cases := Cases(xs)
	train, test, err := Split(cases)
	if err != nil {
		return Version{}, err
	}
	r, err := Evaluate(KindKNN, TrainKNN(train), train, test)
	if err != nil {
		return Version{}, err
	}
	m := TrainKNN(cases)
	params := fmt.Sprintf("k %d, half-life %.0f days", m.K, m.HalfLifeDays)
	return s.add(KindKNN, params, r, modelFile{Kind: KindKNN, KNN: m}, len(cases))
}

// TrainLLM evaluates a language model served by llama-server at endpoint
// on the examples and adds it as a version (its prompts show the admin's
// most similar past answers). Not used until accepted.
func (s *Store) TrainLLM(xs []sentry.Example, endpoint, name string) (Version, error) {
	xs = newest(xs)
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Version{}, errors.New("endpoint: an http(s) URL of a llama-server")
	}
	cases := Cases(xs)
	train, test, err := Split(cases)
	if err != nil {
		return Version{}, err
	}
	r, err := Evaluate(KindLLM, &LLM{Endpoint: endpoint, Name: name, Past: train}, train, test)
	if err != nil {
		return Version{}, err
	}
	r.Model = name
	mf := modelFile{Kind: KindLLM, LLM: &llmFile{Endpoint: endpoint, Name: name, Shots: 5, Past: cases}}
	return s.add(KindLLM, name+" at "+endpoint, r, mf, len(cases))
}

// newest keeps the newest MaxExamples answers.
func newest(xs []sentry.Example) []sentry.Example {
	if len(xs) > MaxExamples {
		return xs[len(xs)-MaxExamples:]
	}
	return xs
}

func (s *Store) add(kind, params string, r Report, mf modelFile, n int) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Version{}, err
	}
	v := Version{Version: s.idx.Next, Kind: kind, Created: s.now().UTC(), Examples: n, Params: params, Report: r}
	data, err := json.Marshal(mf)
	if err != nil {
		return Version{}, err
	}
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return Version{}, err
	}
	if err := writeFile(s.file(v.Version), data); err != nil {
		return Version{}, err
	}
	s.idx.Next++
	s.idx.Versions = append(s.idx.Versions, v)
	s.pruneLocked()
	if err := s.saveLocked(); err != nil {
		return Version{}, err
	}
	return v, nil
}

// pruneLocked drops the oldest versions that were never accepted.
func (s *Store) pruneLocked() {
	over := len(s.idx.Versions) - keepVersions
	kept := s.idx.Versions[:0]
	for _, v := range s.idx.Versions {
		if over > 0 && v.Accepted == nil {
			over--
			_ = os.Remove(s.file(v.Version))
			continue
		}
		kept = append(kept, v)
	}
	s.idx.Versions = kept
}

// Accept puts a version in use.
func (s *Store) Accept(version int) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Version{}, err
	}
	i := slices.IndexFunc(s.idx.Versions, func(v Version) bool { return v.Version == version })
	if i < 0 {
		return Version{}, fmt.Errorf("no version %d", version)
	}
	if s.idx.Current() == version {
		return s.idx.Versions[i], nil
	}
	old := slices.Clone(s.idx.History)
	t := s.now().UTC()
	s.idx.Versions[i].Accepted = &t
	s.idx.History = append(slices.DeleteFunc(s.idx.History, func(v int) bool { return v == version }), version)
	if err := s.loadCurrentLocked(); err != nil {
		s.idx.History = old
		_ = s.loadCurrentLocked()
		return Version{}, err
	}
	return s.idx.Versions[i], s.saveLocked()
}

// Rollback returns to the version accepted before the current one (or to
// no model when there is none).
func (s *Store) Rollback() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return 0, err
	}
	if len(s.idx.History) == 0 {
		return 0, errors.New("no model in use")
	}
	s.idx.History = s.idx.History[:len(s.idx.History)-1]
	if err := s.loadCurrentLocked(); err != nil {
		return 0, err
	}
	return s.idx.Current(), s.saveLocked()
}

// SetPolicy changes what the model may do.
func (s *Store) SetPolicy(p Policy) error {
	if err := p.Check(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	if p.Thresholds == nil {
		p.Thresholds = map[string]float64{}
	}
	s.idx.Policy = p
	return s.saveLocked()
}

// Advise implements sentry.Advisor with the accepted version.
func (s *Store) Advise(inc sentry.Incident) (sentry.Advice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadLocked() != nil || s.cur == nil || s.idx.Policy.Use == UseOff {
		return sentry.Advice{}, false
	}
	p, err := s.cur.Predict(Query{Time: inc.First, F: FeaturesOf(inc.Alert, inc.Enrichment)})
	if err != nil || p.Tool == "" || p.Tool == sentry.ToolRequestApproval {
		return sentry.Advice{}, false
	}
	t, ok := s.idx.Policy.Thresholds[p.Tool]
	return sentry.Advice{
		Model:      fmt.Sprintf("v%d (%s)", s.curVer.Version, s.curVer.Kind),
		Action:     sentry.Action{Tool: p.Tool, Minutes: p.Minutes},
		Confidence: p.Confidence,
		Act:        s.idx.Policy.Use == UseAct && modelMayRun(p.Tool) && ok && p.Confidence >= t,
		Why:        p.Why,
	}, true
}
