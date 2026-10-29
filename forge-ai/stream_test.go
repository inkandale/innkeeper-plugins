package main

import (
	"slices"
	"strings"
	"testing"
)

func TestVisibleDropsThinkBlocks(t *testing.T) {
	cases := map[string]string{
		`<think>plan</think>{"x":1}`:                  `{"x":1}`,
		`{"x":1}<think>more`:                          `{"x":1}`,
		"<think>a</think>\n{\"x\":<think>b</think>1}": "\n{\"x\":1}",
		`no think`: `no think`,
		// Qwen3 and R1 templates put the opening tag in the prompt.
		`reasoning only</think>{"x":1}`:    `{"x":1}`,
		`a</think>b</think>c`:              `c`,
		`<think>x</think>y</think>{"x":1}`: `{"x":1}`,
	}
	for in, want := range cases {
		if got := visible(in); got != want {
			t.Errorf("visible(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartedKeysOnPartialOutput(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"Sure! ```json\n{\"name\": \"Bo\", \"descr", []string{"name"}},
		{"```json\n{\"name\": \"Bo\", \"description\": \"A big", []string{"name", "description"}},
		{`{"name": "Bo", "tags": ["a", "b"], "first_mes": "Hi`, []string{"name", "tags", "first_mes"}},
		{`<think>{"wrong": 1}</think>{"name": "Bo"}`, []string{"name"}},
		{"Here is {{char}}'s card:\n```json\n{\"name\": \"Bo\", \"desc", []string{"name"}},
	}
	for _, c := range cases {
		if got := startedKeys(c.in); !slices.Equal(got, c.want) {
			t.Errorf("startedKeys(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseObjectAfterThinkAndFence(t *testing.T) {
	obj, err := parseObject("<think>hmm {not json}</think>\n```json\n{\"name\": \"Bo\", \"tags\": [\"x\"]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if string(obj["name"]) != `"Bo"` || string(obj["tags"]) != `["x"]` {
		t.Fatalf("parsed %v", obj)
	}
	obj, err = parseObject("Here is {{char}}'s card, with {braces}:\n```json\n{\"name\": \"Bo\"}\n```")
	if err != nil || string(obj["name"]) != `"Bo"` {
		t.Fatalf("object after macros: %v, %v", obj, err)
	}
	if _, err := parseObject("I can't help with that."); err == nil || !strings.Contains(err.Error(), "I can't help") {
		t.Fatalf("prose: err = %v, want one quoting the output", err)
	}
	if _, err := parseObject("```json\n{\"name\": \"Bo\", \"desc"); err == nil || !strings.Contains(err.Error(), "Max tokens") {
		t.Fatalf("truncated object: err = %v, want one naming Max tokens", err)
	}
}

func TestStartedItemsOnPartialOutput(t *testing.T) {
	cases := map[string]int{
		"":                           0,
		`{"entries": [`:              0,
		`{"entries": [{"keys": ["a"`: 1,
		"Sure: ```json\n{\"note\": \"{x}\", \"entries\": [{\"keys\": []}, {\"co": 2,
		`{"entries": [{}, {}, {}]}`:                     3,
		`{"other": [{}], "entries": [`:                  0,
		`<think>{"entries": [{}]}</think>{"entries": [`: 0,
	}
	for in, want := range cases {
		if got := startedItems(in, "entries"); got != want {
			t.Errorf("startedItems(%q) = %d, want %d", in, got, want)
		}
	}
}
