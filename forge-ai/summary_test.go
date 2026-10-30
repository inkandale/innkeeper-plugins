package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummaryTextKeepsWhatTheCardIsAbout(t *testing.T) {
	got, err := summaryText(json.RawMessage(`{"name": "Brakka", "description": " A dwarf smith. ", "mes_example": "<START>",
		"extensions": {"depth_prompt": {"prompt": "Stay gruff."}}, "first_mes": "Hm."}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "Name: Brakka\n\n## description\nA dwarf smith.\n\n## depth_prompt\nStay gruff.\n\n## first_mes\nHm."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	long, _ := json.Marshal(map[string]string{"name": "X", "description": strings.Repeat("é", summaryChars)})
	if got, _ := summaryText(long); len(got) > summaryChars {
		t.Fatalf("%d chars, want at most %d", len(got), summaryChars)
	}
}

func TestSummaryValuesReadsBoldedLines(t *testing.T) {
	got, err := summaryValues("Sure.\n**Premise:** A smith keeps a grudge.\n**Themes:** Grudges, , forge")
	if err != nil || string(got[premiseKey]) != `"A smith keeps a grudge."` || string(got[themesKey]) != `["grudges","forge"]` {
		t.Fatalf("got %s, %s, %v", got[premiseKey], got[themesKey], err)
	}
}
