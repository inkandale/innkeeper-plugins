package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// englishText is ordinary card prose, long enough to clear every size threshold.
const englishText = "She is a quiet librarian who has kept the old tower archive for most of her life. " +
	"When a stranger comes in from the rain, she will watch them from behind the shelves before she says anything at all. " +
	"She keeps a cat named Ink and a ledger of every book that was never returned."

// cleanCard trips no check.
func cleanCard() map[string]string {
	return map[string]string{
		"name":        "Mira",
		"description": "{{char}} is Mira. " + englishText,
		"personality": "Patient, dry, and fond of the people who are lost in her stacks.",
		"scenario":    "{{user}} has come into the archive to wait out a storm.",
		"first_mes":   "Mira looks up from her ledger. \"You are dripping on the floor, and that is a first edition.\"",
	}
}

func lintCard(t *testing.T, config map[string]any, card map[string]string) map[string]json.RawMessage {
	t.Helper()
	s, err := parseSettings(config)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(card)
	res, err := enrich(s, mustJSON(t, protocol.EnrichRunParams{Run: "R1",
		Revisions: []protocol.EnrichRevision{{CardID: 1, RevisionID: 10, Data: data}}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].Error != "" {
		t.Fatalf("results = %+v", res.Results)
	}
	return res.Results[0].Values
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func number(t *testing.T, values map[string]json.RawMessage, key string) float64 {
	t.Helper()
	var n float64
	if err := json.Unmarshal(values[key], &n); err != nil {
		t.Fatalf("%s = %s: %v", key, values[key], err)
	}
	return n
}

func problems(t *testing.T, values map[string]json.RawMessage) []string {
	t.Helper()
	var p []string
	if err := json.Unmarshal(values["problems"], &p); err != nil || p == nil {
		t.Fatalf("problems = %s: %v", values["problems"], err)
	}
	return p
}

func TestCleanCardScoresFullWithNoProblems(t *testing.T) {
	v := lintCard(t, nil, cleanCard())
	if got := number(t, v, "lint"); got != 100 {
		t.Errorf("lint = %v, want 100", got)
	}
	if p := problems(t, v); len(p) != 0 {
		t.Errorf("problems = %v, want none", p)
	}
	if number(t, v, "tokens") <= 0 {
		t.Errorf("tokens = %s, want an estimate", v["tokens"])
	}
}

func TestEachCheckListsItsCodeAndTakesItsPoints(t *testing.T) {
	cases := map[string]struct {
		edit   func(c map[string]string)
		points float64
	}{
		"no_name":          {func(c map[string]string) { c["name"] = " " }, 100},
		"no_description":   {func(c map[string]string) { c["description"] = ""; c["personality"] += " " + englishText }, 100},
		"no_first_message": {func(c map[string]string) { c["first_mes"] = "" }, 100},
		"oversized":        {func(c map[string]string) { c["system_prompt"] = strings.Repeat("The rain does not stop. ", 700) }, 20},
		"repeated_text":    {func(c map[string]string) { c["scenario"] += "\n\n" + englishText }, 15},
		"first_message_copies_description": {func(c map[string]string) {
			c["description"] = englishText + "\n\n{{char}} is Mira."
			c["first_mes"] = englishText + "\n\n\"Hello.\""
		}, 15},
		"placeholder":   {func(c map[string]string) { c["scenario"] = "<bot> has let {{user}} in from the storm." }, 15},
		"no_char_macro": {func(c map[string]string) { c["description"] = englishText; c["first_mes"] = "\"You are dripping on the floor.\"" }, 10},
		"markup":        {func(c map[string]string) { c["personality"] = "<div style=\"color:red\">Patient and dry.</div>" }, 10},
		"mojibake":      {func(c map[string]string) { c["personality"] = "Patient, dry, and fond of cafÃ© regulars." }, 10},
	}
	for code, tc := range cases {
		t.Run(code, func(t *testing.T) {
			card := cleanCard()
			tc.edit(card)
			v := lintCard(t, nil, card)
			if p := problems(t, v); !slices.Equal(p, []string{code}) {
				t.Fatalf("problems = %v, want [%s]", p, code)
			}
			if got := number(t, v, "lint"); got != 100-tc.points {
				t.Errorf("lint = %v, want %v", got, 100-tc.points)
			}
		})
	}
}

func TestShortCardIsTooShort(t *testing.T) {
	v := lintCard(t, nil, map[string]string{"name": "Mira", "description": "{{char}} is a librarian, and she is kind to all of you.",
		"first_mes": "Mira looks up from her ledger and smiles at you."})
	if p := problems(t, v); !slices.Contains(p, "too_short") {
		t.Errorf("problems = %v, want too_short", p)
	}
}

func TestHundredPointProblemZeroesTheScore(t *testing.T) {
	card := cleanCard()
	card["first_mes"] = ""
	card["personality"] = "<b>Patient</b>, dry, and fond of cafÃ© regulars."
	if got := number(t, lintCard(t, nil, card), "lint"); got != 0 {
		t.Errorf("lint = %v, want 0", got)
	}
}

func TestEnglishScoreSeparatesLanguages(t *testing.T) {
	texts := map[string]string{
		"Spanish": "Ella es una bibliotecaria tranquila que ha cuidado el archivo de la vieja torre durante casi toda su vida. " +
			"Cuando un extraño entra huyendo de la lluvia, lo observa desde detrás de los estantes antes de decir nada.",
		"Chinese": "她是一位安静的图书管理员，几乎一生都在看守古塔里的档案馆。当一个陌生人从雨中走进来时，她会先在书架后面观察对方，然后才开口说话。",
		"Russian": "Она тихая библиотекарша, которая почти всю жизнь хранит архив старой башни. " +
			"Когда незнакомец заходит с дождя, она сначала наблюдает за ним из-за полок и только потом говорит.",
	}
	if got := number(t, lintCard(t, nil, cleanCard()), "english"); got < 25 {
		t.Errorf("English card: english = %v, want at least 25", got)
	}
	for lang, text := range texts {
		card := cleanCard()
		card["description"], card["first_mes"] = "{{char}} "+text, text
		v := lintCard(t, nil, card)
		if got := number(t, v, "english"); got >= 15 {
			t.Errorf("%s card: english = %v, want under 15", lang, got)
		}
		if p := problems(t, v); !slices.Contains(p, "not_english") {
			t.Errorf("%s card: problems = %v, want not_english", lang, p)
		}
	}
}

func TestSettingsChangeThresholdsAndPoints(t *testing.T) {
	v := lintCard(t, map[string]any{"max_tokens": 10}, cleanCard())
	if p := problems(t, v); !slices.Equal(p, []string{"oversized"}) {
		t.Fatalf("problems = %v, want [oversized]", p)
	}
	v = lintCard(t, map[string]any{"max_tokens": 10, "points": map[string]any{"oversized": 0}}, cleanCard())
	if p := problems(t, v); !slices.Equal(p, []string{"oversized"}) {
		t.Errorf("problems = %v, want [oversized] listed at 0 points", p)
	}
	if got := number(t, v, "lint"); got != 100 {
		t.Errorf("lint = %v, want 100", got)
	}
}

func TestSettingsDefaultEveryProblemsPoints(t *testing.T) {
	s, err := parseSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if _, ok := s.Points[c.code]; !ok {
			t.Errorf("plugin.toml has no default points for %s", c.code)
		}
	}
	if s.MinTokens != 50 || s.MaxTokens != 4000 || s.RepeatWords != 20 || s.MinEnglish != 25 || len(s.Placeholders) == 0 {
		t.Errorf("defaults = %+v", s)
	}
}

func TestUndecodableRevisionGetsAnError(t *testing.T) {
	s, _ := parseSettings(nil)
	res, err := enrich(s, mustJSON(t, protocol.EnrichRunParams{Run: "R1", Revisions: []protocol.EnrichRevision{
		{CardID: 1, RevisionID: 10, Data: json.RawMessage(`[1]`)},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Error == "" || res.Results[0].Values != nil {
		t.Errorf("result = %+v, want an error and no values", res.Results[0])
	}
}
