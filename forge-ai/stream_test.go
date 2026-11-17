package main

import (
	"encoding/json"
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

func TestParseObjectAcceptsRawLineBreaksInStrings(t *testing.T) {
	obj, err := parseObject("```json\n{\n  \"first_mes\": \"{{user}} walks in.\n\n\t\\\"Hi,\\\" she says.\r\n\"\n}\n```")
	if err != nil {
		t.Fatal(err)
	}
	wantField(t, obj, "first_mes", "{{user}} walks in.\n\n\t\"Hi,\" she says.\r\n")
}

func TestParseObjectAcceptsUnescapedDialogueQuotes(t *testing.T) {
	obj, err := parseObject("```json\n{\n \"first_mes\": \"She jogs past. \"Hey!\" she calls to {{user}}.\n\nShe stops.\"\n}\n```")
	if err != nil {
		t.Fatal(err)
	}
	wantField(t, obj, "first_mes", "She jogs past. \"Hey!\" she calls to {{user}}.\n\nShe stops.")
}

func TestParseObjectRefusesARepairThatDropsText(t *testing.T) {
	for _, reply := range []string{
		"```json\n{\"first_mes\": \"\"Wait\", he says. She doesn't.\"}\n```",
		"```json\n{\"first_mes\": \"She runs.\n\nShe sto",
	} {
		_, err := parseObject(reply)
		if err == nil || !strings.Contains(err.Error(), "didn't return valid JSON (invalid character") {
			t.Fatalf("%q: err = %v, want the decoder's reason", reply, err)
		}
	}
}

func wantField(t *testing.T, obj map[string]json.RawMessage, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(obj[key], &got); err != nil || got != want {
		t.Fatalf("%s = %s (%v), want %q", key, obj[key], err, want)
	}
}
