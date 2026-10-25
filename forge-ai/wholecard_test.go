package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func userPrompt(req map[string]any) string {
	return req["messages"].([]any)[1].(map[string]any)["content"].(string)
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func wholeCardRun(t *testing.T, id string) *run {
	t.Helper()
	p := sampleRun(t, id)
	p.Field = ""
	r, err := parseRun(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWholeCardRunPlansThenEditsEachPlannedField(t *testing.T) {
	llm, cfg := withLLM(t,
		streamed(`{"description": "Cut the repeated height.", "personality": "skip"}`),
		streamed(`{"description": "A towering orc who guards {{user}}."}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "tighten")
	p.Field = ""
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	m := proposalOf(t, res)
	if m["description"] != "A towering orc who guards {{user}}." || m["personality"] != "Gruff." || m["extensions"] == nil {
		t.Fatalf("proposal %v", m)
	}
	if len(llm.requests) != 2 || !strings.Contains(userPrompt(llm.requests[1]), "Plan for this field: Cut the repeated height.") {
		t.Fatalf("requests %v", llm.requests)
	}
	var messages []string
	for _, pr := range h.progress {
		if pr.Done >= pr.Total {
			t.Fatalf("progress %+v: done must stay below total", pr)
		}
		messages = append(messages, pr.Message)
	}
	if len(messages) < 2 || !slices.Equal(messages[:2], []string{"Planning Tighten", "Tighten: Description (1 of 1)"}) {
		t.Fatalf("progress %q", messages)
	}
}

func TestLaterSectionsSeeEarlierEdits(t *testing.T) {
	llm, cfg := withLLM(t,
		streamed(`{"description": "Shorten.", "personality": "Match the new description."}`),
		streamed(`{"description": "A big orc."}`),
		streamed(`{"personality": "Calm."}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "tighten")
	p.Field = ""
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if m := proposalOf(t, res); m["description"] != "A big orc." || m["personality"] != "Calm." {
		t.Fatalf("proposal %v", m)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("%d requests, want 3", len(llm.requests))
	}
	if !strings.Contains(userPrompt(llm.requests[1]), "Plan for this field: Shorten.") ||
		!strings.Contains(userPrompt(llm.requests[2]), "Plan for this field: Match the new description.") {
		t.Fatal("a section didn't get its own note")
	}
	if !strings.Contains(userPrompt(llm.requests[2]), "A big orc.") {
		t.Fatalf("personality's section didn't see the new description:\n%s", userPrompt(llm.requests[2]))
	}
}

func TestPlanThatSkipsEverythingChangesNothing(t *testing.T) {
	llm, cfg := withLLM(t, streamed(`{"description": "skip"}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "expand")
	p.Field = ""
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	_ = json.Unmarshal(res.Proposal, &got)
	_ = json.Unmarshal([]byte(sampleCard), &want)
	if len(llm.requests) != 1 || !jsonEqual(got, want) {
		t.Fatalf("%d requests, proposal %v", len(llm.requests), got)
	}
}

func TestPlanIgnoresUnknownKeysAndSkipsMissingFields(t *testing.T) {
	llm, cfg := withLLM(t,
		streamed(`{"extensions": "Wipe them.", "character_book": "Add one.", "scenario": null, "personality": "Say less."}`),
		streamed(`{"personality": "Terse."}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "tighten")
	p.Field = ""
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	m := proposalOf(t, res)
	if len(llm.requests) != 2 || m["personality"] != "Terse." || m["description"] != "A very big, very tall orc who guards {{user}}." ||
		!jsonEqual(m["extensions"], map[string]any{"x": 1}) {
		t.Fatalf("%d requests, proposal %v", len(llm.requests), m)
	}
	if _, added := m["scenario"]; added {
		t.Fatal("a null note added scenario")
	}
}

func TestMalformedPlanFailsTheRun(t *testing.T) {
	for name, plan := range map[string]string{
		"no JSON":       "I'd tighten the description.",
		"note not text": `{"description": 3}`,
		"note a list":   `{"description": ["Cut it."]}`,
	} {
		llm, cfg := withLLM(t, streamed(plan), streamed(`{"description": "x"}`))
		h := startPlugin(t, cfg)
		p := sampleRun(t, "tighten")
		p.Field = ""
		_, err := h.run(t, p)
		if err == nil || !strings.Contains(err.Error(), "plan") {
			t.Errorf("%s: err = %v, want a plan error", name, err)
		}
		if len(llm.requests) != 1 {
			t.Errorf("%s: %d requests, want only the plan", name, len(llm.requests))
		}
	}
}

func TestCancelBetweenSectionsStopsTheRun(t *testing.T) {
	llm, cfg := withLLM(t,
		streamed(`{"description": "Shorten.", "personality": "Shorten."}`),
		streamed(`{"description": "A big orc."}`),
		streamed(`{"personality": "Calm."}`))
	h := startPlugin(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := 0
	llm.onRequest = func() {
		if requests++; requests == 2 {
			cancel() // The owner pressed Stop during the first section.
		}
	}
	_, err := h.srv.wholeCard(ctx, h.in, wholeCardRun(t, "tighten"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("%d requests, want the plan and one section", len(llm.requests))
	}
}

func TestPlanCarriesTheDirectionAndNamesTheFields(t *testing.T) {
	llm, cfg := withLLM(t, streamed(`{}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "revise")
	p.Field = ""
	if _, err := h.run(t, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(userPrompt(llm.requests[0]), "The owner's direction: make him the brother of @cards/Billy") {
		t.Fatalf("plan user prompt:\n%s", userPrompt(llm.requests[0]))
	}
	if _, ok := llm.requests[0]["tools"]; ok {
		t.Error("the plan offered tools")
	}
}

func TestPlanContractListsOnlyTheActionsFields(t *testing.T) {
	system, _, err := wholeCardRun(t, "tighten").planPrompts(settings{})
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(system, "whose keys are the fields to plan, ")
	if !ok {
		t.Fatalf("no JSON contract line:\n%s", system)
	}
	keys, _, _ := strings.Cut(after, ", each with")
	if keys == "" || strings.Contains(keys, `"name"`) || !strings.Contains(keys, `"description"`) {
		t.Fatalf("contract keys = %s", keys)
	}
}

// A plan holding each field's new text runs past its token limit and won't parse.
func TestPlanContractAsksForNotesNotNewText(t *testing.T) {
	system, _, err := wholeCardRun(t, "revise").planPrompts(settings{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "not the new text") {
		t.Fatalf("plan system prompt doesn't rule out the new text:\n%s", system)
	}
}

func TestWholeCardResultKeepsEveryRawReply(t *testing.T) {
	_, cfg := withLLM(t,
		streamed(`{"description": "Cut the repeated height."}`),
		streamed(`{"description": "A towering orc."}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "tighten")
	p.Field = ""
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Plan:", "Cut the repeated height.", label("description") + ":", "A towering orc."} {
		if !strings.Contains(res.Raw, want) {
			t.Errorf("Raw lacks %q:\n%s", want, res.Raw)
		}
	}
}

func TestPlanWithoutInstructionUsesTheActionsInstructions(t *testing.T) {
	r := wholeCardRun(t, "tighten")
	system, user, err := r.planPrompts(settings{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "Tighten it: cut filler") || strings.Contains(user, "direction") {
		t.Fatalf("system:\n%s\nuser:\n%s", system, user)
	}
	if system, _, _ = r.planPrompts(settings{Prompts: map[string]string{"whole-card": "Plan briefly."}}); !strings.Contains(system, "Plan briefly.") ||
		strings.Contains(system, "Plan an edit of the whole card") {
		t.Fatalf("whole-card override not used:\n%s", system)
	}
}

func TestChangePOVNamesOnlyTheFieldItEdits(t *testing.T) {
	for _, pov := range []string{"first", "third"} {
		p := sampleRun(t, "change-pov")
		p.Inputs = json.RawMessage(`{"point_of_view": "` + pov + `"}`)
		system, _, _ := render(t, p, settings{})
		for _, other := range []string{"first_mes", "mes_example", "system_prompt", "post_history_instructions"} {
			if strings.Contains(system, other) {
				t.Errorf("%s person on description names %s:\n%s", pov, other, system)
			}
		}
		p.Field = "system_prompt"
		if system, _, _ = render(t, p, settings{}); !strings.Contains(system, "enforce "+pov+" person") {
			t.Errorf("%s person on system_prompt doesn't enforce it:\n%s", pov, system)
		}
	}
	p := sampleRun(t, "change-pov")
	p.Field, p.Inputs = "first_mes", json.RawMessage(`{"point_of_view": "third"}`)
	if system, _, _ := render(t, p, settings{}); !strings.Contains(system, "omniscient narrator") {
		t.Errorf("third person on first_mes lacks the narrator rule:\n%s", system)
	}
}
