package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper/protocol"
)

// rawChars caps a card's own text sent to the embedding model; most read
// fewer than 8,192 tokens.
const rawChars = 8000

// enrich summarizes a batch's cards, up to Concurrency at once, then embeds
// them in one call. A card whose summary fails gets no vector, so no card
// has vectors from two kinds of text.
func (s *settings) enrich(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, protocol.InvalidParams(fmt.Errorf("revisions must hold 1 to %d", protocol.MaxEnrichBatch))
	}
	if s.Summary == 0 && !s.Vectors {
		return nil, errors.New("nothing to write: pick a summary connection, or install pgvector so vectors can be stored")
	}
	results := make([]protocol.EnrichResult, len(p.Revisions))
	texts := make([]string, len(p.Revisions))
	var wg sync.WaitGroup
	slots := make(chan struct{}, s.Concurrency)
	for i, rev := range p.Revisions {
		results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Values: map[string]json.RawMessage{}}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			text, err := s.cardText(ctx, rev, results[i].Values)
			if err != nil {
				results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Error: err.Error()}
				return
			}
			texts[i] = text
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Vectors {
		s.addVectors(ctx, results, texts)
	}
	return &protocol.EnrichRunResult{Results: results}, nil
}

// cardText is the text to embed for rev: its premise and themes when a
// summary connection is set, written into values, else its own fields.
func (s *settings) cardText(ctx context.Context, rev protocol.EnrichRevision, values map[string]json.RawMessage) (string, error) {
	if s.Summary == 0 {
		return rawText(rev.Data)
	}
	summary, err := summarize(ctx, s.conn, s, rev.Data)
	if err != nil {
		return "", err
	}
	values[premiseKey], values[themesKey] = summary[premiseKey], summary[themesKey]
	var premise string
	var themes []string
	_ = json.Unmarshal(summary[premiseKey], &premise)
	_ = json.Unmarshal(summary[themesKey], &themes)
	return premise + "\n" + strings.Join(themes, ", "), nil
}

// rawText is the fields that say who the character is, leaving out
// greetings and example dialogue so the vector isn't about prose style.
func rawText(data json.RawMessage) (string, error) {
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		return "", fmt.Errorf("read card: %w", err)
	}
	var parts []string
	for _, f := range []string{"name", "description", "personality", "scenario", "creator_notes"} {
		if v, _ := card[f].(string); strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		return "", errors.New("the card has no text to embed")
	}
	if len(text) > rawChars {
		text = strings.ToValidUTF8(text[:rawChars], "")
	}
	return text, nil
}

// addVectors embeds the texts of the cards that got one, in one call. If
// that call fails, each card is tried alone, so one text the model rejects
// fails only its own card.
func (s *settings) addVectors(ctx context.Context, results []protocol.EnrichResult, texts []string) {
	var idx []int
	var batch []string
	for i, t := range texts {
		if results[i].Error == "" {
			idx, batch = append(idx, i), append(batch, t)
		}
	}
	if len(batch) == 0 {
		return
	}
	set := func(i int, v []float32, space string) {
		raw, _ := json.Marshal(protocol.VectorValue{Space: space, Vector: v})
		results[i].Values["embedding"] = raw
	}
	if vectors, space, err := s.embed(ctx, batch); err == nil {
		for k, i := range idx {
			set(i, vectors[k], space)
		}
		return
	}
	for k, i := range idx {
		vectors, space, err := s.embed(ctx, batch[k:k+1])
		if err != nil {
			results[i] = protocol.EnrichResult{RevisionID: results[i].RevisionID, Error: err.Error()}
			continue
		}
		set(i, vectors[0], space)
	}
}
