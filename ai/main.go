// Command ai offers model services to other plugins, ai.chat and ai.embed
// through any-llm-go, and ai.decide. One instance is one connection to a model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
)

const version = "0.2.0"

// connection is what initialize configured. llm is nil for the empty config
// the core's settings process starts with.
type connection struct {
	settings settings
	llm      anyllm.Provider
	// apiKey is for /v1/systemone, which any-llm-go doesn't call.
	apiKey string
}

type server struct {
	conn *protocol.Conn
	// Set by initialize; requests run on other goroutines.
	cur atomic.Pointer[connection]
}

func main() {
	srv := &server{}
	srv.conn = protocol.NewConn(os.Stdin, os.Stdout, srv.handle)
	if err := srv.conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (srv *server) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodInitialize:
		var p protocol.InitializeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, invalid("%v", err)
		}
		c, err := connect(p.Config, p.Secrets["api_key"])
		if err != nil {
			return nil, err
		}
		srv.cur.Store(c)
		return protocol.InitializeResult{Protocol: protocol.Version, Name: "ai", Version: version,
			Capabilities: []string{"ai", "settings"}, Services: c.services()}, nil
	case protocol.MethodShutdown:
		return nil, nil
	case protocol.MethodAIChat:
		return srv.chat(ctx, params)
	case protocol.MethodAIEmbed:
		return srv.embed(ctx, params)
	case protocol.MethodAIDecide:
		return srv.decide(ctx, params)
	case protocol.MethodSettingsOptions:
		return options(ctx, params)
	case protocol.MethodSettingsRun:
		return runSetting(ctx, params)
	}
	return nil, protocol.MethodNotFound(method)
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
		return nil, fmt.Errorf("settings: %s doesn't offer embeddings", s.Provider)
	}
	if slices.Contains(s.Kinds, "decide") && s.BaseURL == "" {
		return nil, errors.New("settings: decide needs a base_url, the URL its /v1/systemone is under, such as http://localhost:8080/v1")
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

// offering returns the connection when it offers method.
func (srv *server) offering(method string) (*connection, error) {
	c := srv.cur.Load()
	if c == nil || !slices.Contains(c.services(), method) {
		return nil, protocol.MethodNotFound(method)
	}
	return c, nil
}

func invalid(format string, args ...any) error {
	return protocol.NewError(protocol.CodeInvalidParams, fmt.Sprintf(format, args...), false)
}
