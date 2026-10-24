package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

func TestActionsValidate(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range actions {
		if err := a.Validate(); err != nil {
			t.Error(err)
		}
		if seen[a.ID] {
			t.Errorf("action %s repeats", a.ID)
		}
		seen[a.ID] = true
	}
}

func result(t *testing.T, p protocol.ActionRunParams, output string) protocol.ActionRunResult {
	t.Helper()
	in, err := parseRun(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.result(output)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func proposalOf(t *testing.T, res protocol.ActionRunResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(res.Proposal, &m); err != nil {
		t.Fatalf("proposal %s: %v", res.Proposal, err)
	}
	return m
}

func TestFieldActionProposalChangesOnlyItsField(t *testing.T) {
	res := result(t, sampleRun(t, "tighten"), `{"description": "A towering orc.", "name": "Grok"}`)
	m := proposalOf(t, res)
	if m["description"] != "A towering orc." || m["name"] != "Brakka" || m["extensions"] == nil {
		t.Fatalf("proposal %v", m)
	}
	if res.Report != nil {
		t.Fatal("field action made a report")
	}
}

func TestGenerateProposalIsTheNewCard(t *testing.T) {
	m := proposalOf(t, result(t, sampleRun(t, "generate"), `{"name": "Ash", "description": "A courier.", "tags": ["sci-fi"]}`))
	if m["name"] != "Ash" || len(m) != 3 {
		t.Fatalf("proposal %v", m)
	}
}

func TestProposalRefusesWrongTypes(t *testing.T) {
	in, err := parseRun(sampleRun(t, "revise"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.result(`{"description": ["not", "text"]}`); err == nil {
		t.Fatal("a list description was accepted")
	}
}

func TestEvaluateReport(t *testing.T) {
	out := `{"summary": "Solid **orc**.", "overall_score": 78, "card_type": "character",
		"dimensions": {"voice": {"score": 88, "comment": "Distinct."}, "consistency": {"score": 70, "comment": "Mostly."}},
		"contradictions": [{"fields": ["personality", "first_mes"], "issue": "Gruff, yet chatty.", "fix": "Make first_mes curt."}],
		"misplaced_content": [{"excerpt": "Always reply in 3 lines", "current_field": "description", "suggested_field": "post_history_instructions", "reason": "Per-turn rule."}],
		"suggestions": ["Add a system prompt."]}`
	res := result(t, sampleRun(t, "evaluate"), out)
	r := res.Report
	if r == nil || res.Proposal != nil {
		t.Fatalf("result %+v", res)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	// The score is the dimensions' mean, not the model's overall_score.
	if r.Summary != "Solid **orc**." || r.Score == nil || *r.Score != 79 {
		t.Fatalf("summary %q, score %v", r.Summary, r.Score)
	}
	// Dimensions keep the prompt's order, not the model's.
	if len(r.Dimensions) != 2 || r.Dimensions[0].Key != "consistency" || r.Dimensions[1].Label != "Voice" {
		t.Fatalf("dimensions %+v", r.Dimensions)
	}
	if len(r.Findings) != 2 || r.Findings[0].Severity != "high" || r.Findings[0].Text != "Gruff, yet chatty. Fix: Make first_mes curt." || r.Findings[1].Move == nil ||
		r.Findings[1].Move.To != "post_history_instructions" || r.Findings[1].Fields[0] != "description" {
		t.Fatalf("findings %+v", r.Findings)
	}
	if len(r.Suggestions) != 1 {
		t.Fatalf("suggestions %v", r.Suggestions)
	}
}

func TestRunInputsChecked(t *testing.T) {
	bad := map[string]func(*protocol.ActionRunParams){
		"unknown action":    func(p *protocol.ActionRunParams) { p.Action = "nope" },
		"field not offered": func(p *protocol.ActionRunParams) { p.Field = "extensions" },
		"no instruction":    func(p *protocol.ActionRunParams) { p.Instruction = " " },
		"no point of view": func(p *protocol.ActionRunParams) {
			p.Action, p.Field, p.Inputs = "change-pov", "description", json.RawMessage(`{}`)
		},
		"scenario is not a pov": func(p *protocol.ActionRunParams) {
			p.Action, p.Field, p.Inputs = "change-pov", "description", json.RawMessage(`{"point_of_view": "scenario"}`)
		},
		"examples on a new card": func(p *protocol.ActionRunParams) { p.Action, p.Field, p.Entity.Data = "examples", "mes_example", nil },
	}
	for name, change := range bad {
		p := sampleRun(t, "revise")
		change(&p)
		_, err := parseRun(p)
		var rpc *protocol.Error
		if !errors.As(err, &rpc) || rpc.Code != -32602 {
			t.Errorf("%s: err = %v, want -32602", name, err)
		}
	}
}

func TestNullMeansUnchanged(t *testing.T) {
	m := proposalOf(t, result(t, sampleRun(t, "revise"), `{"description": null}`))
	if m["description"] != "A very big, very tall orc who guards {{user}}." {
		t.Fatalf("proposal %v", m)
	}
	p := sampleRun(t, "revise")
	p.Field = "scenario"
	if _, added := proposalOf(t, result(t, p, `{"scenario": null}`))["scenario"]; added {
		t.Fatal("a null added scenario, which the card lacks")
	}
}

func TestTagsAsCommaSeparatedText(t *testing.T) {
	p := sampleRun(t, "revise")
	p.Field = "tags"
	m := proposalOf(t, result(t, p, `{"tags": "orc, guard ,, brother"}`))
	tags, _ := m["tags"].([]any)
	if len(tags) != 3 || tags[1] != "guard" || tags[2] != "brother" {
		t.Fatalf("tags = %v", m["tags"])
	}
}

func TestTheTenActions(t *testing.T) {
	var ids []string
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	want := []string{"generate", "revise", "tighten", "expand", "to-prose", "change-pov", "examples", "alternate-greetings", "lore-entries", "evaluate"}
	if !slices.Equal(ids, want) {
		t.Fatalf("actions %v, want %v", ids, want)
	}
	for _, id := range []string{"revise", "tighten", "expand", "to-prose", "change-pov"} {
		if a, _ := findAction(id); !a.WholeCard || len(a.Fields) == 0 {
			t.Errorf("%s: want a field action that also runs on the whole card", id)
		}
	}
}

func TestReviseOnTagsTakesCommaText(t *testing.T) {
	p := sampleRun(t, "revise")
	p.Field = "tags"
	p.Entity.Data = json.RawMessage(`{"name": "Brakka", "tags": ["orc", "guard"]}`)
	system, user, _ := render(t, p, settings{})
	if !strings.Contains(user, "\norc, guard\n") || !strings.Contains(system, `"tags"`) {
		t.Fatalf("tags field prompt:\n%s\n%s", system, user)
	}
}

func TestAlternateGreetingsAppendToExisting(t *testing.T) {
	p := sampleRun(t, "alternate-greetings")
	p.Entity.Data = json.RawMessage(`{"name": "Brakka", "first_mes": "Hi.", "alternate_greetings": ["Old."], "extensions": {"x": 1}}`)
	m := proposalOf(t, result(t, p, `{"alternate_greetings": ["*Rain.* Who goes there?", " ", "Old.", "*Dawn.*"]}`))
	got, _ := json.Marshal(m["alternate_greetings"])
	if string(got) != `["Old.","*Rain.* Who goes there?","*Dawn.*"]` || m["first_mes"] != "Hi." || m["extensions"] == nil {
		t.Fatalf("proposal %v", m)
	}
}

func TestAlternateGreetingsOnACardWithoutAny(t *testing.T) {
	m := proposalOf(t, result(t, sampleRun(t, "alternate-greetings"), `{"alternate_greetings": ["*Dawn.*"]}`))
	if got, _ := json.Marshal(m["alternate_greetings"]); string(got) != `["*Dawn.*"]` || m["name"] != "Brakka" {
		t.Fatalf("proposal %v", m)
	}
}

func TestLoreEntriesKeepExistingAndAppendNew(t *testing.T) {
	out := `{"entries": [
		{"comment": "The pass", "keys": ["BRAKKA", "Iron Pass"], "content": "Matches an existing entry's first key."},
		{"comment": "Orcs", "keys": ["the orc", "Orcs", "Greenskins"], "content": "The orcs of the pass."},
		{"comment": "Mostly taken", "keys": ["the orc", "orcs", "Tusks"], "content": "Two of three keys taken."},
		{"comment": "Empty", "keys": ["Snow"], "content": " "},
		{"keys": ["Billy", "billy"], "content": "Brakka's brother."}]}`
	res := result(t, sampleRun(t, "lore-entries"), out)
	m := proposalOf(t, res)
	if m["name"] != "Northern Reach" || m["description"] != "The cold north." {
		t.Fatalf("proposal %v", m)
	}
	// The Forge's review compares entries as JSON text, so the existing one keeps its bytes.
	var before struct{ Entries []json.RawMessage }
	_ = json.Unmarshal([]byte(sampleLorebook), &before)
	var existing bytes.Buffer
	_ = json.Compact(&existing, before.Entries[0])
	if !bytes.Contains(res.Proposal, existing.Bytes()) {
		t.Fatalf("existing entry %s changed in %s", existing.Bytes(), res.Proposal)
	}
	entries, _ := m["entries"].([]any)
	added, _ := json.Marshal(entries[1:])
	if string(added) != `[{"comment":"Orcs","content":"The orcs of the pass.","enabled":true,"extensions":{},`+
		`"insertion_order":100,"keys":["Orcs","Greenskins"],"position":"before_char","use_regex":false},`+
		`{"content":"Brakka's brother.","enabled":true,"extensions":{},"insertion_order":100,"keys":["Billy"],"position":"before_char","use_regex":false}]` {
		t.Fatalf("added entries %s", added)
	}
}

func TestLoreEntryWithoutKeyListStillReachesTheModel(t *testing.T) {
	p := sampleRun(t, "lore-entries")
	p.Entity.Data = json.RawMessage(`{"name": "Weather", "entries": [{"keys": "Snow", "content": "It always snows."}, "not an entry"]}`)
	r, err := parseRun(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, user, err := r.prompts(settings{}); err != nil || !strings.Contains(user, "It always snows.") {
		t.Fatalf("user prompt %q, err %v", user, err)
	}
	res, err := r.result(`{"entries": [{"keys": ["Snow"], "content": "Snow falls at night."}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(res.Proposal, []byte(`"not an entry"`)) || !bytes.Contains(res.Proposal, []byte(`"keys":["Snow"],"content":"Snow falls at night."`)) {
		t.Fatalf("proposal %s", res.Proposal)
	}
}

func TestAppendThatAddsNothingLeavesTheDraft(t *testing.T) {
	for action, out := range map[string]string{
		"alternate-greetings": `{"alternate_greetings": []}`,
		"lore-entries":        `{"entries": [{"keys": ["Brakka"], "content": "Taken."}]}`,
	} {
		p := sampleRun(t, action)
		res := result(t, p, out)
		var got, want any
		_ = json.Unmarshal(res.Proposal, &got)
		_ = json.Unmarshal(p.Entity.Data, &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: proposal %s, want the draft", action, res.Proposal)
		}
	}
}

func TestCountDefaultsAndLimits(t *testing.T) {
	cases := []struct {
		action, inputs string
		want           int
	}{
		{"alternate-greetings", `{}`, 3},
		{"alternate-greetings", `{"count": 50}`, 10},
		{"lore-entries", `{}`, 5},
		{"lore-entries", `{"count": 50}`, 20},
		{"lore-entries", `{"count": -2}`, 1},
	}
	for _, c := range cases {
		p := sampleRun(t, c.action)
		p.Inputs = json.RawMessage(c.inputs)
		r, err := parseRun(p)
		if err != nil {
			t.Fatal(err)
		}
		if r.count != c.want {
			t.Errorf("%s %s: count %d, want %d", c.action, c.inputs, r.count, c.want)
		}
	}
}
