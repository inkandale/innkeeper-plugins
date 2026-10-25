package main

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/inkandale/innkeeper/protocol"
)

const sampleCard = `{"name": "Brakka", "description": "A very big, very tall orc who guards {{user}}.", "personality": "Gruff.",
	"first_mes": "*Brakka grunts.*", "mes_example": "<START>\n{{char}}: Hm.", "tags": ["orc"], "extensions": {"x": 1}}`

// sampleLorebook has one entry, with an id the proposal must keep.
const sampleLorebook = `{"name": "Northern Reach", "description": "The cold north.", "entries": [{"id": 1, "keys": ["Brakka", "the orc"],
	"content": "Brakka guards the pass & the gate.", "enabled": true, "insertion_order": 50, "use_regex": false, "extensions": {"depth": 4}}]}`

// sampleRun is a valid run of the action id on sampleCard, or for a lorebook
// action on sampleLorebook with sampleCard attached.
func sampleRun(t *testing.T, id string) protocol.ActionRunParams {
	t.Helper()
	a, ok := findAction(id)
	if !ok {
		t.Fatalf("no action %s", id)
	}
	p := protocol.ActionRunParams{Run: "1.1", Action: id, Entity: protocol.ActionEntity{Kind: "card", Data: json.RawMessage(sampleCard)},
		Inputs: json.RawMessage(`{}`)}
	switch a.Applies {
	case protocol.ApplyNew:
		p.Entity.Data = nil
	case protocol.ApplyLorebook:
		p.Entity = protocol.ActionEntity{Kind: "lorebook", Data: json.RawMessage(sampleLorebook)}
		p.Context.Cards = []protocol.ContextItem{{ID: 7, Name: "Brakka", Data: json.RawMessage(sampleCard)}}
	}
	if slices.Contains(a.Fields, "description") {
		p.Field = "description"
	} else if len(a.Fields) > 0 {
		p.Field = a.Fields[0]
	}
	if a.Instruction {
		p.Instruction = "make him the brother of @cards/Billy"
	}
	if id == "change-pov" {
		p.Inputs = json.RawMessage(`{"point_of_view": "first"}`)
	}
	return p
}

func render(t *testing.T, p protocol.ActionRunParams, s settings) (system, user string, keys []string) {
	t.Helper()
	in, err := parseRun(p)
	if err != nil {
		t.Fatalf("%s: %v", p.Action, err)
	}
	system, user, err = in.prompts(s)
	if err != nil {
		t.Fatalf("%s: %v", p.Action, err)
	}
	return system, user, in.writes()
}

func TestEveryActionRenders(t *testing.T) {
	for _, a := range actions {
		system, user, keys := render(t, sampleRun(t, a.ID), settings{})
		if strings.TrimSpace(system) == "" || strings.TrimSpace(user) == "" {
			t.Errorf("%s: empty prompt", a.ID)
		}
		if strings.Contains(system+user, "[[") || strings.Contains(system+user, "<no value>") {
			t.Errorf("%s: unrendered template text:\n%s\n%s", a.ID, system, user)
		}
		if a.Applies == protocol.ApplyNew && !strings.Contains(system, "{{char}}") {
			t.Errorf("%s: system prompt lost the roleplay macros", a.ID)
		}
		if a.Applies == protocol.ApplyCard && !strings.Contains(user, "guards {{user}}") {
			t.Errorf("%s: user prompt lost the card's macros", a.ID)
		}
		if a.Applies == protocol.ApplyCard && !strings.Contains(user, "<START>") {
			t.Errorf("%s: user prompt escaped the card's <START>", a.ID)
		}
		if a.Applies == protocol.ApplyLorebook && !strings.Contains(user, "Brakka guards the pass & the gate.") {
			t.Errorf("%s: user prompt lacks the lorebook or escaped it", a.ID)
		}
		if len(keys) == 0 {
			t.Errorf("%s: writes no keys", a.ID)
		}
		for _, k := range keys {
			if !strings.Contains(system, `"`+k+`"`) {
				t.Errorf("%s: system prompt never names key %q", a.ID, k)
			}
		}
		if a.Applies == protocol.ApplyCard && !strings.Contains(user, "very tall orc") {
			t.Errorf("%s: user prompt lacks the card", a.ID)
		}
	}
}

func TestPromptsCarryRunInputs(t *testing.T) {
	_, user, _ := render(t, sampleRun(t, "revise"), settings{})
	if !strings.Contains(user, "@cards/Billy") {
		t.Errorf("revise: instruction missing from %q", user)
	}
	_, user, keys := render(t, sampleRun(t, "tighten"), settings{})
	if !strings.Contains(user, `"description"`) || !slices.Equal(keys, []string{"description"}) {
		t.Errorf("tighten: field missing; keys %v, user %q", keys, user)
	}
	p := sampleRun(t, "examples")
	p.Inputs = json.RawMessage(`{"count": 5}`)
	if _, user, _ := render(t, p, settings{}); !strings.Contains(user, "Write 5 new example") {
		t.Errorf("examples: count missing from %q", user)
	}
	p = sampleRun(t, "alternate-greetings")
	p.Entity.Data = json.RawMessage(`{"name": "Brakka", "alternate_greetings": ["*The gate creaks.*"]}`)
	p.Inputs = json.RawMessage(`{"count": 4}`)
	if system, user, _ := render(t, p, settings{}); !strings.Contains(user, "Write 4 new alternate greetings") || !strings.Contains(user, "The gate creaks.") {
		t.Errorf("alternate-greetings: count or existing greetings missing:\n%s\n%s", system, user)
	}
	p = sampleRun(t, "lore-entries")
	p.Instruction = "the guards of the pass"
	system, user, _ := render(t, p, settings{})
	for _, want := range []string{"Write 5 new entries", "the guards of the pass", "Northern Reach", "Brakka guards the pass & the gate.", "Card: Brakka"} {
		if !strings.Contains(system+user, want) {
			t.Errorf("lore-entries: prompt lacks %q:\n%s\n%s", want, system, user)
		}
	}
	if strings.Contains(user, "insertion_order") {
		t.Errorf("lore-entries: the model sees entry settings:\n%s", user)
	}
	p = sampleRun(t, "evaluate-lorebook")
	p.Entity.Data = json.RawMessage(`{"entries": [{"keys": ["pass"], "secondary_keys": ["gate"], "constant": true, "content": "Snowbound.", "insertion_order": 9}]}`)
	_, user, _ = render(t, p, settings{})
	if !strings.Contains(user, `"secondary_keys": [`) || !strings.Contains(user, `"constant": true`) || strings.Contains(user, "insertion_order") {
		t.Errorf("evaluate-lorebook: the model doesn't see how entries trigger:\n%s", user)
	}
	for pov, want := range map[string]string{"first": "first person", "third": "third person", "scenario": "scenario card"} {
		p := sampleRun(t, "generate")
		p.Inputs = json.RawMessage(`{"point_of_view": "` + pov + `"}`)
		if system, user, _ := render(t, p, settings{}); !strings.Contains(system+user, want) {
			t.Errorf("generate %s: system prompt lacks %q", pov, want)
		}
	}
}

func TestHouseStyleAndOverrideInSystemPrompt(t *testing.T) {
	s := settings{Prompts: map[string]string{"house_style": "Use British spelling.", "evaluate": "Be harsh about clichés."}}
	for _, a := range actions {
		system, user, keys := render(t, sampleRun(t, a.ID), s)
		if !strings.Contains(system, "Use British spelling.") {
			t.Errorf("%s: no house style", a.ID)
		}
		if got := strings.Contains(system, "Be harsh about clichés."); got != (a.ID == "evaluate") {
			t.Errorf("%s: override in system prompt = %v", a.ID, got)
		}
		if strings.Contains(user, "British") {
			t.Errorf("%s: house style leaked into the user prompt", a.ID)
		}
		for _, k := range keys {
			if !strings.Contains(system, `"`+k+`"`) {
				t.Errorf("%s: an override lost the JSON contract's key %q", a.ID, k)
			}
		}
	}
	system, _, _ := render(t, sampleRun(t, "evaluate"), s)
	if strings.Contains(system, "Determine Card Type") || !strings.Contains(system, `"overall_score"`) {
		t.Errorf("evaluate override kept the default or lost the contract:\n%s", system)
	}
}

func TestEmptyOverrideMeansNoInstructions(t *testing.T) {
	for _, k := range promptKeys {
		def, err := execute(k+".instructions", promptData{})
		if err != nil {
			t.Fatal(err)
		}
		s := settings{Prompts: map[string]string{k: ""}}
		var system string
		var keys []string
		if pov, ok := strings.CutPrefix(k, "generate-"); ok {
			p := sampleRun(t, "generate")
			p.Inputs = json.RawMessage(`{"point_of_view": "` + pov + `"}`)
			system, _, keys = render(t, p, s)
		} else if k == "whole-card" {
			r := wholeCardRun(t, "tighten")
			if system, _, err = r.planPrompts(s); err != nil {
				t.Fatal(err)
			}
			keys = r.Fields
		} else {
			system, _, keys = render(t, sampleRun(t, k), s)
		}
		if strings.Contains(system, def) {
			t.Errorf("%s: an empty override kept the default instructions", k)
		}
		for _, key := range keys {
			if !strings.Contains(system, `"`+key+`"`) {
				t.Errorf("%s: an empty override lost the JSON contract's key %q", k, key)
			}
		}
	}
}

func TestDefaultInstructionsTakeNoData(t *testing.T) {
	full := promptData{Card: "{}", Field: "description", FieldText: "x", Instruction: "y", PointOfView: "first", Count: 7, Image: true}
	for _, k := range promptKeys {
		empty, err := execute(k+".instructions", promptData{})
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		filled, err := execute(k+".instructions", full)
		if err != nil || filled != empty || strings.TrimSpace(empty) == "" {
			t.Errorf("%s: instructions depend on the run or are empty:\n%s", k, empty)
		}
	}
}

func TestEveryRunHasInstructions(t *testing.T) {
	for _, a := range actions {
		in, err := parseRun(sampleRun(t, a.ID))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(promptKeys, in.instructionsKey()) {
			t.Errorf("%s: instructions key %q isn't in promptKeys", a.ID, in.instructionsKey())
		}
	}
}

func TestSettingsDefaultsAreTheInstructions(t *testing.T) {
	res, err := settingsDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Defaults) != len(promptKeys) {
		t.Fatalf("%d defaults, want %d", len(res.Defaults), len(promptKeys))
	}
	for _, k := range promptKeys {
		want, err := execute(k+".instructions", promptData{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Defaults["/prompts/"+k] != want {
			t.Errorf("%s default %q, want %q", k, res.Defaults["/prompts/"+k], want)
		}
	}
}

func TestContextInUserPrompt(t *testing.T) {
	p := sampleRun(t, "revise")
	p.Context = protocol.ActionContext{
		Cards:     []protocol.ContextItem{{ID: 7, Name: "Billy", Data: json.RawMessage(`{"name": "Billy", "description": "A small goblin."}`)}},
		Lorebooks: []protocol.ContextItem{{ID: 3, Name: "Northern Reach", Data: json.RawMessage(`{"entries": []}`)}},
	}
	_, user, _ := render(t, p, settings{})
	for _, want := range []string{"Card: Billy", "A small goblin.", "Lorebook: Northern Reach"} {
		if !strings.Contains(user, want) {
			t.Errorf("user prompt lacks %q:\n%s", want, user)
		}
	}
	if _, user, _ := render(t, sampleRun(t, "revise"), settings{}); strings.Contains(user, "Attached by the owner") {
		t.Errorf("empty context still rendered:\n%s", user)
	}
}

func TestPromptSettingsMatchKeys(t *testing.T) {
	var m struct {
		Capabilities []string `toml:"capabilities"`
		Config       struct {
			Properties struct {
				Prompts struct {
					Properties map[string]any `toml:"properties"`
				} `toml:"prompts"`
			} `toml:"properties"`
		} `toml:"config"`
	}
	if _, err := toml.DecodeFile("plugin.toml", &m); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.Capabilities, "settings") {
		t.Errorf("capabilities %v lack settings, so the core never asks for the defaults", m.Capabilities)
	}
	want := append([]string{"house_style"}, promptKeys...)
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(m.Config.Properties.Prompts.Properties)); !slices.Equal(got, want) {
		t.Fatalf("prompts properties %v, want %v", got, want)
	}
}
