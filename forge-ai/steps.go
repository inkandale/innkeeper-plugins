package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// runSteps makes one call per step, in order, each against the draft as the
// steps before it left it, and proposes the draft. raw holds the output of
// any plan call before the steps, which progress counts as one step.
func (srv *server) runSteps(ctx context.Context, in *instance, r *run, steps []step, raw []string) (*protocol.ActionRunResult, error) {
	done := len(raw)
	for i, s := range steps {
		_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(done + i),
			Total: int64(done + len(steps)), Message: s.message})
		// part shares r.draft; its proposal copies the draft before changing
		// it, and decoding the proposal back into r.draft shows the next step the change.
		part := *r
		part.Field, part.note, part.takes, part.part = s.field, s.note, s.takes, s.label
		res, err := srv.perform(ctx, in, &part, true, noProgress)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.label, err)
		}
		raw = append(raw, s.label+":\n"+res.Raw)
		if err := json.Unmarshal(res.Proposal, &r.draft); err != nil {
			return nil, err
		}
	}
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
	return srv.runSteps(ctx, in, r, steps, nil)
}

// appendItems writes the run's count of new list items one at a time, each
// seeing the ones before it.
func (srv *server) appendItems(ctx context.Context, in *instance, r *run) (*protocol.ActionRunResult, error) {
	steps := make([]step, r.count)
	for i := range steps {
		steps[i] = step{label: fmt.Sprintf("%s %d", r.appends.item, i+1),
			message: fmt.Sprintf("Writing %s %d of %d", r.appends.item, i+1, r.count), takes: r.appends.take}
	}
	return srv.runSteps(ctx, in, r, steps, nil)
}
