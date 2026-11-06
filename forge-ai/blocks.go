package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	maxBlocks  = 8
	maxPicks   = 6
	maxColumns = 6
	maxRows    = 20
	// maxLine caps a reason, column, cell, or summary.
	maxLine = 300
	// maxMarkdown caps a text block, which every later turn replays.
	maxMarkdown = 16 << 10
	// maxDraft caps the draft a turn takes, as the core caps a proposal.
	maxDraft = 256 << 10
	// maxEcho caps the input a complaint quotes back to the model.
	maxEcho = 80
)

// cardID is a card ID from the model: a JSON integer, or a string of
// decimal digits, which some models send instead.
type cardID int64

func (id *cardID) UnmarshalJSON(b []byte) error {
	var digits string
	if json.Unmarshal(b, &digits) == nil && digits != "" && strings.Trim(digits, "0123456789") == "" {
		b = []byte(digits)
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("card_id is an integer, got %s", oneLine(string(b), maxEcho))
	}
	*id = cardID(n)
	return nil
}

// pick is one recommended card. The server fills in its name.
type pick struct {
	CardID cardID `json:"card_id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// cell is one table cell: text, or a card the turn's tools returned.
type cell struct {
	Text   string
	CardID cardID
	Name   string
	// invalid is a cell that is neither, kept so the check can say where it is.
	invalid string
}

func (c cell) MarshalJSON() ([]byte, error) {
	if c.CardID == 0 {
		return json.Marshal(c.Text)
	}
	return json.Marshal(struct {
		CardID cardID `json:"card_id"`
		Name   string `json:"name"`
	}{c.CardID, c.Name})
}

func (c *cell) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &c.Text)
	}
	var ref struct {
		CardID cardID `json:"card_id"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(b, &ref); err != nil || ref.CardID <= 0 {
		c.invalid = string(b)
		return nil
	}
	c.CardID, c.Name = ref.CardID, ref.Name
	return nil
}

// block is one piece of a reply; Type says which fields it uses. The panel
// draws each type with its own code.
type block struct {
	Type     string            `json:"type"`
	Markdown string            `json:"markdown,omitempty"`
	Picks    []pick            `json:"picks,omitempty"`
	Columns  []string          `json:"columns,omitempty"`
	Rows     [][]cell          `json:"rows,omitempty"`
	Summary  string            `json:"summary,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	// Card, Revision, and Snapshot are the draft a proposal changes, set by the server.
	Card     int64 `json:"card,omitempty"`
	Revision int64 `json:"revision,omitempty"`
	Snapshot int64 `json:"snapshot,omitempty"`
}

// draft is the card open in the Forge, as the panel sent it. Snapshot is
// the host's ID for that read, which Review starts from.
type draft struct {
	Card     int64           `json:"card"`
	Revision int64           `json:"revision"`
	Snapshot int64           `json:"snapshot,omitempty"`
	Data     json.RawMessage `json:"data"`
}

var replyTool = tool("reply", "Finish the turn with your answer, as blocks.", map[string]any{
	"blocks": map[string]any{"type": "array", "minItems": 1, "maxItems": maxBlocks, "items": map[string]any{
		"type": "object", "required": []string{"type"}, "properties": map[string]any{
			"type":     map[string]any{"type": "string", "enum": []string{"text", "picks", "table", "proposal"}},
			"markdown": map[string]any{"type": "string", "description": "text: what you say, in Markdown without tables"},
			"picks": map[string]any{"type": "array", "maxItems": maxPicks, "description": "picks: the cards you recommend",
				"items": map[string]any{"type": "object", "required": []string{"card_id", "reason"}, "properties": map[string]any{
					"card_id": map[string]any{"type": "integer"},
					"reason":  map[string]any{"type": "string", "description": "One line on why it fits"},
				}}},
			"columns": map[string]any{"type": "array", "maxItems": maxColumns, "items": map[string]any{"type": "string"},
				"description": "table: the column headings"},
			"rows": map[string]any{"type": "array", "maxItems": maxRows, "description": "table: an array per row, a cell per column",
				"items": map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "object", "required": []string{"card_id"}, "properties": map[string]any{"card_id": map[string]any{"type": "integer"}}},
				}}}},
			"summary": map[string]any{"type": "string", "description": "proposal: one line on what changes"},
			"fields": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
				"description": "proposal: the complete new text of each field you change, by key"},
		}}},
}, "blocks")

// checkDraft returns the draft's text fields: name and the prose fields
// that hold text. A nil draft has none.
func checkDraft(d *draft) (map[string]string, error) {
	if d == nil {
		return nil, nil
	}
	if d.Card <= 0 || d.Revision <= 0 {
		return nil, protocol.InvalidParams(errors.New("draft needs its card and revision"))
	}
	if len(d.Data) > maxDraft {
		return nil, protocol.InvalidParams(fmt.Errorf("draft is over %d KiB", maxDraft>>10))
	}
	var data map[string]any
	if err := json.Unmarshal(d.Data, &data); err != nil || data == nil {
		return nil, protocol.InvalidParams(errors.New("draft data must be a JSON object"))
	}
	fields := map[string]string{}
	for _, k := range append([]string{"name"}, textFields...) {
		if v, ok := data[k].(string); ok {
			fields[k] = v
		}
	}
	return fields, nil
}

// newTurn starts a turn for input, calling library for its tools.
func newTurn(input turnInput, library func(name, arguments string) (string, error)) (*turn, error) {
	editable, err := checkDraft(input.Draft)
	if err != nil {
		return nil, err
	}
	return &turn{input: input, library: library, seen: map[int64]string{}, editable: editable}, nil
}

// finish takes a reply call. It returns a complaint for the model, or ""
// once the blocks pass and are kept in t.reply.
func (t *turn) finish(arguments string) string {
	var args struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "error: reply's arguments aren't valid: " + err.Error()
	}
	blocks, err := t.check(args.Blocks)
	if err != nil {
		return "error: " + err.Error() + ". Call reply again with that fixed."
	}
	t.reply = blocks
	return ""
}

// check returns the blocks as the panel may show them, built anew from
// what each type allows.
func (t *turn) check(in []json.RawMessage) ([]block, error) {
	if len(in) == 0 || len(in) > maxBlocks {
		return nil, fmt.Errorf("reply takes 1 to %d blocks", maxBlocks)
	}
	out := make([]block, 0, len(in))
	for i, raw := range in {
		// Decoded apart from the rest, so a block that fails to decode is still named by its type.
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &head)
		var b block
		err := json.Unmarshal(raw, &b)
		if err == nil {
			b, err = t.checkBlock(b)
		}
		if err == nil && b.Type == "proposal" && slices.ContainsFunc(out, func(o block) bool { return o.Type == "proposal" }) {
			err = errors.New("a reply holds at most one proposal")
		}
		if err != nil {
			if head.Type == "" {
				return nil, fmt.Errorf("block %d: %w", i+1, err)
			}
			return nil, fmt.Errorf("block %d (%s): %w", i+1, head.Type, err)
		}
		out = append(out, b)
	}
	return out, nil
}

func (t *turn) checkBlock(b block) (block, error) {
	switch b.Type {
	case "text":
		md := strings.TrimSpace(visible(b.Markdown))
		if md == "" {
			return block{}, errors.New("text needs markdown")
		}
		if len(md) > maxMarkdown {
			return block{}, fmt.Errorf("text is over %d KiB; say it in fewer words", maxMarkdown>>10)
		}
		return block{Type: "text", Markdown: md}, nil
	case "picks":
		return t.checkPicks(b.Picks)
	case "table":
		return t.checkTable(b.Columns, b.Rows)
	case "proposal":
		return t.checkProposal(b.Summary, b.Fields)
	}
	return block{}, fmt.Errorf("unknown block type %q; use text, picks, table, or proposal", b.Type)
}

func (t *turn) checkPicks(in []pick) (block, error) {
	if len(in) == 0 || len(in) > maxPicks {
		return block{}, fmt.Errorf("picks takes 1 to %d cards", maxPicks)
	}
	out := make([]pick, 0, len(in))
	for _, p := range in {
		name, err := t.cardName(p.CardID)
		if err != nil {
			return block{}, err
		}
		if slices.ContainsFunc(out, func(q pick) bool { return q.CardID == p.CardID }) {
			return block{}, fmt.Errorf("card %d is picked twice", p.CardID)
		}
		out = append(out, pick{CardID: p.CardID, Name: name, Reason: oneLine(p.Reason, maxLine)})
	}
	return block{Type: "picks", Picks: out}, nil
}

func (t *turn) checkTable(columns []string, rows [][]cell) (block, error) {
	if len(columns) == 0 || len(columns) > maxColumns {
		return block{}, fmt.Errorf("a table has 1 to %d columns", maxColumns)
	}
	if len(rows) == 0 || len(rows) > maxRows {
		return block{}, fmt.Errorf("a table has 1 to %d rows", maxRows)
	}
	header := make([]string, len(columns))
	for i, c := range columns {
		header[i] = oneLine(c, maxLine)
	}
	body := make([][]cell, len(rows))
	for i, r := range rows {
		if len(r) != len(header) {
			return block{}, fmt.Errorf("row %d has %d cells for %d columns", i+1, len(r), len(header))
		}
		body[i] = make([]cell, len(r))
		for j, c := range r {
			if c.invalid != "" {
				return block{}, fmt.Errorf(`row %d column %d: a cell is text or {"card_id": N}, got %s`, i+1, j+1, oneLine(c.invalid, maxEcho))
			}
			if c.CardID == 0 {
				body[i][j].Text = oneLine(c.Text, maxLine)
				continue
			}
			name, err := t.cardName(c.CardID)
			if err != nil {
				return block{}, err
			}
			body[i][j] = cell{CardID: c.CardID, Name: name}
		}
	}
	return block{Type: "table", Columns: header, Rows: body}, nil
}

func (t *turn) checkProposal(summary string, fields map[string]string) (block, error) {
	if t.input.Draft == nil {
		return block{}, errors.New("no card is open in the Forge, so there is nothing to change")
	}
	if len(fields) == 0 {
		return block{}, errors.New("a proposal changes at least one field")
	}
	out := make(map[string]string, len(fields))
	size := 0
	for k, v := range fields {
		if _, ok := t.editable[k]; !ok {
			return block{}, fmt.Errorf("the card has no text field %q; use one of %s", k, strings.Join(slices.Sorted(maps.Keys(t.editable)), ", "))
		}
		v = strings.TrimSpace(v)
		if k == "name" {
			v = oneLine(v, maxCardName)
		}
		out[k] = v
		size += len(k) + len(out[k])
	}
	// The core refuses a proposal over the same cap as the draft.
	if size > maxDraft {
		return block{}, fmt.Errorf("the proposal's fields are over %d KiB together; change less at once", maxDraft>>10)
	}
	return block{Type: "proposal", Summary: oneLine(summary, maxLine), Fields: out, Card: t.input.Draft.Card, Revision: t.input.Draft.Revision, Snapshot: t.input.Draft.Snapshot}, nil
}

func (t *turn) cardName(id cardID) (string, error) {
	if name, ok := t.seen[int64(id)]; ok {
		return name, nil
	}
	return "", fmt.Errorf("no tool returned card %d in this turn; search, then use cards from the results", id)
}
