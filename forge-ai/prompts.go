package main

import (
	"embed"
	"encoding/json"
	"strings"
	"text/template"

	"github.com/inkandale/innkeeper/protocol"
)

//go:embed prompts/*.tmpl
var promptFiles embed.FS

// The prompts use [[ ]] so that {{char}} and {{user}} reach the model as written.
var templates = template.Must(template.New("").Delims("[[", "]]").ParseFS(promptFiles, "prompts/*.tmpl"))

// promptData is what the templates see.
type promptData struct {
	Card        string
	Lorebook    string
	Existing    string
	Field       string
	FieldText   string
	Instruction string
	PointOfView string
	Count       int
	Image       bool
	Context     protocol.ActionContext
	// Instructions are the action's rules: the owner's override or the default.
	Instructions string
	// Keys are the keys the model returns, quoted, for its JSON contract.
	Keys string
	// Note is the plan's note for the field, in a whole-card run.
	Note string
	// Action and ActionInstructions describe the edit a whole-card plan is for.
	Action             string
	ActionInstructions string
}

// promptKeys are the instructions blocks, in the Prompts tab's order. Each is
// a setting under prompts and a template named <key>.instructions.
var promptKeys = []string{"whole-card", "generate-first", "generate-third", "generate-scenario", "revise", "tighten", "expand",
	"to-prose", "change-pov", "examples", "alternate-greetings", "revise-lorebook", "lore-entries", "evaluate", "evaluate-lorebook"}

// instructionsKey names the run's instructions block.
func (r *run) instructionsKey() string {
	if r.ID == "generate" {
		return "generate-" + r.pointOfView
	}
	return r.ID
}

// instructions are key's override, or else its default block. The core
// saves no key for a field left at its default, so an empty override is
// the owner asking for no instructions.
func instructions(s settings, key string) (string, error) {
	if o, ok := s.Prompts[key]; ok {
		return strings.TrimSpace(o), nil
	}
	return execute(key+".instructions", promptData{})
}

// settingsDefaults is each instructions block, by its setting's pointer.
func settingsDefaults() (protocol.SettingsDefaultsResult, error) {
	out := protocol.SettingsDefaultsResult{Defaults: map[string]any{}}
	for _, k := range promptKeys {
		text, err := execute(k+".instructions", promptData{})
		if err != nil {
			return out, err
		}
		out.Defaults["/prompts/"+k] = text
	}
	return out, nil
}

// prompts renders the run's system and user prompts. The system prompt ends
// with the owner's house style.
func (r *run) prompts(s settings) (system, user string, err error) {
	d := promptData{Field: r.Field, Instruction: r.Instruction, PointOfView: r.pointOfView, Count: r.count,
		Image: r.Images.Reference != "" || r.Images.Avatar != "", Context: r.Context, Note: r.note}
	switch {
	case r.Applies == protocol.ApplyLorebook:
		book, err := r.sentLorebook()
		if err != nil {
			return "", "", err
		}
		if d.Lorebook, err = indented(book); err != nil {
			return "", "", err
		}
	case r.draft != nil:
		card, err := r.sent()
		if err != nil {
			return "", "", err
		}
		if d.Card, err = indented(card); err != nil {
			return "", "", err
		}
		switch v := card[r.Field].(type) {
		case string:
			d.FieldText = v
		case []string:
			d.FieldText = strings.Join(v, ", ")
		}
		if r.appends != nil && r.appends.existing != nil {
			list, err := r.appends.existing(r)
			if err != nil {
				return "", "", err
			}
			if len(list) > 0 {
				if d.Existing, err = indented(list); err != nil {
					return "", "", err
				}
			}
		}
	}
	if d.Instructions, err = instructions(s, r.instructionsKey()); err != nil {
		return "", "", err
	}
	d.Keys = quoted(r.writes())
	if system, err = execute(r.ID+".system", d); err != nil {
		return "", "", err
	}
	if user, err = execute(r.ID+".user", d); err != nil {
		return "", "", err
	}
	if hs := strings.TrimSpace(s.Prompts["house_style"]); hs != "" {
		system += "\n\n## House style\n\n" + hs
	}
	return system, user, nil
}

// planPrompts renders the prompts for a whole-card run's plan.
func (r *run) planPrompts(s settings) (system, user string, err error) {
	card, err := r.sent()
	if err != nil {
		return "", "", err
	}
	d := promptData{Instruction: r.Instruction, PointOfView: r.pointOfView, Context: r.Context, Action: r.Label, Keys: quoted(r.Fields)}
	if d.Card, err = indented(card); err != nil {
		return "", "", err
	}
	if d.Instructions, err = instructions(s, "whole-card"); err != nil {
		return "", "", err
	}
	if d.ActionInstructions, err = instructions(s, r.instructionsKey()); err != nil {
		return "", "", err
	}
	if system, err = execute("plan.system", d); err != nil {
		return "", "", err
	}
	if user, err = execute("plan.user", d); err != nil {
		return "", "", err
	}
	if hs := strings.TrimSpace(s.Prompts["house_style"]); hs != "" {
		system += "\n\n## House style\n\n" + hs
	}
	return system, user, nil
}

// quoted is keys, each in double quotes, separated by commas.
func quoted(keys []string) string {
	return `"` + strings.Join(keys, `", "`) + `"`
}

// indented is v as indented JSON.
func indented(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // Keep <START> as written.
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

func execute(name string, d promptData) (string, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, d); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}
