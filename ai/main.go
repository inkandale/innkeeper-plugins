// Command ai offers model services to other plugins, ai.chat and ai.embed
// through any-llm-go, and ai.decide. One instance is one connection to a model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
)

// connection is what initialize configured. llm is nil for the empty config
// the core's settings process starts with.
type connection struct {
	settings settings
	llm      anyllm.Provider
	// apiKey is for /v1/systemone, which any-llm-go doesn't call.
	apiKey string
}

func main() {
	protocol.Serve(plugin())
}

// plugin answers each call with the connection initialize configured.
func plugin() protocol.Plugin[connection] {
	return protocol.Plugin[connection]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*connection, error) {
			return connect(p.Config, p.Secrets["api_key"])
		},
		Methods: map[string]func(*connection, context.Context, json.RawMessage) (any, error){
			protocol.MethodAIChat:   (*connection).chat,
			protocol.MethodAIEmbed:  (*connection).embed,
			protocol.MethodAIDecide: (*connection).decide,
			protocol.MethodSettingsOptions: func(_ *connection, ctx context.Context, params json.RawMessage) (any, error) {
				return options(ctx, params)
			},
			protocol.MethodSettingsRun: func(_ *connection, ctx context.Context, params json.RawMessage) (any, error) {
				return runSetting(ctx, params)
			},
		},
		Services: (*connection).services,
	}
}

// connect builds the connection a config describes. An empty config gives
// a connection that offers nothing.
func connect(cfg map[string]any, apiKey string) (*connection, error) {
	if len(cfg) == 0 {
		return &connection{}, nil
	}
	s, err := parseSettings(cfg)
	if err != nil {
		return nil, err
	}
	if s.Model == "" {
		return nil, errors.New("settings: model is required")
	}
	llm, err := s.provider(apiKey)
	if err != nil {
		return nil, err
	}
	if slices.Contains(s.Kinds, "embed") && !embeds(llm) {
		return nil, fmt.Errorf("%s doesn't offer embeddings, so untick embed under Provides", s.Provider)
	}
	if slices.Contains(s.Kinds, "decide") && s.BaseURL == "" {
		return nil, errors.New("Fill in Base URL first: the address of the decision model, the part before /v1/systemone")
	}
	return &connection{settings: s, llm: llm, apiKey: apiKey}, nil
}

// services are what the connection offers; none before it's configured.
func (c *connection) services() []string {
	if c.llm == nil {
		return nil
	}
	return c.settings.services()
}

// offering returns MethodNotFound unless the connection offers method.
func (c *connection) offering(method string) error {
	if !slices.Contains(c.services(), method) {
		return protocol.MethodNotFound(method)
	}
	return nil
}
