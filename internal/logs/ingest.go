package logs

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"time"
)

// Source is one followed log file and its line parser.
type Source struct {
	Name  string // used for the position file: <PosDir>/tail-<name>.pos
	Path  string
	Parse func(line string) (*Event, error)
}

// DefaultSources are the nftables (ulogd2) and Suricata EVE logs.
func DefaultSources() []Source {
	return []Source{
		{Name: "nftables", Path: NftablesLog, Parse: ParseNftables},
		{Name: "suricata", Path: SuricataLog, Parse: ParseSuricata},
	}
}

// Ingester tails the sources and stores their events in batches (every
// FlushEvery or BatchSize events, whichever comes first).
type Ingester struct {
	Store  *Store
	PosDir string // data_dir
	Logger *slog.Logger

	// PolicyAction returns accept/deny for a policy id so traffic events get
	// an action (nft log lines do not carry one); nil leaves it empty.
	PolicyAction func(policyID string) string

	BatchSize  int           // default 500
	FlushEvery time.Duration // default 1s
	Poll       time.Duration // tailer poll, default 500ms
}

// Run ingests until ctx ends. Missing files are waited for.
func (in *Ingester) Run(ctx context.Context, sources []Source) {
	logger := in.Logger
	if logger == nil {
		logger = slog.Default()
	}
	batch := max(in.BatchSize, 1)
	if in.BatchSize == 0 {
		batch = 500
	}
	every := in.FlushEvery
	if every <= 0 {
		every = time.Second
	}
	events := make(chan *Event, batch*2)
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := &Tailer{Path: src.Path, PosFile: filepath.Join(in.PosDir, "tail-"+src.Name+".pos"), Poll: in.Poll}
			bad := 0
			err := t.Run(ctx, func(line string) {
				e, err := src.Parse(line)
				switch {
				case errors.Is(err, ErrSkip):
					return
				case err != nil:
					if bad++; bad <= 5 || bad%1000 == 0 {
						logger.Warn("logs: unparsable line", "source", src.Name, "count", bad, "error", err)
					}
					return
				}
				select {
				case events <- e:
				case <-ctx.Done():
				}
			})
			if err != nil {
				logger.Error("logs: tailer stopped", "source", src.Name, "path", src.Path, "error", err)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	tick := time.NewTicker(every)
	defer tick.Stop()
	var pending []*Event
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if err := in.Store.InsertBatch(pending); err != nil {
			logger.Warn("logs: store batch failed", "events", len(pending), "error", err)
		}
		pending = pending[:0]
	}
	for {
		select {
		case e, ok := <-events:
			if !ok {
				flush()
				return
			}
			if e.Kind == "traffic" && e.Action == "" && e.PolicyID != "" && in.PolicyAction != nil {
				e.Action = in.PolicyAction(e.PolicyID)
			}
			pending = append(pending, e)
			if len(pending) >= batch {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
