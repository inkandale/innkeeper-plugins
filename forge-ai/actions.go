package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// textFields are the card's prose fields, which field actions run on.
var textFields = []string{"description", "personality", "scenario", "first_mes", "mes_example", "system_prompt",
	"post_history_instructions", "creator_notes"}

// cardKeys are the card data keys whole-card actions send the model and take
// back. Other keys, such as extensions, never reach the model.
var cardKeys = append(append([]string{"name"}, textFields...), "tags")

// povFields are the fields change-pov rewrites; creator_notes speaks to the player, not as {{char}}.
var povFields = []string{"description", "personality", "scenario", "first_mes", "mes_example", "system_prompt", "post_history_instructions"}

// generatedKeys are what generate writes, in its prompt's order.
var generatedKeys = []string{"name", "description", "personality", "scenario", "first_mes", "mes_example", "tags",
	"system_prompt", "post_history_instructions"}

// action is one declared action and how it runs.
type action struct {
	protocol.Action
	temperature float64
	maxTokens   int
	// appends is set for an action that adds to a list instead of replacing keys.
	appends *appender
	// evaluates is set for a report-only action.
	evaluates *evaluator
	// edits is set for an action that changes a lorebook's entries in place.
	edits *editor
}

const povSchema = `{"type": "object", "properties": {"point_of_view": {"type": "string", "title": "Point of view", "enum": [%s]}}%s}`

var actions = []action{
	{Action: protocol.Action{ID: "generate", Label: "Forge", Description: "Write a new card from a concept.",
		Applies: protocol.ApplyNew, Instruction: true, Library: true, Images: true,
		Inputs: json.RawMessage(fmt.Sprintf(povSchema, `"first", "third", "scenario"`, ""))}, temperature: 0.8, maxTokens: 8192},
	{Action: protocol.Action{ID: "revise", Label: "Revise", Description: "Change it as you describe.",
		Applies: protocol.ApplyCard, Fields: cardKeys, WholeCard: true, Instruction: true, Library: true}, temperature: 0.7, maxTokens: 4096},
	{Action: protocol.Action{ID: "tighten", Label: "Tighten", Description: "Cut filler and repetition; keep every fact.",
		Applies: protocol.ApplyCard, Fields: textFields, WholeCard: true}, temperature: 0.5, maxTokens: 4096},
	{Action: protocol.Action{ID: "expand", Label: "Expand", Description: "Add concrete detail without changing what's there.",
		Applies: protocol.ApplyCard, Fields: textFields, WholeCard: true}, temperature: 0.8, maxTokens: 4096},
	{Action: protocol.Action{ID: "to-prose", Label: "Convert to prose", Description: "Turn lists and bracket formats into paragraphs.",
		Applies: protocol.ApplyCard, Fields: textFields, WholeCard: true}, temperature: 0.5, maxTokens: 4096},
	{Action: protocol.Action{ID: "change-pov", Label: "Change point of view", Description: "Switch between first and third person.",
		Applies: protocol.ApplyCard, Fields: povFields, WholeCard: true,
		Inputs: json.RawMessage(fmt.Sprintf(povSchema, `"first", "third"`, `, "required": ["point_of_view"]`))}, temperature: 0.6, maxTokens: 4096},
	{Action: protocol.Action{ID: "examples", Label: "Write examples", Description: "Replace the example dialogue with new exchanges.",
		Applies: protocol.ApplyCard, Fields: []string{"mes_example"},
		Inputs: json.RawMessage(`{"type": "object", "properties": {"count": {"type": "integer", "title": "Examples", "minimum": 1, "maximum": 10, "default": 3}}}`)}, temperature: 0.8, maxTokens: 4096},
	{Action: protocol.Action{ID: "alternate-greetings", Label: "Write alternate greetings", Description: "Add new opening messages to the card.",
		Applies: protocol.ApplyCard, Library: true,
		Inputs: json.RawMessage(`{"type": "object", "properties": {"count": {"type": "integer", "title": "Greetings", "minimum": 1, "maximum": 10, "default": 3}}}`)}, temperature: 0.9, maxTokens: 8192, appends: greetingsAppender},
	// revise-lorebook comes before lore-entries so the core's Reforge box and Refine run it.
	{Action: protocol.Action{ID: "revise-lorebook", Label: "Revise", Description: "Change it as you describe.",
		Applies: protocol.ApplyLorebook, Instruction: true, Library: true}, temperature: 0.7, maxTokens: 8192, edits: lorebookEditor},
	{Action: protocol.Action{ID: "lore-entries", Label: "Write lore entries", Description: "Add entries about what you describe, from the cards you mention.",
		Applies: protocol.ApplyLorebook, Instruction: true, Library: true,
		Inputs: json.RawMessage(`{"type": "object", "properties": {"count": {"type": "integer", "title": "Entries", "minimum": 1, "maximum": 20, "default": 5}}}`)}, temperature: 0.7, maxTokens: 8192, appends: entriesAppender},
	{Action: protocol.Action{ID: "evaluate", Label: "Evaluate", Description: "Score the card on six dimensions and list its problems.",
		Applies: protocol.ApplyCard, ReportOnly: true}, temperature: 0, maxTokens: 4096, evaluates: cardEvaluator},
	{Action: protocol.Action{ID: "evaluate-lorebook", Label: "Evaluate", Description: "Score the lorebook on five dimensions and list its problems.",
		Applies: protocol.ApplyLorebook, ReportOnly: true}, temperature: 0, maxTokens: 8192, evaluates: lorebookEvaluator},
}

// limits are the temperature and max tokens a's runs send: the instance's
// settings, else the action's own.
func (a action) limits(s settings) (temperature float64, maxTokens int) {
	temperature, maxTokens = a.temperature, a.maxTokens
	if s.Temperature != nil {
		temperature = *s.Temperature
	}
	if s.MaxTokens != nil {
		maxTokens = *s.MaxTokens
	}
	return temperature, maxTokens
}

func findAction(id string) (action, bool) {
	i := slices.IndexFunc(actions, func(a action) bool { return a.ID == id })
	if i < 0 {
		return action{}, false
	}
	return actions[i], true
}

// run is one action.run, checked.
type run struct {
	action
	protocol.ActionRunParams
	draft       map[string]json.RawMessage
	pointOfView string
	count       int
	// model is the model that answered, from the ai instance's reply.
	model string
	// note is the plan's note for this field, in a whole-card run.
	note string
}

// parseRun checks the params against the action's declaration.
func parseRun(p protocol.ActionRunParams) (*run, error) {
	a, ok := findAction(p.Action)
	if !ok {
		return nil, protocol.InvalidParams(fmt.Errorf("unknown action %q", p.Action))
	}
	r := &run{action: a, ActionRunParams: p}
	if len(a.Fields) > 0 && !slices.Contains(a.Fields, p.Field) && (p.Field != "" || !a.WholeCard) {
		return nil, protocol.InvalidParams(fmt.Errorf("action %s doesn't run on field %q", a.ID, p.Field))
	}
	if a.Instruction && strings.TrimSpace(p.Instruction) == "" {
		return nil, protocol.InvalidParams(fmt.Errorf("action %s needs an instruction", a.ID))
	}
	if a.Applies != protocol.ApplyNew {
		if err := json.Unmarshal(p.Entity.Data, &r.draft); err != nil || r.draft == nil {
			return nil, protocol.InvalidParams(fmt.Errorf("action %s needs the %s's data", a.ID, a.Applies))
		}
	}
	var inputs struct {
		PointOfView string `json:"point_of_view"`
		Count       int    `json:"count"`
	}
	if len(p.Inputs) > 0 {
		if err := json.Unmarshal(p.Inputs, &inputs); err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("inputs: %v", err))
		}
	}
	r.pointOfView, r.count = inputs.PointOfView, inputs.Count
	switch a.ID {
	case "generate":
		if r.pointOfView == "" {
			r.pointOfView = "third"
		}
		if !slices.Contains([]string{"first", "third", "scenario"}, r.pointOfView) {
			return nil, protocol.InvalidParams(errors.New("point_of_view must be first, third, or scenario"))
		}
	case "change-pov":
		if r.pointOfView != "first" && r.pointOfView != "third" {
			return nil, protocol.InvalidParams(errors.New("point_of_view must be first or third"))
		}
	case "examples", "alternate-greetings":
		r.count = between(r.count, 3, 10)
	case "lore-entries":
		r.count = between(r.count, 5, 20)
	}
	return r, nil
}

// between is count, or def when it's unset, kept within 1 to most.
func between(count, def, most int) int {
	if count == 0 {
		return def
	}
	return min(max(count, 1), most)
}

// writes is the keys the model returns for this run.
func (r *run) writes() []string {
	switch {
	case r.appends != nil:
		return []string{r.appends.key}
	case r.evaluates != nil:
		return r.evaluates.keys
	case r.edits != nil:
		return r.edits.keys
	case len(r.Fields) > 0:
		return []string{r.Field}
	case r.pointOfView == "scenario":
		return append(slices.Clone(generatedKeys), "creator_notes")
	}
	return generatedKeys
}

// sent is the card as the model sees it: cardKeys, with absent ones empty.
func (r *run) sent() (map[string]any, error) {
	card := map[string]any{}
	for _, k := range cardKeys {
		v, err := typed(k, r.draft[k])
		if err != nil {
			return nil, fmt.Errorf("the card's %s: %w", k, err)
		}
		card[k] = v
	}
	return card, nil
}

// typed decodes a card value: a list of strings for tags, otherwise a string.
// Absent is empty. Tags written as comma-separated text are split.
func typed(key string, raw json.RawMessage) (any, error) {
	if key == "tags" {
		tags := []string{}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			for t := range strings.SplitSeq(text, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
			return tags, nil
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &tags); err != nil {
				return nil, errors.New("isn't a list of text")
			}
		}
		return tags, nil
	}
	var s string
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errors.New("isn't text")
		}
	}
	return s, nil
}

// result turns the model's output into the reply.
func (r *run) result(output string) (protocol.ActionRunResult, error) {
	res := protocol.ActionRunResult{Raw: output}
	obj, err := parseObject(output)
	if err != nil {
		return res, err
	}
	switch {
	case r.evaluates != nil:
		res.Report, err = r.evaluates.report(obj)
	case r.appends != nil:
		res.Proposal, err = r.appends.merge(r, obj[r.appends.key])
	case r.edits != nil:
		res.Proposal, err = r.edits.merge(r, obj)
	default:
		res.Proposal, err = r.proposal(obj)
	}
	return res, err
}

// proposal is the draft with each key the model changed replaced.
func (r *run) proposal(got map[string]json.RawMessage) (json.RawMessage, error) {
	sent, err := r.sent()
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for k, v := range r.draft {
		out[k] = v
	}
	for _, k := range r.writes() {
		raw, ok := got[k]
		if !ok || string(raw) == "null" {
			continue
		}
		v, err := typed(k, raw)
		if err != nil {
			return nil, fmt.Errorf("the model's %s %w", k, err)
		}
		if reflect.DeepEqual(v, sent[k]) {
			continue
		}
		if out[k], err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	return json.Marshal(out)
}

// evaluator is how a report-only action's evaluation becomes a report.
type evaluator struct {
	keys []string // the keys the model returns
	// dimensions are scored, in the prompt's order.
	dimensions []dimension
	// enriches is set when the dimensions are the card enrichments the manifest declares.
	enriches bool
}

type dimension struct{ key, label string }

var cardEvaluator = &evaluator{
	keys: []string{"summary", "overall_score", "card_type", "dimensions", "contradictions", "misplaced_content", "suggestions"},
	dimensions: []dimension{
		{"consistency", "Consistency"}, {"voice", "Voice"}, {"first_impression", "First impression"},
		{"roleplayability", "Roleplayability"}, {"token_efficiency", "Token efficiency"}, {"field_governance", "Field governance"},
	},
	enriches: true,
}

var lorebookEvaluator = &evaluator{
	keys: []string{"summary", "overall_score", "dimensions", "contradictions", "problems", "suggestions"},
	dimensions: []dimension{
		{"triggers", "Triggers"}, {"consistency", "Consistency"}, {"focus", "Focus"},
		{"usefulness", "Usefulness"}, {"token_efficiency", "Token efficiency"},
	},
}

// issue is a problem the model reports: in a card's fields, or in a
// lorebook's entries, named by title.
type issue struct {
	Fields  []string `json:"fields"`
	Entries []string `json:"entries"`
	Issue   string   `json:"issue"`
	Fix     string   `json:"fix"`
}

// finding is i as a finding, or false when it says nothing. Named entries
// lead the text, and the finding sits under the entries.
func (i issue) finding(severity string) (protocol.Finding, bool) {
	text := strings.TrimSpace(i.Issue)
	if text == "" {
		return protocol.Finding{}, false
	}
	fields := i.Fields
	if len(i.Entries) > 0 {
		text = strings.Join(i.Entries, ", ") + ": " + text
		if len(fields) == 0 {
			fields = []string{"entries"}
		}
	}
	if fix := strings.TrimSpace(i.Fix); fix != "" {
		text += " Fix: " + fix
	}
	return protocol.Finding{Severity: severity, Text: text, Fields: fields}, true
}

// evaluation is the JSON the evaluate prompts ask for.
type evaluation struct {
	Summary      string   `json:"summary"`
	OverallScore *float64 `json:"overall_score"`
	Dimensions   map[string]struct {
		Score   *float64 `json:"score"`
		Comment string   `json:"comment"`
	} `json:"dimensions"`
	Contradictions   []issue `json:"contradictions"`
	Problems         []issue `json:"problems"`
	MisplacedContent []struct {
		Excerpt        string `json:"excerpt"`
		CurrentField   string `json:"current_field"`
		SuggestedField string `json:"suggested_field"`
		Reason         string `json:"reason"`
	} `json:"misplaced_content"`
	Suggestions []suggestion `json:"suggestions"`
}

// suggestion is one of the model's suggestions. The prompts ask for a
// string, but models sometimes send an object with the text under a key.
type suggestion string

func (s *suggestion) UnmarshalJSON(b []byte) error {
	var text string
	if json.Unmarshal(b, &text) == nil {
		*s = suggestion(text)
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	var parts []string
	for _, k := range []string{"suggestion", "text", "description", "issue", "fix"} {
		if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
	}
	*s = suggestion(strings.Join(parts, " "))
	return nil
}

func (ev *evaluator) report(obj map[string]json.RawMessage) (*protocol.Report, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var e evaluation
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("the model's evaluation: %w", err)
	}
	r := &protocol.Report{Summary: e.Summary, Score: e.OverallScore}
	for _, s := range e.Suggestions {
		if s != "" {
			r.Suggestions = append(r.Suggestions, string(s))
		}
	}
	var sum float64
	for _, d := range ev.dimensions {
		if v, ok := e.Dimensions[d.key]; ok && v.Score != nil {
			r.Dimensions = append(r.Dimensions, protocol.ReportDimension{Key: d.key, Label: d.label, Score: *v.Score, Comment: v.Comment})
			sum += min(max(*v.Score, 0), 100)
		}
	}
	// The dimensions' mean varies less between runs than the model's own overall score.
	if n := len(r.Dimensions); n > 0 {
		mean := sum / float64(n)
		r.Score = &mean
	}
	for _, c := range e.Contradictions {
		if f, ok := c.finding("high"); ok {
			r.Findings = append(r.Findings, f)
		}
	}
	for _, p := range e.Problems {
		if f, ok := p.finding("medium"); ok {
			r.Findings = append(r.Findings, f)
		}
	}
	for _, m := range e.MisplacedContent {
		f := protocol.Finding{Severity: "medium", Text: m.Reason}
		if m.CurrentField != "" {
			f.Fields = []string{m.CurrentField}
		}
		if m.SuggestedField != "" {
			f.Move = &protocol.FindingMove{Excerpt: m.Excerpt, To: m.SuggestedField}
			if strings.TrimSpace(f.Text) == "" {
				f.Text = "Belongs in " + m.SuggestedField + "."
			}
		}
		if strings.TrimSpace(f.Text) != "" {
			r.Findings = append(r.Findings, f)
		}
	}
	return r, r.Validate()
}
