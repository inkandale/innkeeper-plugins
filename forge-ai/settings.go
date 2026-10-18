package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

// settings are the instance's config.
type settings struct {
	// Connection is the ai instance every model call goes to.
	Connection int64 `json:"connection"`
	// Decide, if set, is the ai instance that scores cards first with ai.decide.
	Decide int64 `json:"decide"`
	// EscalateBelow is the mean confidence under which the chat model re-scores a card.
	EscalateBelow *float64 `json:"escalate_below"`
	Temperature   *float64 `json:"temperature"`
	MaxTokens     *int     `json:"max_tokens"`
	// Prompts are the owner's overrides by instructions key, and house_style.
	// A key present with empty text is an override: no instructions.
	Prompts map[string]string `json:"prompts"`
}

func parseSettings(cfg map[string]any) (settings, error) {
	var s settings
	b, err := json.Marshal(cfg)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		return s, fmt.Errorf("settings: %w", err)
	}
	if s.Connection == 0 {
		return s, errors.New("settings: pick an AI connection")
	}
	return s, nil
}

func (s settings) escalateBelow() float64 {
	if s.EscalateBelow == nil {
		return defaultEscalateBelow
	}
	return *s.EscalateBelow
}
