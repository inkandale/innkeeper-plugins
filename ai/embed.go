package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
)

func (c *connection) embed(ctx context.Context, params json.RawMessage) (any, error) {
	if err := c.offering(protocol.MethodAIEmbed); err != nil {
		return nil, err
	}
	var p protocol.AIEmbedParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Texts) == 0 {
		return nil, protocol.InvalidParams(errors.New("ai.embed needs texts"))
	}
	vectors, err := embedTexts(ctx, c, p.Texts)
	if err != nil {
		return nil, modelError(err)
	}
	return protocol.AIEmbedResult{Vectors: vectors, Model: c.settings.Model}, nil
}

// embedTexts returns one vector per text, in the texts' order. connect
// already checked the provider embeds.
func embedTexts(ctx context.Context, c *connection, texts []string) ([][]float32, error) {
	res, err := c.llm.(anyllm.EmbeddingProvider).Embedding(ctx, anyllm.EmbeddingParams{Model: c.settings.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	if len(res.Data) != len(texts) {
		return nil, fmt.Errorf("the provider returned %d vectors for %d texts", len(res.Data), len(texts))
	}
	vectors := make([][]float32, len(texts))
	for _, d := range res.Data {
		if d.Index < 0 || d.Index >= len(texts) || vectors[d.Index] != nil {
			return nil, fmt.Errorf("the provider returned vector %d for %d texts", d.Index, len(texts))
		}
		v := make([]float32, len(d.Embedding))
		for i, x := range d.Embedding {
			v[i] = float32(x)
		}
		vectors[d.Index] = v
	}
	return vectors, nil
}
