package main

import "strings"

// uiEvent is one ui.event for the panel. Set exactly one field.
type uiEvent struct {
	// Delta is reply text to add to what the panel shows.
	Delta string `json:"delta,omitempty"`
	// Reset clears the reply text the panel shows.
	Reset bool `json:"reset,omitempty"`
	// Conversation is the conversation this send created, for a Retry to resend to.
	Conversation int64 `json:"conversation,omitempty"`
}

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

// liveText streams one model request's visible text to the panel.
type liveText struct {
	show func(uiEvent)
	raw  strings.Builder
	sent string
}

func (l *liveText) add(piece string) {
	l.raw.WriteString(piece)
	text := withoutPartialTag(visible(l.raw.String()))
	// A late </think> can take back text already sent.
	if !strings.HasPrefix(text, l.sent) {
		l.show(uiEvent{Reset: true})
		l.sent = ""
	}
	if delta := text[len(l.sent):]; delta != "" {
		l.show(uiEvent{Delta: delta})
		l.sent = text
	}
}
