package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
)

// step is one plain-text call of a run made of parts.
type step struct {
	label   string // names the part in the raw output and errors
	message string // the progress message as the call starts
	field   string
	note    string
	takes   func(r *run, text string) (json.RawMessage, error)
}

// written is a part an earlier step of the run wrote, as later steps see it.
type written struct{ Label, Text string }

// stepsInOrder makes one call per step, each seeing what the steps before it
// wrote, and proposes the draft with every step's part. raw holds the
// output of any plan call before the steps, which progress counts as one.
func (srv *server) stepsInOrder(ctx context.Context, in *instance, r *run, steps []step, raw []string) (*protocol.ActionRunResult, error) {
	base, done := maps.Clone(r.draft), len(raw)
	var earlier []written
	for i, s := range steps {
		_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(done + i),
			Total: int64(done + len(steps)), Message: s.message})
		out, text, err := srv.callStep(ctx, in, r, base, earlier, s)
		if err != nil {
			return nil, err
		}
		if err := r.applyStep(s, text); err != nil {
			return nil, err
		}
		raw = append(raw, s.label+":\n"+out)
		label := s.label
		if s.field != "" {
			label = s.field
		}
		earlier = append(earlier, written{Label: label, Text: text})
	}
	return r.stepsResult(raw)
}

// stepsAtOnce makes the steps' calls side by side, up to the concurrency
// setting at once, then applies their parts in order. Each sees only the
// card as the run found it, so the steps must not depend on each other, as
// steps a plan already coordinated don't.
func (srv *server) stepsAtOnce(ctx context.Context, in *instance, r *run, steps []step, raw []string) (*protocol.ActionRunResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	base, done := maps.Clone(r.draft), len(raw)
	outs, texts, errs := make([]string, len(steps)), make([]string, len(steps)), make([]error, len(steps))
	var finished atomic.Int64
	var wg sync.WaitGroup
	slots := make(chan struct{}, in.settings.concurrency())
	for i, s := range steps {
		slots <- struct{}{}
		if ctx.Err() != nil {
			<-slots
			errs[i] = ctx.Err()
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(done) + finished.Load(),
				Total: int64(done + len(steps)), Message: s.message})
			outs[i], texts[i], errs[i] = srv.callStep(ctx, in, r, base, nil, s)
			finished.Add(1)
			if errs[i] != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
	for i, s := range steps {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if err := r.applyStep(s, texts[i]); err != nil {
			return nil, err
		}
		raw = append(raw, s.label+":\n"+outs[i])
	}
	return r.stepsResult(raw)
}

// callStep makes s's call against base, the card or lorebook as the run
// found it, and returns the model's output and its plain text. Every step
// sees the same base, with earlier parts listed after it, so a server that
// caches prompt prefixes reads the card once per run, not once per step.
func (srv *server) callStep(ctx context.Context, in *instance, r *run, base map[string]json.RawMessage, earlier []written, s step) (output, text string, err error) {
	part := *r
	part.draft, part.earlier = base, earlier
	part.Field, part.note, part.part = s.field, s.note, s.label
	part.takes = func(r *run, t string) (json.RawMessage, error) {
		text = t
		return encode(r.draft)
	}
	res, err := srv.perform(ctx, in, &part, true, noProgress)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", s.label, err)
	}
	return res.Raw, text, nil
}

// applyStep adds the part s's call wrote to the draft.
func (r *run) applyStep(s step, text string) error {
	part := *r
	part.Field = s.field
	proposal, err := s.takes(&part, text)
	if err != nil {
		return fmt.Errorf("%s: %w", s.label, err)
	}
	return json.Unmarshal(proposal, &r.draft)
}

// stepsResult proposes the draft, with raw as the run's output.
func (r *run) stepsResult(raw []string) (*protocol.ActionRunResult, error) {
	proposal, err := encode(r.draft)
	if err != nil {
		return nil, err
	}
	return &protocol.ActionRunResult{Proposal: proposal, Raw: strings.Join(raw, "\n\n")}, nil
}

// generateFields writes a new card one field at a time.
func (srv *server) generateFields(ctx context.Context, in *instance, r *run) (*protocol.ActionRunResult, error) {
	keys := r.writes()
	steps := make([]step, len(keys))
	for i, k := range keys {
		steps[i] = step{label: label(k), message: fmt.Sprintf("Writing %s (%d of %d)", label(k), i+1, len(keys)),
			field: k, takes: (*run).withFieldText}
	}
	return srv.stepsInOrder(ctx, in, r, steps, nil)
}

// appendItems writes the run's count of new list items one at a time, each
// seeing the ones before it.
func (srv *server) appendItems(ctx context.Context, in *instance, r *run) (*protocol.ActionRunResult, error) {
	steps := make([]step, r.count)
	for i := range steps {
		steps[i] = step{label: fmt.Sprintf("%s %d", r.appends.item, i+1),
			message: fmt.Sprintf("Writing %s %d of %d", r.appends.item, i+1, r.count), takes: r.appends.take}
	}
	return srv.stepsInOrder(ctx, in, r, steps, nil)
}
