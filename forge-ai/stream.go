package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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
	for start := range objectStarts(v) {
		var obj map[string]json.RawMessage
		err := json.NewDecoder(strings.NewReader(start)).Decode(&obj)
		if err == nil {
			return obj, nil
		}
		cutOff = cutOff || errors.Is(err, io.ErrUnexpectedEOF)
	}
	if cutOff {
		return nil, errors.New("the model's reply stopped before its JSON object ended, most likely at the token limit; raise Max tokens under Advanced in Forge's settings")
	}
	if len(v) > 150 {
		v = v[:150] + "…"
	}
	return nil, fmt.Errorf("the model didn't return a JSON object: %q", v)
}

// startedItems counts the items the output has begun in the list under key,
// including one still streaming.
func startedItems(output, key string) int {
	for start := range objectStarts(visible(output)) {
		dec := json.NewDecoder(strings.NewReader(start))
		if _, err := dec.Token(); err != nil {
			continue
		}
		for dec.More() {
			tok, err := dec.Token()
			if k, ok := tok.(string); err != nil || !ok {
				break
			} else if k != key {
				var value json.RawMessage
				if dec.Decode(&value) != nil {
					break
				}
				continue
			}
			if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
				return 0
			}
			n := 0
			for dec.More() {
				// More reports an item at the end of partial input too.
				if strings.Trim(start[dec.InputOffset():], " \t\r\n,") == "" {
					break
				}
				n++
				var item json.RawMessage
				if dec.Decode(&item) != nil {
					break
				}
			}
			return n
		}
	}
	return 0
}
