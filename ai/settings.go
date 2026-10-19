package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
)

// options lists the draft connection's models for the model field.
func options(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.SettingsOptionsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if p.Field != "/model" {
		return nil, protocol.InvalidParams(fmt.Errorf("no choices for %s", p.Field))
	}
	s, err := parseSettings(p.Config)
	if err != nil {
		return nil, err
	}
	apiKey := p.Secrets["api_key"]
	llm, err := s.provider(apiKey)
	if err != nil {
		return nil, err
	}
	if !listsModels(llm) {
		return nil, fmt.Errorf("%s doesn't list its models; type the model's name", s.Provider)
	}
	res, err := llm.(anyllm.ModelLister).ListModels(ctx)
	if err != nil {
		return nil, errors.New(hideKey("list models: "+err.Error(), apiKey))
	}
	ids := make([]string, 0, len(res.Data))
	for _, m := range res.Data {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	out := protocol.SettingsOptionsResult{Options: []protocol.SettingsOption{}}
	for _, id := range slices.Compact(ids) {
		out.Options = append(out.Options, protocol.SettingsOption{Value: id})
	}
	return out, nil
}

// hideKey removes apiKey from a message for the owner, since some providers
// echo the key they were sent.
func hideKey(msg, apiKey string) string {
	if apiKey == "" {
		return msg
	}
	return strings.ReplaceAll(msg, apiKey, "[redacted]")
}

// probeChat asks for one token.
func probeChat(ctx context.Context, c *connection) error {
	one := 1
	_, _, err := stream(ctx, c, anyllm.CompletionParams{Model: c.settings.Model, MaxTokens: &one,
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "Say OK."}}}, func(string) {})
	return err
}

// probeEmbed embeds one word.
func probeEmbed(ctx context.Context, c *connection) error {
	_, err := embedTexts(ctx, c, []string{"OK"})
	return err
}

// probeTimeout bounds Test connection, which a dead server would otherwise
// hold for the provider's hour-long timeout.
const probeTimeout = 30 * time.Second

// runSetting runs the Test connection button: one small real call per
// ticked kind, each reported with its latency.
func runSetting(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.SettingsRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if p.Action != "test" {
		return nil, protocol.InvalidParams(fmt.Errorf("no settings action %q", p.Action))
	}
	apiKey := p.Secrets["api_key"]
	c, err := connect(p.Config, apiKey)
	if err == nil && c.llm == nil {
		err = errors.New("choose a provider and a model first")
	}
	if err != nil {
		return protocol.SettingsRunResult{Message: hideKey(err.Error(), apiKey)}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res := protocol.SettingsRunResult{OK: true, Message: "Connected"}
	for _, name := range c.settings.Kinds {
		k := kinds[name]
		begin := time.Now()
		err := k.probe(ctx, c)
		// At least 1, since latency_ms is omitted when 0.
		check := protocol.SettingsCheck{Name: k.label, OK: err == nil, LatencyMS: max(1, time.Since(begin).Milliseconds())}
		if err != nil {
			check.Message = hideKey(err.Error(), apiKey)
			res.OK, res.Message = false, "A check failed"
		}
		res.Checks = append(res.Checks, check)
	}
	return res, nil
}
