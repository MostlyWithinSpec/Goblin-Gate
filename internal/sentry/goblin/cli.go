package goblin

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// Command runs the offline tools of the Sentry plugin program:
//
//	fixture  [-seed 1] [-n 1500]                 simulated answers (JSONL) to stdout
//	train    -examples F -dir STATE              train a knn version into STATE/models (not accepted yet)
//	eval     -examples F [-llm URL [-name N]]    evaluation reports (JSON): knn, and the llm when given
//	llm-data -examples F -train A -test B        prompt/completion pairs (JSONL) for fine-tuning
//
// The examples file is Sentry's examples.jsonl (GET /models/examples) or
// a fixture. It returns the exit code.
func Command(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: run fixture|train|eval|llm-data [flags]")
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	examples := fs.String("examples", "", "examples file (JSONL)")
	seed := fs.Uint64("seed", 1, "fixture: random seed")
	n := fs.Int("n", 1500, "fixture: number of answers")
	dir := fs.String("dir", "", "train: Sentry's state directory")
	llm := fs.String("llm", "", "eval: llama-server URL serving the language model")
	name := fs.String("name", "", "eval: the language model's name for the report")
	trainOut := fs.String("train", "", "llm-data: training pairs file")
	testOut := fs.String("test", "", "llm-data: test pairs file")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	load := func() ([]Case, error) {
		if *examples == "" {
			return nil, fmt.Errorf("-examples is required")
		}
		xs, err := ReadExamples(*examples)
		if err != nil {
			return nil, err
		}
		return Cases(xs), nil
	}
	switch args[0] {
	case "fixture":
		w := bufio.NewWriter(stdout)
		enc := json.NewEncoder(w)
		for _, x := range Fixture(*seed, *n) {
			if err := enc.Encode(x); err != nil {
				return fail(err)
			}
		}
		if err := w.Flush(); err != nil {
			return fail(err)
		}
	case "train":
		if *dir == "" {
			return fail(fmt.Errorf("-dir is required"))
		}
		xs, err := ReadExamples(*examples)
		if err != nil {
			return fail(err)
		}
		v, err := (&Store{Dir: *dir}).Train(xs)
		if err != nil {
			return fail(err)
		}
		return printJSON(stdout, v)
	case "eval":
		cases, err := load()
		if err != nil {
			return fail(err)
		}
		train, test, err := Split(cases)
		if err != nil {
			return fail(err)
		}
		out := map[string]Report{}
		if out["knn"], err = Evaluate(KindKNN, TrainKNN(train), train, test); err != nil {
			return fail(err)
		}
		if *llm != "" {
			r, err := Evaluate(KindLLM, &LLM{Endpoint: *llm, Name: *name, Past: train}, train, test)
			if err != nil {
				return fail(err)
			}
			r.Model = *name
			out["llm"] = r
		}
		return printJSON(stdout, out)
	case "llm-data":
		cases, err := load()
		if err != nil {
			return fail(err)
		}
		train, test, err := Split(cases)
		if err != nil {
			return fail(err)
		}
		// each training prompt shows the answers before it; test prompts
		// show training answers only
		if err := writePairs(*trainOut, train, train); err != nil {
			return fail(err)
		}
		if err := writePairs(*testOut, test, train); err != nil {
			return fail(err)
		}
	default:
		fmt.Fprintln(stderr, "unknown command", args[0])
		return 2
	}
	return 0
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

// Pair is one fine-tuning example.
type Pair struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

func writePairs(path string, cases, past []Case) error {
	if path == "" {
		return fmt.Errorf("-train and -test are required")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, c := range cases {
		if err := enc.Encode(Pair{Prompt: Prompt(Query{Time: c.Time, F: c.F}, past, 5), Completion: Completion(c)}); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadExamples reads an examples file (JSONL); bad lines are skipped.
func ReadExamples(path string) ([]sentry.Example, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []sentry.Example
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var x sentry.Example
		if json.Unmarshal(sc.Bytes(), &x) == nil && x.Answer != "" {
			out = append(out, x)
		}
	}
	return out, sc.Err()
}

func errorsIsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
