package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// rulesVersion names the rules and weights in each value's meta; change it
// when either changes, so old and new scores don't look comparable.
const rulesVersion = "card-rules-1"

// The thresholds, in characters and estimated tokens, were checked against
// 500 random Chub cards; see the README.
const (
	emptyDescription = 150
	thinDescription  = 300
	noGreeting       = 40
	shortGreeting    = 100
	veryLongTokens   = 8000
)

// templateText is placeholder text an author left unfilled. Each pattern is
// one that flagged no well-rated card in the sample; looser ones, such as
// "todo" or "[]", matched ordinary Spanish and card formats.
var (
	templateText = regexp.MustCompile(`(?i)\binsert (your |the |a )?(name|character|description|greeting|text|scenario)( here)?\b|` +
		`lorem ipsum|\[\s*(your |char(acter)?'?s? )?name( here)?\s*\]|describe (your|the) character here|` +
		`write (your|the) (greeting|description) here|your character'?s name here`)
	templateCaps = regexp.MustCompile(`\bTODO\b|\bTBD\b|\bFILL (ME )?IN\b`)
)

type result struct {
	quality float64
	issues  []string
	tokens  float64
}

// check scores card data: 100, less a weight for each problem found.
func check(data json.RawMessage) result {
	var card map[string]any
	_ = json.Unmarshal(data, &card)
	field := func(k string) string {
		s, _ := card[k].(string)
		return strings.TrimSpace(s)
	}
	r := result{quality: 100, issues: []string{}}
	flag := func(issue string, weight float64) {
		r.issues = append(r.issues, issue)
		r.quality -= weight
	}
	switch n := len(field("description")); {
	case n < emptyDescription:
		flag("empty description", 40)
	case n < thinDescription:
		flag("thin description", 15)
	}
	switch n := len(field("first_mes")); {
	case n < noGreeting:
		flag("no greeting", 35)
	case n < shortGreeting:
		flag("short greeting", 10)
	}
	var text, all strings.Builder
	for _, k := range []string{"name", "description", "personality", "scenario", "first_mes", "mes_example"} {
		text.WriteString(field(k) + "\n")
	}
	if templateText.MatchString(text.String()) || templateCaps.MatchString(text.String()) {
		flag("template text", 25)
	}
	all.WriteString(text.String())
	all.WriteString(field("system_prompt"))
	all.WriteString(field("post_history_instructions"))
	// ponytail: four characters a token; use a real tokenizer if this flags the wrong cards.
	r.tokens = float64(len(all.String()) / 4)
	if r.tokens > veryLongTokens {
		flag("very long", 15)
	}
	r.quality = max(r.quality, 0)
	return r
}
