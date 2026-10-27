package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/abadojack/whatlanggo"
)

// card is the part of a revision's CCv3 data the checks read.
type card struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Personality  string `json:"personality"`
	Scenario     string `json:"scenario"`
	FirstMessage string `json:"first_mes"`
	SystemPrompt string `json:"system_prompt"`
	PostHistory  string `json:"post_history_instructions"`
	// Alternates are the other greetings an app offers by swiping.
	Alternates []string `json:"alternate_greetings"`
}

// hasAlternate reports whether the card has a greeting besides its first message.
func (c card) hasAlternate() bool {
	return slices.ContainsFunc(c.Alternates, func(g string) bool { return !blank(g) })
}

// alwaysOn are the fields sent with every message.
func (c card) alwaysOn() []string {
	return []string{c.Description, c.Personality, c.Scenario, c.SystemPrompt, c.PostHistory}
}

func (c card) all() []string { return append(c.alwaysOn(), c.Name, c.FirstMessage) }

// tokens estimates a text's token count as a quarter of its characters, the
// web editor's rule. It undercounts Chinese, Japanese, and Korean.
func tokens(texts ...string) int {
	n := 0
	for _, t := range texts {
		n += utf8.RuneCountInString(t)
	}
	return (n + 3) / 4
}

// facts are what the checks compare against the settings.
type facts struct {
	card     card
	tokens   int
	settings settings
}

// checks are in the order problems lists them. Each takes its points off
// lint's 100; a card missing what roleplay needs loses them all.
var checks = []struct {
	code   string
	points int
	fails  func(f facts) bool
}{
	{"no_name", 100, func(f facts) bool { return blank(f.card.Name) }},
	{"no_description", 100, func(f facts) bool { return blank(f.card.Description) }},
	{"no_first_message", 100, func(f facts) bool { return blank(f.card.FirstMessage) && !f.card.hasAlternate() }},
	// The chat opens on an empty message until the owner swipes to an alternate.
	{"empty_first_message", 10, func(f facts) bool { return blank(f.card.FirstMessage) && f.card.hasAlternate() }},
	{"too_short", 100, func(f facts) bool {
		return tokens(f.card.Description, f.card.Personality, f.card.Scenario) < f.settings.MinTokens
	}},
	{"oversized", 20, func(f facts) bool { return f.tokens > f.settings.MaxTokens }},
	{"repeated_text", 15, func(f facts) bool { return repeated(f.card.alwaysOn(), f.settings.RepeatWords) }},
	{"first_message_copies_description", 15, func(f facts) bool {
		first, _, _ := strings.Cut(strings.TrimSpace(f.card.Description), "\n")
		return wordCount(first) >= f.settings.RepeatWords && strings.Contains(normalize(f.card.FirstMessage), normalize(first))
	}},
	{"placeholder", 15, func(f facts) bool { return slices.ContainsFunc(f.card.all(), f.hasPlaceholder) }},
	{"no_char_macro", 10, func(f facts) bool {
		names := func(t string) bool {
			t = strings.ToLower(t)
			return strings.Contains(t, "{{char}}") || !blank(f.card.Name) && strings.Contains(t, strings.ToLower(strings.TrimSpace(f.card.Name)))
		}
		return !names(f.card.Description) && !names(f.card.FirstMessage)
	}},
	{"markup", 10, func(f facts) bool { return slices.ContainsFunc(f.card.alwaysOn(), htmlTag.MatchString) }},
	{"mojibake", 10, func(f facts) bool { return slices.ContainsFunc(f.card.all(), garbled.MatchString) }},
}

var (
	// macro is a {{...}} macro, which apps replace before the model sees it.
	macro   = regexp.MustCompile(`\{\{[^{}]*\}\}`)
	htmlTag = regexp.MustCompile(`(?i)</?(?:div|span|p|br|hr|img|style|table|tr|td|font|center|details|summary|a|b|i|u|h[1-6]|ul|ol|li)(?:\s[^>]*)?/?>`)
	// garbled is UTF-8 read as Windows-1252, such as é as Ã©, or a replacement character.
	garbled = regexp.MustCompile(`â€|Ã[\x{A0}-\x{BF}]|\x{FFFD}`)
)

func (f facts) hasPlaceholder(text string) bool {
	text = strings.ToLower(macro.ReplaceAllString(text, ""))
	return slices.ContainsFunc(f.settings.Placeholders, func(p string) bool {
		return p != "" && strings.Contains(text, strings.ToLower(p))
	})
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// normalize lowercases text and collapses its whitespace, so copies match
// across line breaks.
func normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func wordCount(s string) int { return len(strings.Fields(s)) }

// repeated reports whether a paragraph of at least minWords words in one
// field appears in another.
func repeated(fields []string, minWords int) bool {
	norm := make([]string, len(fields))
	for i, f := range fields {
		norm[i] = normalize(f)
	}
	for i, f := range fields {
		for para := range strings.SplitSeq(f, "\n") {
			if wordCount(para) < minWords {
				continue
			}
			p := normalize(para)
			for j, other := range norm {
				if j != i && strings.Contains(other, p) {
					return true
				}
			}
		}
	}
	return false
}

// result is one card's lint.
type result struct {
	score    int
	tokens   int
	language string
	problems []string
}

func lint(s settings, c card) result {
	f := facts{card: c, tokens: tokens(c.alwaysOn()...), settings: s}
	r := result{score: 100, tokens: f.tokens, language: language(c.Description, c.FirstMessage), problems: []string{}}
	for _, ch := range checks {
		if ch.fails(f) {
			r.problems = append(r.problems, ch.code)
			r.score -= ch.points
		}
	}
	r.score = max(r.score, 0)
	return r
}

// maxTokens is the top of the tokens enrichment's range.
const maxTokens = 1_000_000

func (r result) values() (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range map[string]any{"lint": r.score, "tokens": min(r.tokens, maxTokens), "language": r.language, "problems": r.problems} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

// unknownLanguage is the language of a card too short or mixed to tell.
const unknownLanguage = "Unknown"

// languageSample is how many characters language reads. Past about 4,000,
// whatlanggo's confidence goes negative and it calls English unreliable.
const languageSample = 2000

// language names the texts' language, such as English or Japanese, after
// macros and markup are removed.
// ponytail: whatlanggo confuses short Cyrillic text (Ukrainian reads as Serbian, so Unknown); lingua-go is accurate but adds 130 MB.
func language(texts ...string) string {
	var b strings.Builder
	for _, t := range texts {
		b.WriteString(htmlTag.ReplaceAllString(macro.ReplaceAllString(t, " "), " "))
		b.WriteString("\n")
	}
	sample := []rune(b.String())
	info := whatlanggo.Detect(string(sample[:min(len(sample), languageSample)]))
	if !info.IsReliable() {
		return unknownLanguage
	}
	return info.Lang.String()
}
