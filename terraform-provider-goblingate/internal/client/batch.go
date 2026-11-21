package client

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Terraform creates, updates and deletes independent resources in
// parallel, each in its own call. The batcher collects the changes that
// arrive within a short window and makes them one candidate edit and one
// commit, so an apply does not commit (and reload the firewall) once per
// resource. Batches never overlap: the next one starts after the previous
// commit, on a candidate that must be clean again.

type op struct {
	ctx     context.Context
	summary string
	fn      func(Edit) error
	done    chan error
}

type batcher struct {
	c      *Client
	window time.Duration
	ops    chan *op
	max    int
}

func newBatcher(c *Client, window time.Duration) *batcher {
	b := &batcher{c: c, window: window, ops: make(chan *op), max: 100}
	go b.loop()
	return b
}

func (b *batcher) run(ctx context.Context, summary string, fn func(Edit) error) error {
	o := &op{ctx: ctx, summary: summary, fn: fn, done: make(chan error, 1)}
	select {
	case b.ops <- o:
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-o.done
}

func (b *batcher) loop() {
	for first := range b.ops {
		batch := []*op{first}
		timer := time.NewTimer(b.window)
	collect:
		for len(batch) < b.max {
			select {
			case o := <-b.ops:
				batch = append(batch, o)
				timer.Reset(b.window)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		err := b.apply(batch)
		for _, o := range batch {
			o.done <- err
		}
	}
}

// apply runs a batch: check the candidate is clean, make every change,
// commit; on any failure discard what was changed.
func (b *batcher) apply(batch []*op) error {
	// the first request's context: Terraform cancels them all together
	ctx := context.WithoutCancel(batch[0].ctx)
	if err := b.c.clean(ctx); err != nil {
		return err
	}
	e := &edit{ctx: ctx, c: b.c}
	for _, o := range batch {
		if err := o.fn(e); err != nil {
			b.c.discard(ctx)
			if len(batch) == 1 {
				return err
			}
			return fmt.Errorf("%s: %w (nothing was committed: the %d changes of this apply step are committed together)", o.summary, err, len(batch))
		}
	}
	if err := b.c.commit(ctx, comment(batch)); err != nil {
		b.c.discard(ctx)
		return err
	}
	return nil
}

// comment is the commit comment: what changed, cut to a readable length.
func comment(batch []*op) string {
	const limit = 400
	s := "terraform: "
	for i, o := range batch {
		part := o.summary
		if i > 0 {
			part = ", " + part
		}
		if len(s)+len(part) > limit {
			return s + fmt.Sprintf(" and %d more", len(batch)-i)
		}
		s += part
	}
	return strings.TrimSpace(s)
}
