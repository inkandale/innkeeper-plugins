package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/protocol"
)

// qualityKey holds the evaluation's overall score; each dimension's score
// goes under the dimension's key.
const qualityKey = "quality"

// evaluatePrompt names the evaluate setup in each value's meta: a hash of the
// template and the settings that change the prompt or the output, so scores
// from different setups don't look comparable.
func evaluatePrompt(s settings) (string, error) {
	tmpl, err := promptFiles.ReadFile("prompts/evaluate.tmpl")
	if err != nil {
		return "", err
	}
	a, _ := findAction("evaluate")
	temperature, maxTokens := a.limits(s)
	h := sha256.New()
	h.Write(tmpl)
	// JSON keeps the fields apart, so no two setups hash the same text.
	// A nil override is the default; an empty one is no instructions.
	var override *string
	if o, ok := s.Prompts[a.ID]; ok {
		override = &o
	}
	setup, err := json.Marshal([]any{s.Prompts["house_style"], override, temperature, maxTokens})
	if err != nil {
		return "", err
	}
	h.Write(setup)
	return "evaluate-" + hex.EncodeToString(h.Sum(nil)[:4]), nil
}

// enrich scores each revision with the evaluate action. One revision's
// failure is its own error; the rest of the batch goes on.
func (srv *server) enrich(ctx context.Context, in *instance, params json.RawMessage) (*protocol.EnrichRunResult, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid("%v", err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, invalid("revisions must hold 1 to %d", protocol.MaxEnrichBatch)
	}
	if in.err != nil {
		return nil, in.err
	}
	res := &protocol.EnrichRunResult{Results: []protocol.EnrichResult{}}
	// ponytail: one revision at a time; run a few concurrently if batches are slow.
	for i, rev := range p.Revisions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_ = srv.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: int64(i), Total: int64(len(p.Revisions)),
			Message: fmt.Sprintf("Evaluating %s (%d of %d)", revisionName(rev), i+1, len(p.Revisions))})
		values, vm, err := srv.scores(ctx, in, p.Run, rev)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		meta, _ := json.Marshal(vm)
		out := protocol.EnrichResult{RevisionID: rev.RevisionID, Values: values, Meta: meta}
		if err != nil {
			out = protocol.EnrichResult{RevisionID: rev.RevisionID, Error: err.Error()}
		}
		res.Results = append(res.Results, out)
	}
	return res, nil
}

// valueMeta is stored with each value: the model and the prompt's version.
type valueMeta struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// scores returns one revision's scores by enrichment key, each 0 to 100.
// With a decide connection the decision model scores it; a failure or a
// decision under the confidence threshold falls back to the chat model.
func (srv *server) scores(ctx context.Context, in *instance, run string, rev protocol.EnrichRevision) (map[string]json.RawMessage, valueMeta, error) {
	if in.settings.Decide != 0 {
		values, vm, err := srv.decideScores(ctx, in.settings, rev)
		if err == nil || ctx.Err() != nil {
			return values, vm, err
		}
	}
	return srv.evaluateScores(ctx, in, run, rev)
}

// evaluateScores runs evaluate on one revision with the chat model.
func (srv *server) evaluateScores(ctx context.Context, in *instance, run string, rev protocol.EnrichRevision) (map[string]json.RawMessage, valueMeta, error) {
	prompt, err := evaluatePrompt(in.settings)
	if err != nil {
		return nil, valueMeta{}, err
	}
	r, err := parseRun(protocol.ActionRunParams{Run: run, Action: "evaluate",
		Entity: protocol.ActionEntity{Kind: "card", Data: rev.Data}})
	if err != nil {
		return nil, valueMeta{}, err
	}
	res, err := srv.perform(ctx, in, r, false, func() func(string) { return func(string) {} })
	if err != nil {
		return nil, valueMeta{}, err
	}
	values := map[string]json.RawMessage{}
	add := func(key string, score float64) {
		values[key], _ = json.Marshal(min(max(score, 0), 100))
	}
	if res.Report.Score != nil {
		add(qualityKey, *res.Report.Score)
	}
	for _, d := range res.Report.Dimensions {
		add(d.Key, d.Score)
	}
	if len(values) == 0 {
		return nil, valueMeta{}, errors.New("the model's evaluation has no scores")
	}
	return values, valueMeta{Model: r.model, Prompt: prompt}, nil
}

// revisionName is the card's name for progress messages.
func revisionName(rev protocol.EnrichRevision) string {
	var card struct{ Name string }
	if json.Unmarshal(rev.Data, &card) == nil && card.Name != "" {
		return card.Name
	}
	return fmt.Sprintf("revision %d", rev.RevisionID)
}
