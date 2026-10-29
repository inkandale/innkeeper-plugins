package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// planLimits are the plan call's temperature and max tokens, unless the
// instance sets its own.
var planLimits = action{temperature: 0.3, maxTokens: 4096}

// wholeCard runs a field action on the whole card: one call plans a note per
// field, then the action runs on each planned field in turn, against the card
// as the fields before it left it.
func (srv *server) wholeCard(ctx context.Context, in *instance, r *run) (*protocol.ActionRunResult, error) {
	_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Total: 1, Message: "Planning " + r.Label})
	notes, planText, err := srv.plan(ctx, in, r)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	raw := []string{"Plan:\n" + planText}
	var planned []string
	for _, f := range r.Fields {
		if notes[f] != "" {
			planned = append(planned, f)
		}
	}
	for i, f := range planned {
		_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(i + 1), Total: int64(len(planned) + 1),
			Message: fmt.Sprintf("%s: %s (%d of %d)", r.Label, label(f), i+1, len(planned))})
		// section shares r.draft; its proposal copies the draft before changing
		// its field, and decoding it back into r.draft shows the next section the change.
		section := *r
		section.Field, section.note = f, notes[f]
		res, err := srv.perform(ctx, in, &section, true, noProgress)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label(f), err)
		}
		raw = append(raw, label(f)+":\n"+res.Raw)
		if err := json.Unmarshal(res.Proposal, &r.draft); err != nil {
			return nil, err
		}
	}
	proposal, err := json.Marshal(r.draft)
	if err != nil {
		return nil, err
	}
	return &protocol.ActionRunResult{Proposal: proposal, Raw: strings.Join(raw, "\n\n")}, nil
}

// plan returns the model's note per field of r's, leaving out each it skips
// or doesn't mention, and the model's reply text. Keys that aren't r's fields
// are ignored.
func (srv *server) plan(ctx context.Context, in *instance, r *run) (map[string]string, string, error) {
	system, user, err := r.planPrompts(in.settings)
	if err != nil {
		return nil, "", err
	}
	temperature, maxTokens := planLimits.limits(in.settings)
	req := protocol.AIChatParams{Temperature: &temperature, MaxTokens: &maxTokens,
		Messages: []protocol.ChatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}}
	reply, err := srv.complete(ctx, in, r.Run, req, toolbox{}, noProgress)
	if err != nil {
		return nil, "", err
	}
	notes, err := planNotes(reply.Text, r.Fields)
	if err == nil {
		return notes, reply.Text, nil
	}
	// Some models copy each field's text into the plan despite the prompt and run out of tokens mid-object.
	req.Messages = append(req.Messages,
		protocol.ChatMessage{Role: "assistant", Content: reply.Text},
		protocol.ChatMessage{Role: "user", Content: "That reply couldn't be used: " + err.Error() +
			". Answer again with only the JSON object: a note of one to three sentences per field, saying what to change, not the field's text."})
	if reply, err = srv.complete(ctx, in, r.Run, req, toolbox{}, noProgress); err != nil {
		return nil, "", err
	}
	notes, err = planNotes(reply.Text, r.Fields)
	return notes, reply.Text, err
}

// planNotes reads the plan's note per field out of its reply.
func planNotes(text string, fields []string) (map[string]string, error) {
	obj, err := parseObject(text)
	if err != nil {
		return nil, err
	}
	notes := map[string]string{}
	for _, f := range fields {
		raw, ok := obj[f]
		if !ok || string(raw) == "null" {
			continue
		}
		var note string
		if err := json.Unmarshal(raw, &note); err != nil {
			return nil, fmt.Errorf("the model's note for %s isn't text", f)
		}
		if note = strings.TrimSpace(note); note != "" && !strings.EqualFold(note, "skip") {
			notes[f] = note
		}
	}
	return notes, nil
}

// noProgress gives a request no progress messages of its own.
func noProgress() func(string) { return func(string) {} }
