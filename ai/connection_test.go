package main

import (
	"slices"
	"strings"
	"testing"
)

func TestConnectRefusesBadSettings(t *testing.T) {
	cases := []struct {
		config  map[string]any
		key     string
		wantErr string
	}{
		{map[string]any{"provider": "openai", "model": "gpt-5"}, "", "API key"},
		{map[string]any{"provider": "openai", "model": "gpt-5"}, "sk-x", ""},
		{map[string]any{"provider": "anthropic", "model": "claude"}, "k", ""},
		{map[string]any{"provider": "openai-compatible", "model": "m", "base_url": "http://localhost:8000/v1"}, "", ""},
		{map[string]any{"provider": "openai-compatible", "model": "m"}, "", "Base URL"},
		{map[string]any{"provider": "ollama", "model": "llama3"}, "", ""},
		{map[string]any{"provider": "llamacpp", "model": "m"}, "", ""},
		{map[string]any{"provider": "llamafile", "model": "m"}, "", ""},
		{map[string]any{"provider": "gemini", "model": "m"}, "k", ""},
		{map[string]any{"provider": "mistral", "model": "m"}, "k", ""},
		{map[string]any{"provider": "groq", "model": "m"}, "k", ""},
		{map[string]any{"provider": "deepseek", "model": "m"}, "k", ""},
		{map[string]any{"provider": "skynet", "model": "m"}, "", "provider"},
		{map[string]any{"provider": "ollama"}, "", "model"},
		{map[string]any{"provider": "ollama", "model": "m", "kinds": []any{"draw"}}, "", "kind"},
		{map[string]any{"provider": "ollama", "model": "m", "kinds": []any{}}, "", "tick"},
		{map[string]any{"provider": "openai", "model": "e", "kinds": []any{"embed"}}, "k", ""},
		{map[string]any{"provider": "ollama", "model": "e", "kinds": []any{"embed"}}, "", ""},
		{map[string]any{"provider": "openai-compatible", "model": "e", "base_url": "http://localhost:8000/v1", "kinds": []any{"embed"}}, "", ""},
		{map[string]any{"provider": "anthropic", "model": "c", "kinds": []any{"embed"}}, "k", "embeddings"},
		{map[string]any{"provider": "groq", "model": "m", "kinds": []any{"chat", "embed"}}, "k", "embeddings"},
	}
	for _, c := range cases {
		_, err := connect(c.config, c.key)
		if c.wantErr == "" && err != nil {
			t.Errorf("%v: %v", c.config, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%v: err = %v, want one mentioning %q", c.config, err, c.wantErr)
		}
	}
}

func TestParseSettingsDefaultsAbsentKindsToChat(t *testing.T) {
	s, err := parseSettings(map[string]any{"provider": "ollama", "model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Kinds, []string{"chat"}) {
		t.Errorf("kinds = %v, want [chat]", s.Kinds)
	}
}

func TestOpenAICompatibleListsModels(t *testing.T) {
	llm, err := settings{Provider: "openai-compatible", BaseURL: "http://localhost:8000/v1"}.provider("")
	if err != nil {
		t.Fatal(err)
	}
	if !listsModels(llm) {
		t.Error("openai-compatible doesn't list models")
	}
}
