// Command card-embed is an enrich plugin. It gives each card a vector for
// similar cards and search by meaning, from a summary of its premise when a
// chat connection is set, and answers vector.query with the same model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/inkandale/innkeeper/protocol"
)

// settings is an instance's config and what core told it at initialize.
type settings struct {
	Embed, Summary int64
	Concurrency    int
	// Vectors is whether core stores vectors; without it, embedding is wasted.
	Vectors bool
	conn    *protocol.Conn
}

func parseSettings(p protocol.InitializeParams, conn *protocol.Conn) *settings {
	id := func(key string) int64 { f, _ := p.Config[key].(float64); return int64(f) }
	s := &settings{Embed: id("embed_connection"), Summary: id("summary_connection"), Concurrency: 4,
		Vectors: slices.Contains(p.Features, protocol.FeatureVectors), conn: conn}
	if n, ok := p.Config["concurrency"].(float64); ok && n >= 1 {
		s.Concurrency = int(n)
	}
	return s
}

func plugin() protocol.Plugin[settings] {
	return protocol.Plugin[settings]{
		// Core's config check skips required settings, so start without them and fail when used.
		Init: func(ctx context.Context, p protocol.InitializeParams) (*settings, error) {
			return parseSettings(p, protocol.Peer(ctx)), nil
		},
		Methods: map[string]func(*settings, context.Context, json.RawMessage) (any, error){
			protocol.MethodEnrichRun:   (*settings).enrich,
			protocol.MethodVectorQuery: (*settings).query,
		},
		Services: func(*settings) []string { return []string{protocol.MethodVectorQuery} },
	}
}

func main() { protocol.Serve(plugin()) }

// space names the models and prompt behind a vector. The embed model comes
// from its reply; the summary model is named by its connection, since
// vector.query never calls it.
func (s *settings) space(embedModel string) string {
	out := "embed:" + embedModel
	if embedModel == "" {
		out = "embed:instance " + strconv.FormatInt(s.Embed, 10)
	}
	if s.Summary != 0 {
		out += fmt.Sprintf("|summary:%d|prompt:%s", s.Summary, promptVersion)
	}
	return out
}

// embed returns one vector per text from the embed connection, and its space.
func (s *settings) embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	if s.Embed == 0 {
		return nil, "", errors.New("pick an embedding connection in this plugin's settings")
	}
	var res protocol.AIEmbedResult
	if err := s.conn.Call(ctx, protocol.MethodAIEmbed, protocol.AIEmbedParams{
		ServiceCall: protocol.ServiceCall{Instance: s.Embed}, Texts: texts}, &res); err != nil {
		return nil, "", fmt.Errorf("embed: %w", err)
	}
	if err := res.Validate(len(texts)); err != nil {
		return nil, "", fmt.Errorf("embed: %w", err)
	}
	return res.Vectors, s.space(res.Model), nil
}

// query embeds a search query as written: a query reads like a premise already.
func (s *settings) query(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.VectorQueryParams
	if err := json.Unmarshal(params, &p); err != nil || p.Text == "" {
		return nil, protocol.InvalidParams(errors.New("vector.query needs text"))
	}
	vectors, space, err := s.embed(ctx, []string{p.Text})
	if err != nil {
		return nil, err
	}
	return protocol.VectorValue{Space: space, Vector: vectors[0]}, nil
}
