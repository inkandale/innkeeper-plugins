package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

func TestEvaluateReportTakesObjectSuggestions(t *testing.T) {
	out := `{"summary": "Fine.", "dimensions": {"voice": {"score": 80}},
		"suggestions": ["Add a system prompt.", {"suggestion": "Give her a rival.", "fields": ["description"]}, {"issue": "No greeting.", "fix": "Add one."}, {"priority": 1}]}`
	r := result(t, sampleRun(t, "evaluate"), out).Report
	want := []string{"Add a system prompt.", "Give her a rival.", "No greeting. Add one."}
	if r == nil || !slices.Equal(r.Suggestions, want) {
		t.Fatalf("report %+v", r)
	}
}

func TestEvaluateLorebookReport(t *testing.T) {
	out := `{"summary": "Thin.", "overall_score": 90,
		"dimensions": {"triggers": {"score": 40, "comment": "Too broad."}, "consistency": {"score": 80, "comment": "Fine."}},
		"contradictions": [{"entries": ["Brakka", "The pass"], "issue": "Two different gatekeepers.", "fix": "Pick one."}],
		"problems": [{"entries": ["Brakka"], "issue": "Key \"the\" fires on every message."}],
		"suggestions": ["Add an entry for the clans."]}`
	res := result(t, sampleRun(t, "evaluate-lorebook"), out)
	r := res.Report
	if r == nil || res.Proposal != nil {
		t.Fatalf("result %+v", res)
	}
	if r.Score == nil || *r.Score != 60 || len(r.Dimensions) != 2 || r.Dimensions[0].Key != "triggers" || r.Dimensions[1].Label != "Consistency" {
		t.Fatalf("score %v, dimensions %+v", r.Score, r.Dimensions)
	}
	want := []protocol.Finding{
		{Severity: "high", Text: "Brakka, The pass: Two different gatekeepers. Fix: Pick one.", Fields: []string{"entries"}},
		{Severity: "medium", Text: `Brakka: Key "the" fires on every message.`, Fields: []string{"entries"}},
	}
	if !reflect.DeepEqual(r.Findings, want) {
		t.Fatalf("findings %+v", r.Findings)
	}
}

const threeEntries = `{"name": "Reach", "description": "Cold.", "extensions": {"x": 1}, "entries": [
	{"id": 1, "keys": ["pass"], "content": "Snowbound.", "insertion_order": 7, "extensions": {"depth": 4}},
	{"id": 2, "keys": ["gate"], "content": "Iron."},
	{"id": 3, "keys": ["clans"], "content": "Three clans.", "constant": true}]}`

// reviseLorebook runs revise-lorebook on threeEntries with a fake model
// that gives replies in turn, the plan first.
func reviseLorebook(t *testing.T, replies ...string) (map[string]json.RawMessage, *fakeLLM) {
	t.Helper()
	var scripted []fakeReply
	for _, r := range replies {
		scripted = append(scripted, streamed(r))
	}
	llm, cfg := withLLM(t, scripted...)
	h := startPlugin(t, cfg)
	p := sampleRun(t, "revise-lorebook")
	p.Entity.Data = json.RawMessage(threeEntries)
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(res.Proposal, &out); err != nil {
		t.Fatal(err)
	}
	return out, llm
}

func TestReviseLorebookPlansThenWritesEachPart(t *testing.T) {
	out, llm := reviseLorebook(t,
		`{"description": "Make it bitter.", "entries": [{"entry": 1, "note": "Add winter."},
			{"entry": 3, "note": "Not constant; add clan."}, {"note": "Add the chief."}], "removed": [2]}`,
		"Bitter cold.",
		"Keys: pass\n\nSnowbound in winter.",
		"Title:\nKeys: clans, clan\nConstant: no\n\nThree clans.",
		"Title: Chief\nKeys:  chief \n\n \"Kneel,\" says the chief. He leads the clans. ")
	var entries []map[string]any
	if err := json.Unmarshal(out["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	if string(out["description"]) != `"Bitter cold."` || string(out["extensions"]) != `{"x":1}` || len(entries) != 3 {
		t.Fatalf("proposal %s", out)
	}
	// An edit changes only the properties it gives; the rest keep their values.
	want := []map[string]any{
		{"id": 1.0, "keys": []any{"pass"}, "content": "Snowbound in winter.", "insertion_order": 7.0, "extensions": map[string]any{"depth": 4.0}},
		{"id": 3.0, "keys": []any{"clans", "clan"}, "content": "Three clans.", "constant": false},
		{"comment": "Chief", "keys": []any{"chief"}, "content": `"Kneel," says the chief. He leads the clans.`, "enabled": true,
			"insertion_order": 100.0, "position": "before_char", "use_regex": false, "extensions": map[string]any{}},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries %v", entries)
	}
	// Entry 3's step still sees it as entry 3: deletions wait for the last step.
	if len(llm.requests) != 5 || !strings.Contains(userPrompt(llm.requests[3]), "Write: Entry 3\nPlan for it: Not constant; add clan.") ||
		!strings.Contains(userPrompt(llm.requests[3]), `"content": "Iron."`) {
		t.Fatalf("requests %v", llm.requests)
	}
}

func TestReviseLorebookKeepsTheDraftWhenNothingChanges(t *testing.T) {
	out, llm := reviseLorebook(t,
		`{"name": "Reach", "entries": [{"entry": 2, "note": "Same."}, {"entry": 9, "note": "No such entry."},
			{"note": "Empty."}], "removed": [0, 12]}`,
		"Keys: gate\n\nIron.",
		"Keys: empty\n\n ")
	var got, want any
	b, _ := json.Marshal(out)
	_ = json.Unmarshal(b, &got)
	_ = json.Unmarshal([]byte(threeEntries), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("proposal %s", out)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("%d requests, want the plan and two steps; an unknown entry gets none", len(llm.requests))
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
	for _, reply := range []string{`{"tags": "orc, guard ,, brother"}`, "orc, guard ,, brother"} {
		m := proposalOf(t, result(t, p, reply))
		tags, _ := m["tags"].([]any)
		if len(tags) != 3 || tags[1] != "guard" || tags[2] != "brother" {
			t.Fatalf("%s: tags = %v", reply, m["tags"])
		}
	}
}

func TestFieldActionTakesPlainTextWithDialogueQuotes(t *testing.T) {
	p := sampleRun(t, "revise")
	p.Field = "first_mes"
	greeting := "She jogs past {{user}}. \"Wait\", she calls. \"Hey!\"\n\n*She stops.*"
	for _, reply := range []string{greeting, "<think>plan</think>\n```\n" + greeting + "\n```"} {
		if got := proposalOf(t, result(t, p, reply))["first_mes"]; got != greeting {
			t.Fatalf("%q: first_mes = %q", reply, got)
		}
	}
}

func TestTheTwelveActions(t *testing.T) {
	var ids []string
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	want := []string{"generate", "revise", "tighten", "expand", "to-prose", "change-pov", "examples", "alternate-greetings", "revise-lorebook", "lore-entries", "evaluate", "evaluate-lorebook"}
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

// appendRun runs action on data with count set to one call per reply.
func appendRun(t *testing.T, action, data string, replies ...string) (protocol.ActionRunResult, *fakeLLM) {
	t.Helper()
	var scripted []fakeReply
	for _, r := range replies {
		scripted = append(scripted, streamed(r))
	}
	llm, cfg := withLLM(t, scripted...)
	h := startPlugin(t, cfg)
	p := sampleRun(t, action)
	if data != "" {
		p.Entity.Data = json.RawMessage(data)
	}
	p.Inputs = json.RawMessage(fmt.Sprintf(`{"count": %d}`, len(replies)))
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	return res, llm
}

func TestAlternateGreetingsAppendToExisting(t *testing.T) {
	res, llm := appendRun(t, "alternate-greetings",
		`{"name": "Brakka", "first_mes": "Hi.", "alternate_greetings": ["Old."], "extensions": {"x": 1}}`,
		`*Rain.* "Who goes there?"`, " ", "Old.", "```\n*Dawn.*\n```")
	m := proposalOf(t, res)
	got, _ := json.Marshal(m["alternate_greetings"])
	if string(got) != `["Old.","*Rain.* \"Who goes there?\"","*Dawn.*"]` || m["first_mes"] != "Hi." || m["extensions"] == nil {
		t.Fatalf("proposal %v", m)
	}
	if !strings.Contains(userPrompt(llm.requests[3]), "Who goes there?") {
		t.Fatal("a later greeting's call doesn't see the earlier ones")
	}
}

func TestAlternateGreetingsOnACardWithoutAny(t *testing.T) {
	res, _ := appendRun(t, "alternate-greetings", "", "*Dawn.*")
	m := proposalOf(t, res)
	if got, _ := json.Marshal(m["alternate_greetings"]); string(got) != `["*Dawn.*"]` || m["name"] != "Brakka" {
		t.Fatalf("proposal %v", m)
	}
}

func TestLoreEntriesKeepExistingAndAppendNew(t *testing.T) {
	res, _ := appendRun(t, "lore-entries", "",
		"Title: The pass\nKeys: BRAKKA, Iron Pass\n\nMatches an existing entry's first key.",
		"Title: Orcs\nKeys: the orc, Orcs, Greenskins\n\nThe orcs of the pass.",
		"Title: Mostly taken\nKeys: the orc, orcs, Tusks\n\nTwo of three keys taken.",
		"Title: Empty\nKeys: Snow\n\n ",
		"Keys: Billy, billy\n\nBrakka's brother.")
	m := proposalOf(t, res)
	if m["name"] != "Northern Reach" || m["description"] != "The cold north." {
		t.Fatalf("proposal %v", m)
	}
	// The Forge's review compares entries as JSON text, so the existing one
	// keeps its bytes, as the protocol's encoder sends them.
	var before struct{ Entries []json.RawMessage }
	_ = json.Unmarshal([]byte(sampleLorebook), &before)
	var compact, existing bytes.Buffer
	_ = json.Compact(&compact, before.Entries[0])
	json.HTMLEscape(&existing, compact.Bytes())
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
	res, llm := appendRun(t, "lore-entries", `{"name": "Weather", "entries": [{"keys": "Snow", "content": "It always snows."}, "not an entry"]}`,
		"Keys: Snow\n\nSnow falls at night.")
	if !strings.Contains(userPrompt(llm.requests[0]), "It always snows.") {
		t.Fatalf("user prompt %q", userPrompt(llm.requests[0]))
	}
	if !bytes.Contains(res.Proposal, []byte(`"not an entry"`)) || !bytes.Contains(res.Proposal, []byte(`"keys":["Snow"],"content":"Snow falls at night."`)) {
		t.Fatalf("proposal %s", res.Proposal)
	}
}

func TestAppendThatAddsNothingLeavesTheDraft(t *testing.T) {
	for action, reply := range map[string]string{
		"alternate-greetings": "  ",
		"lore-entries":        "Keys: Brakka\n\nTaken.",
	} {
		res, _ := appendRun(t, action, "", reply)
		var got, want any
		_ = json.Unmarshal(res.Proposal, &got)
		_ = json.Unmarshal(sampleRun(t, action).Entity.Data, &want)
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

func TestFieldActionKeepsOnlyTheTextInsideItsTags(t *testing.T) {
	p := sampleRun(t, "revise")
	p.Field = "system_prompt"
	reply := "Here's the new system_prompt field:\n<text>\nYou narrate {{char}}.\n</text>\nHope that helps!"
	if got := proposalOf(t, result(t, p, reply))["system_prompt"]; got != "You narrate {{char}}." {
		t.Fatalf("system_prompt = %q", got)
	}
}

func TestCardPromptsShowTheAlternateGreetings(t *testing.T) {
	card := `{"name": "Brakka", "first_mes": "Hi.", "alternate_greetings": ["*The gate creaks.*"]}`
	for _, id := range []string{"evaluate", "revise", "alternate-greetings"} {
		p := sampleRun(t, id)
		p.Entity.Data = json.RawMessage(card)
		if _, user, _ := render(t, p, settings{}); !strings.Contains(user, "The gate creaks.") {
			t.Errorf("%s: the model doesn't see the alternate greetings:\n%s", id, user)
		}
	}
	p := sampleRun(t, "revise")
	p.Field, p.Entity.Data = "", json.RawMessage(card)
	r, err := parseRun(p)
	if err != nil {
		t.Fatal(err)
	}
	system, user, err := r.planPrompts(settings{})
	if err != nil || !strings.Contains(user, "The gate creaks.") || !strings.Contains(system, `"alternate_greetings": other opening messages`) {
		t.Fatalf("plan prompts (%v):\n%s\n%s", err, system, user)
	}
}
