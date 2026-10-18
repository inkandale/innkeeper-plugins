package main

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
)

// manifest is read for its config schema's defaults, so each default lives
// only in plugin.toml.
//
//go:embed plugin.toml
var manifest []byte

// settings is an instance's config over the schema's defaults.
type settings struct {
	MinTokens    int      `json:"min_tokens"`
	MaxTokens    int      `json:"max_tokens"`
	RepeatWords  int      `json:"repeat_words"`
	Placeholders []string `json:"placeholders"`
}

// schemaProperty is the part of a config schema property defaults come from.
type schemaProperty struct {
	Default    any
	Properties map[string]schemaProperty
}

// parseSettings lays config, which holds only what the owner changed, over
// the schema's defaults.
func parseSettings(config map[string]any) (settings, error) {
	var m struct{ Config schemaProperty }
	if _, err := toml.Decode(string(manifest), &m); err != nil {
		return settings{}, fmt.Errorf("plugin.toml: %w", err)
	}
	var s settings
	for _, layer := range []any{defaults(m.Config), config} {
		b, err := json.Marshal(layer)
		if err != nil {
			return settings{}, err
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return settings{}, fmt.Errorf("config: %w", err)
		}
	}
	return s, nil
}

// defaults is p's default, or for an object, its properties' defaults.
func defaults(p schemaProperty) any {
	if p.Properties == nil {
		return p.Default
	}
	out := map[string]any{}
	for k, sub := range p.Properties {
		out[k] = defaults(sub)
	}
	return out
}
