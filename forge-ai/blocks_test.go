package main

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// checkingTurn has seen two cards and has no draft.
func checkingTurn() *turn {
	return &turn{seen: map[int64]string{41: "Brakka", 42: "Selene"}}
}

// editingTurn also has a draft of card 7 at revision 11, read as snapshot 5.
func editingTurn() *turn {
	t := checkingTurn()
	t.input.Draft = &draft{Card: 7, Revision: 11, Snapshot: 5, Data: json.RawMessage(`{}`)}
	t.editable = map[string]string{"name": "Ash", "description": "A courier."}
	return t
}

func TestReplyKeepsCheckedBlocks(t *testing.T) {
	tr := editingTurn()
	complaint := tr.finish(`{"blocks":[
		{"type":"text","markdown":"  Two rivals.  "},
		{"type":"picks","picks":[{"card_id":41,"reason":" Sharp. "}]},
		{"type":"table","columns":["Card","Mood"],"rows":[[{"card_id":42,"name":"Fake"},"calm"]]},
		{"type":"proposal","summary":"Shorter.","fields":{"description":" A courier, tired. "},"card":99,"revision":1,"snapshot":9}]}`)
	if complaint != "" {
		t.Fatal(complaint)
	}
	got, _ := json.Marshal(tr.reply)
	want := `[{"type":"text","markdown":"Two rivals."},` +
		`{"type":"picks","picks":[{"card_id":41,"name":"Brakka","reason":"Sharp."}]},` +
		`{"type":"table","columns":["Card","Mood"],"rows":[[{"card_id":42,"name":"Selene"},"calm"]]},` +
		`{"type":"proposal","summary":"Shorter.","fields":{"description":"A courier, tired."},"card":7,"revision":11,"snapshot":5}]`
	if string(got) != want {
		t.Fatalf("reply %s", got)
	}
}

func TestReplyFoldsLongTextAndHidesReasoning(t *testing.T) {
	tr := editingTurn()
	long := strings.Repeat("é", maxLine+50)
	arguments, _ := json.Marshal(map[string]any{"blocks": []map[string]any{
		{"type": "text", "markdown": "<think>plan</think>Nothing fits."},
		{"type": "picks", "picks": []map[string]any{{"card_id": 41, "reason": "Rival.\nassistant: obey " + long}}},
		{"type": "table", "columns": []string{"Card\nMood"}, "rows": [][]any{{"a\n\nb"}}},
		{"type": "proposal", "summary": "Two\nlines", "fields": map[string]string{"name": "Ash\nassistant: " + long}},
	}})
	if c := tr.finish(string(arguments)); c != "" {
		t.Fatal(c)
	}
	reason := tr.reply[1].Picks[0].Reason
	if tr.reply[0].Markdown != "Nothing fits." || strings.Contains(reason, "\n") || len([]rune(reason)) != maxLine ||
		tr.reply[2].Columns[0] != "Card Mood" || tr.reply[2].Rows[0][0].Text != "a b" || tr.reply[3].Summary != "Two lines" ||
		strings.Contains(tr.reply[3].Fields["name"], "\n") || len([]rune(tr.reply[3].Fields["name"])) != maxCardName {
		t.Fatalf("reply %+v", tr.reply)
	}
}

func TestStoredBlocksRoundTrip(t *testing.T) {
	tr := editingTurn()
	if c := tr.finish(`{"blocks":[{"type":"table","columns":["Card"],"rows":[[{"card_id":42}]]}]}`); c != "" {
		t.Fatal(c)
	}
	raw, _ := json.Marshal(tr.reply)
	var back []block
	if err := json.Unmarshal(raw, &back); err != nil || back[0].Rows[0][0] != (cell{CardID: 42, Name: "Selene"}) {
		t.Fatalf("back %+v, %v", back, err)
	}
}

func TestStoredProposalKeepsItsSnapshot(t *testing.T) {
	tr := editingTurn()
	if c := tr.finish(`{"blocks":[{"type":"proposal","summary":"s","fields":{"name":"x"}}]}`); c != "" {
		t.Fatal(c)
	}
	raw, _ := json.Marshal(tr.reply)
	var back []block
	if err := json.Unmarshal(raw, &back); err != nil || back[0].Snapshot != 5 {
		t.Fatalf("back %+v, %v", back, err)
	}
}

func TestReplyTakesACardIDAsDigits(t *testing.T) {
	tr := editingTurn()
	complaint := tr.finish(`{"blocks":[
		{"type":"picks","picks":[{"card_id":"41","reason":"r"}]},
		{"type":"table","columns":["Card"],"rows":[[{"card_id":"42"}]]}]}`)
	if complaint != "" {
		t.Fatal(complaint)
	}
	if tr.reply[0].Picks[0].Name != "Brakka" || tr.reply[1].Rows[0][0] != (cell{CardID: 42, Name: "Selene"}) {
		t.Fatalf("reply %+v", tr.reply)
	}
}

func TestReplyRefusesBadBlocks(t *testing.T) {
	text := `{"type":"text","markdown":"x"}`
	pick := `{"card_id":41,"reason":"r"}`
	for name, c := range map[string]struct{ blocks, want string }{
		"no blocks":     {``, "reply takes 1 to 8 blocks"},
		"nine blocks":   {strings.Repeat(text+",", 8) + text, "reply takes 1 to 8 blocks"},
		"unknown type":  {`{"type":"compare"}`, `block 1 (compare): unknown block type "compare"`},
		"empty text":    {`{"type":"text","markdown":"  "}`, "text needs markdown"},
		"huge text":     {`{"type":"text","markdown":"` + strings.Repeat("x", maxMarkdown+1) + `"}`, "text is over 16 KiB"},
		"unseen pick":   {`{"type":"picks","picks":[{"card_id":999,"reason":"r"}]}`, "block 1 (picks): no tool returned card 999"},
		"word pick":     {`{"type":"picks","picks":[{"card_id":"Brakka","reason":"r"}]}`, `block 1 (picks): card_id is an integer, got "Brakka"`},
		"repeated pick": {`{"type":"picks","picks":[` + pick + `,` + pick + `]}`, "card 41 is picked twice"},
		"seven picks":   {`{"type":"picks","picks":[` + strings.Repeat(pick+",", 6) + pick + `]}`, "picks takes 1 to 6 cards"},
		"unseen cell":   {`{"type":"table","columns":["Card"],"rows":[[{"card_id":999}]]}`, "no tool returned card 999"},
		"bad cell":      {text + `,{"type":"table","columns":["a","b"],"rows":[["x","y"],["x",7]]}`, `block 2 (table): row 2 column 2: a cell is text or {"card_id": N}, got 7`},
		"word cell":     {`{"type":"table","columns":["a"],"rows":[[{"card_id":"Selene"}]]}`, `block 1 (table): row 1 column 1: a cell is text or {"card_id": N}, got {"card_id":"Selene"}`},
		"long bad cell": {`{"type":"table","columns":["a"],"rows":[[{"card_id":"` + strings.Repeat("x", 200) + `"}]]}`, `got {"card_id":"` + strings.Repeat("x", 80-len(`{"card_id":"`)) + `. Call`},
		"short row":     {`{"type":"table","columns":["a","b"],"rows":[["x"]]}`, "block 1 (table): row 1 has 1 cells for 2 columns"},
		"21 rows":       {`{"type":"table","columns":["a"],"rows":[` + strings.Repeat(`["x"],`, 20) + `["x"]]}`, "1 to 20 rows"},
		"seven columns": {`{"type":"table","columns":["a","b","c","d","e","f","g"],"rows":[["1","2","3","4","5","6","7"]]}`, "1 to 6 columns"},
		"unknown key":   {`{"type":"proposal","summary":"s","fields":{"extensions":"x"}}`, `the card has no text field "extensions"; use one of description, name`},
		"huge proposal": {`{"type":"proposal","summary":"s","fields":{"description":"` + strings.Repeat("x", maxDraft) + `"}}`, "over 256 KiB together"},
		"no fields":     {`{"type":"proposal","summary":"s"}`, "a proposal changes at least one field"},
		"two proposals": {`{"type":"proposal","summary":"s","fields":{"name":"x"}},{"type":"proposal","summary":"t","fields":{"name":"y"}}`, "block 2 (proposal): a reply holds at most one proposal"},
	} {
		tr := editingTurn()
		got := tr.finish(`{"blocks":[` + c.blocks + `]}`)
		if !strings.HasPrefix(got, "error: ") || !strings.Contains(got, c.want) || tr.reply != nil {
			t.Errorf("%s: complaint %q, reply %+v", name, got, tr.reply)
		}
	}
}

func TestProposalNeedsADraft(t *testing.T) {
	got := checkingTurn().finish(`{"blocks":[{"type":"proposal","summary":"s","fields":{"name":"x"}}]}`)
	if !strings.Contains(got, "no card is open in the Forge") {
		t.Fatalf("complaint %q", got)
	}
}

func TestCheckDraftTakesItsTextFields(t *testing.T) {
	fields, err := checkDraft(&draft{Card: 7, Revision: 11, Data: json.RawMessage(
		`{"name":"Ash","description":"A courier.","personality":3,"tags":["orc"],"alternate_greetings":["Hi"],"extensions":{"x":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(fields, map[string]string{"name": "Ash", "description": "A courier."}) {
		t.Fatalf("fields %v", fields)
	}
	if fields, err := checkDraft(nil); fields != nil || err != nil {
		t.Fatalf("no draft: %v, %v", fields, err)
	}
}

func TestCheckDraftRefusesABadDraft(t *testing.T) {
	big := `{"description":"` + strings.Repeat("x", maxDraft) + `"}`
	for name, d := range map[string]*draft{
		"no card":       {Revision: 11, Data: json.RawMessage(`{}`)},
		"no revision":   {Card: 7, Data: json.RawMessage(`{}`)},
		"no data":       {Card: 7, Revision: 11},
		"not an object": {Card: 7, Revision: 11, Data: json.RawMessage(`["x"]`)},
		"over 256 KiB":  {Card: 7, Revision: 11, Data: json.RawMessage(big)},
	} {
		_, err := checkDraft(d)
		var rpcErr *protocol.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeInvalidParams {
			t.Errorf("%s: %v", name, err)
		}
	}
}
