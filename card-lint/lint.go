package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
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
	english  int
	settings settings
}

// checks are in the order problems lists them.
var checks = []struct {
	code  string
	fails func(f facts) bool
}{
	{"no_name", func(f facts) bool { return blank(f.card.Name) }},
	{"no_description", func(f facts) bool { return blank(f.card.Description) }},
	{"no_first_message", func(f facts) bool { return blank(f.card.FirstMessage) }},
	{"too_short", func(f facts) bool {
		return tokens(f.card.Description, f.card.Personality, f.card.Scenario) < f.settings.MinTokens
	}},
	{"not_english", func(f facts) bool { return f.english < f.settings.MinEnglish }},
	{"oversized", func(f facts) bool { return f.tokens > f.settings.MaxTokens }},
	{"repeated_text", func(f facts) bool { return repeated(f.card.alwaysOn(), f.settings.RepeatWords) }},
	{"first_message_copies_description", func(f facts) bool {
		first, _, _ := strings.Cut(strings.TrimSpace(f.card.Description), "\n")
		return wordCount(first) >= f.settings.RepeatWords && strings.Contains(normalize(f.card.FirstMessage), normalize(first))
	}},
	{"placeholder", func(f facts) bool { return slices.ContainsFunc(f.card.all(), f.hasPlaceholder) }},
	{"no_char_macro", func(f facts) bool {
		names := func(t string) bool {
			t = strings.ToLower(t)
			return strings.Contains(t, "{{char}}") || !blank(f.card.Name) && strings.Contains(t, strings.ToLower(strings.TrimSpace(f.card.Name)))
		}
		return !names(f.card.Description) && !names(f.card.FirstMessage)
	}},
	{"markup", func(f facts) bool { return slices.ContainsFunc(f.card.alwaysOn(), htmlTag.MatchString) }},
	{"mojibake", func(f facts) bool { return slices.ContainsFunc(f.card.all(), garbled.MatchString) }},
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
	english  int
	problems []string
}

func lint(s settings, c card) result {
	f := facts{card: c, tokens: tokens(c.alwaysOn()...), english: english(c.Description, c.FirstMessage), settings: s}
	r := result{score: 100, tokens: f.tokens, english: f.english, problems: []string{}}
	for _, ch := range checks {
		if ch.fails(f) {
			r.problems = append(r.problems, ch.code)
			r.score -= s.Points[ch.code]
		}
	}
	r.score = max(r.score, 0)
	return r
}

// maxTokens is the top of the tokens enrichment's range.
const maxTokens = 1_000_000

func (r result) values() (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range map[string]any{"lint": r.score, "tokens": min(r.tokens, maxTokens), "english": r.english, "problems": r.problems} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

// english is the percentage of the texts' words that are common English
// words, after macros and markup are removed. Chinese and Japanese, written
// without spaces, count each character as a word.
// ponytail: a stopword ratio says only "English or not"; naming the language needs a detector such as whatlanggo.
func english(texts ...string) int {
	words, hits := 0, 0
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		words++
		if englishWords[word.String()] {
			hits++
		}
		word.Reset()
	}
	for _, t := range texts {
		t = htmlTag.ReplaceAllString(macro.ReplaceAllString(t, " "), " ")
		for _, r := range strings.ToLower(t) {
			switch {
			case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana):
				flush()
				words++
			case unicode.IsLetter(r) || r == '\'':
				word.WriteRune(r)
			default:
				flush()
			}
		}
		flush()
	}
	if words == 0 {
		return 0
	}
	return hits * 100 / words
}

var englishWords = func() map[string]bool {
	m := map[string]bool{}
	for w := range strings.FieldsSeq(`the a an and or but if of to in on at by for with from as into about over
		after before under than then so not no is are was were be been being am has have had do does did will would
		can could should may might must shall i you he she it we they me him her us them my your his its our their
		this that these those who whom which what when where why how all any each some there here up down out off
		very just too also only more most again never always while until because through`) {
		m[w] = true
	}
	return m
}()
