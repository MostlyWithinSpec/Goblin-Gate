package notify

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// IPSBatcher turns IPS drops from the log pipeline into at most one
// "ips_block" event per interval: how many were dropped and the top
// signatures, instead of one message per packet.
type IPSBatcher struct {
	D     *Dispatcher
	Every time.Duration // default 1 minute

	mu     sync.Mutex
	total  int
	bySig  map[string]int
	bySrcs map[string]bool
}

// Observe counts a log event if it is an IPS drop. Safe from any goroutine;
// never blocks (logs.Ingester.OnEvent).
func (b *IPSBatcher) Observe(e *logs.Event) {
	if e == nil || e.Kind != "ips" || (e.Action != "drop" && e.Action != "deny") {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bySig == nil {
		b.bySig, b.bySrcs = map[string]int{}, map[string]bool{}
	}
	b.total++
	b.bySig[e.Signature]++
	if e.SrcIP != "" {
		b.bySrcs[e.SrcIP] = true
	}
}

// Run publishes the counts every interval until ctx ends.
func (b *IPSBatcher) Run(ctx context.Context) {
	every := b.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ev, ok := b.flush(); ok {
				b.D.Publish(ev)
			}
		}
	}
}

// flush returns the summary event of the interval and resets the counts.
func (b *IPSBatcher) flush() (Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total == 0 {
		return Event{}, false
	}
	type sig struct {
		name string
		n    int
	}
	var sigs []sig
	for k, v := range b.bySig {
		sigs = append(sigs, sig{k, v})
	}
	sort.Slice(sigs, func(i, j int) bool {
		return sigs[i].n > sigs[j].n || sigs[i].n == sigs[j].n && sigs[i].name < sigs[j].name
	})
	var lines []string
	for i, s := range sigs {
		if i == 3 {
			lines = append(lines, fmt.Sprintf("and %d more signatures", len(sigs)-3))
			break
		}
		name := s.name
		if name == "" {
			name = "(no signature)"
		}
		lines = append(lines, fmt.Sprintf("%d × %s", s.n, name))
	}
	ev := Event{
		Kind:    "ips_block",
		Title:   fmt.Sprintf("IPS blocked %d connection(s)", b.total),
		Message: fmt.Sprintf("From %d source address(es):\n%s", len(b.bySrcs), strings.Join(lines, "\n")),
		Fields:  map[string]string{"count": strconv.Itoa(b.total)},
	}
	b.total, b.bySig, b.bySrcs = 0, map[string]int{}, map[string]bool{}
	return ev, true
}
