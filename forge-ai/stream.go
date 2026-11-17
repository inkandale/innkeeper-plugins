package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/kaptinlin/jsonrepair"
)

// visible is the model's output without reasoning. A <think> block is
// removed, an unclosed one hides the rest since the model is still reasoning,
// and a </think> with no opening tag (Qwen3 and R1 templates put that in the
// prompt) drops everything before it.
func visible(s string) string {
	var b strings.Builder
	for {
		open, end := strings.Index(s, "<think>"), strings.Index(s, "</think>")
		if end >= 0 && (open < 0 || end < open) {
			b.Reset()
			s = s[end+len("</think>"):]
			continue
		}
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])
		end = strings.Index(s[open:], "</think>")
		if end < 0 {
			return b.String()
		}
		s = s[open+end+len("</think>"):]
	}
}

// objectStarts yields the output from each '{' on, since prose before the
// JSON may hold braces of its own, such as {{char}}.
func objectStarts(v string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for i := strings.IndexByte(v, '{'); i >= 0; {
			if !yield(v[i:]) {
				return
			}
			next := strings.IndexByte(v[i+1:], '{')
			if next < 0 {
				return
			}
			i += 1 + next
		}
	}
}

// startedKeys lists the top-level keys of the JSON object the output has
// begun, in order, including one whose value is still streaming.
func startedKeys(output string) []string {
	for start := range objectStarts(visible(output)) {
		if keys := keysFrom(start); len(keys) > 0 {
			return keys
		}
	}
	return nil
}

func keysFrom(s string) []string {
	dec := json.NewDecoder(strings.NewReader(s))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			break
		}
		keys = append(keys, key)
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			break
		}
	}
	return keys
}

// parseObject reads the first JSON object in the output, after any reasoning
// and around any code fence or prose.
func parseObject(output string) (map[string]json.RawMessage, error) {
	v := strings.TrimSpace(visible(output))
	cutOff := false
	// The start that decodes furthest is the model's object, not a {{macro}}.
	var syntaxErr *json.SyntaxError
	for start := range objectStarts(v) {
		var obj map[string]json.RawMessage
		err := json.NewDecoder(strings.NewReader(start)).Decode(&obj)
		if err == nil {
			return obj, nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			cutOff = true
			continue
		}
		var se *json.SyntaxError
		if errors.As(err, &se) && (syntaxErr == nil || se.Offset > syntaxErr.Offset) {
			syntaxErr = se
		}
		if obj := repairObject(start); obj != nil {
			return obj, nil
		}
	}
	if cutOff {
		return nil, errors.New("the model's reply stopped before its JSON object ended, most likely at the token limit; raise Max tokens under Advanced in Forge's settings")
	}
	if len(v) > 150 {
		v = v[:150] + "…"
	}
	if syntaxErr != nil {
		return nil, fmt.Errorf("the model didn't return valid JSON (%v): %q", syntaxErr, v)
	}
	return nil, fmt.Errorf("the model didn't return a JSON object: %q", v)
}

// plainText is a plain-text reply without reasoning or a code fence around
// all of it.
func plainText(output string) string {
	text := strings.TrimSpace(visible(output))
	if fenced, ok := strings.CutPrefix(text, "```"); ok {
		if _, body, ok := strings.Cut(fenced, "\n"); ok {
			if body, ok = strings.CutSuffix(strings.TrimRight(body, " \t\n"), "```"); ok {
				text = strings.TrimSpace(body)
			}
		}
	}
	return text
}

// fieldName matches the top-level keys Forge asks a model for, such as first_mes.
var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// repairObject fixes unescaped quotes and raw line breaks in a model's JSON
// object, or returns nil. A repair that would silently drop text is refused:
// closing a reply cut off before its final }, or turning prose into a key,
// as with "Wait", he says.
func repairObject(start string) map[string]json.RawMessage {
	if !strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(start), "```")), "}") {
		return nil
	}
	repaired, err := jsonrepair.Repair(start)
	if err != nil {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(repaired), &obj) != nil {
		return nil
	}
	for key := range obj {
		if !fieldName.MatchString(key) {
			return nil
		}
	}
	return obj
}

// withoutPartialTag holds back the end of s while it could still become a
// think tag, so the tag's first pieces never reach the panel.
func withoutPartialTag(s string) string {
	for _, tag := range []string{"</think>", "<think>"} {
		for n := len(tag) - 1; n > 0; n-- {
			if strings.HasSuffix(s, tag[:n]) {
				return s[:len(s)-n]
			}
		}
	}
	return s
}
