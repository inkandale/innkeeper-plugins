package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/config"
	"github.com/mozilla-ai/any-llm-go/providers/anthropic"
	"github.com/mozilla-ai/any-llm-go/providers/deepseek"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/groq"
	"github.com/mozilla-ai/any-llm-go/providers/llamacpp"
	"github.com/mozilla-ai/any-llm-go/providers/llamafile"
	"github.com/mozilla-ai/any-llm-go/providers/mistral"
	"github.com/mozilla-ai/any-llm-go/providers/ollama"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
)

// settings are one connection's config.
type settings struct {
	Provider string   `json:"provider"`
	BaseURL  string   `json:"base_url"`
	Model    string   `json:"model"`
	Kinds    []string `json:"kinds"`
}

// kind is one thing a connection can be for.
type kind struct {
	services []string
	label    string
	probe    func(context.Context, *connection) error
}

// kinds are the values of the config's kinds, each with the services it
// offers, its name in Test connection's report, and the smallest real call
// that proves it works. A chat model also answers ai.decide, as the fallback
// for a decision model; decide answers it through the server's /v1/systemone.
var kinds = map[string]kind{
	"chat":   {[]string{protocol.MethodAIChat, protocol.MethodAIDecide}, "Chat", probeChat},
	"embed":  {[]string{protocol.MethodAIEmbed}, "Embeddings", probeEmbed},
	"decide": {[]string{protocol.MethodAIDecide}, "Decisions", probeDecide},
}

// parseSettings reads a config. It doesn't require a model, since listing
// the provider's models comes before choosing one.
func parseSettings(cfg map[string]any) (settings, error) {
	var s settings
	b, err := json.Marshal(cfg)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		return s, fmt.Errorf("settings: %w", err)
	}
	if _, ok := providers[s.Provider]; !ok {
		return s, fmt.Errorf("settings: provider %q isn't one of %s", s.Provider, strings.Join(slices.Sorted(maps.Keys(providers)), ", "))
	}
	kindNames := strings.Join(slices.Sorted(maps.Keys(kinds)), ", ")
	// No kinds field means chat; a saved form with every box unticked sends [].
	if s.Kinds == nil {
		s.Kinds = []string{"chat"}
	}
	if len(s.Kinds) == 0 {
		return s, fmt.Errorf("settings: tick at least one of %s", kindNames)
	}
	for _, k := range s.Kinds {
		if _, ok := kinds[k]; !ok {
			return s, fmt.Errorf("settings: kind %q isn't one of %s", k, kindNames)
		}
	}
	return s, nil
}

// services are the service methods the connection offers, sorted.
func (s settings) services() []string {
	var out []string
	for _, k := range s.Kinds {
		out = append(out, kinds[k].services...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type newProvider func(...config.Option) (anyllm.Provider, error)

// wrap turns a provider package's New into a newProvider.
func wrap[P anyllm.Provider](f func(...config.Option) (P, error)) newProvider {
	return func(opts ...config.Option) (anyllm.Provider, error) {
		p, err := f(opts...)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}

var providers = map[string]newProvider{
	// For vLLM, llama.cpp servers, LM Studio, and OpenRouter, with or without a key.
	"openai-compatible": wrap(func(opts ...config.Option) (*openai.CompatibleProvider, error) {
		// The zero Capabilities would make embeds and listsModels refuse it.
		return openai.NewCompatible(openai.CompatibleConfig{Name: "openai-compatible", Capabilities: anyllm.Capabilities{
			Completion: true, CompletionImage: true, CompletionStreaming: true, CompletionTools: true,
			Embedding: true, ListModels: true,
		}}, opts...)
	}),
	"openai":    wrap(openai.New),
	"anthropic": wrap(anthropic.New),
	"gemini":    wrap(gemini.New),
	"mistral":   wrap(mistral.New),
	"groq":      wrap(groq.New),
	"deepseek":  wrap(deepseek.New),
	"ollama":    wrap(ollama.New),
	"llamacpp":  wrap(llamacpp.New),
	"llamafile": wrap(llamafile.New),
}

// provider builds the configured any-llm-go provider. Providers that need
// a key fail here without one.
func (s settings) provider(apiKey string) (anyllm.Provider, error) {
	if s.Provider == "openai-compatible" && s.BaseURL == "" {
		return nil, errors.New("settings: openai-compatible needs a base_url")
	}
	// A long generation on a local model can stream for many minutes; the
	// caller's context is what bounds it.
	opts := []config.Option{config.WithTimeout(time.Hour)}
	if apiKey != "" {
		opts = append(opts, config.WithAPIKey(apiKey))
	}
	if s.BaseURL != "" {
		opts = append(opts, config.WithBaseURL(s.BaseURL))
	}
	p, err := providers[s.Provider](opts...)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	return p, nil
}

// embeds reports whether llm can embed. Some providers share an
// implementation with an Embedding method but declare they host no
// embedding models.
func embeds(llm anyllm.Provider) bool {
	if _, ok := llm.(anyllm.EmbeddingProvider); !ok {
		return false
	}
	c, ok := llm.(anyllm.CapabilityProvider)
	return !ok || c.Capabilities().Embedding
}

// listsModels reports whether llm can list its models, on the same terms
// as embeds.
func listsModels(llm anyllm.Provider) bool {
	if _, ok := llm.(anyllm.ModelLister); !ok {
		return false
	}
	c, ok := llm.(anyllm.CapabilityProvider)
	return !ok || c.Capabilities().ListModels
}
