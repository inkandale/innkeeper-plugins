package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// script answers each model round in order, and records each request and
// what the panel was shown.
type script struct {
	rounds   []protocol.AIChatResult
	requests []protocol.AIChatParams
	events   []uiEvent
}

func (s *script) chat(_ context.Context, p protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error) {
	s.requests = append(s.requests, p)
	r := s.rounds[min(len(s.requests)-1, len(s.rounds)-1)]
	if r.Text != "" {
		onText(r.Text)
	}
	return r, nil
}

func (s *script) deps(lib func(name, arguments string) (string, error)) turnDeps {
	return turnDeps{chat: s.chat, library: lib, show: func(e uiEvent) { s.events = append(s.events, e) }}
}

// lastResult is the last message the model was sent.
func (s *script) lastResult() string {
	messages := s.requests[len(s.requests)-1].Messages
	return messages[len(messages)-1].Content
}

func call(name, arguments string) protocol.AIChatResult {
	return protocol.AIChatResult{ToolCalls: []protocol.ToolCall{{ID: name, Name: name, Arguments: arguments}}}
}

const searchResult = `{"hits":[
	{"id":41,"slug":"brakka","revision_id":410,"fields":{"name":"Brakka","description":"An orc rival."},"tags":["rival"],
	 "score":{"instance":5,"key":"quality","value":80,"stale":false},
	 "scores":[{"instance":5,"key":"quality","value":80,"stale":false}]},
	{"id":42,"slug":"selene","revision_id":420,"fields":{"name":"Selene"}}],"total":2}`

const similarResult = `{"basis":"tags","items":[
	{"card_id":51,"library_id":1,"name":"Near","avatar":"abc","tags":["rival"],"score":0.8},
	{"card_id":52,"library_id":2,"name":"Elsewhere","tags":[],"score":0.7}]}`

const relatedResult = `{"groups":[
	{"group":"creator","items":[{"card_id":61,"library_id":1,"name":"Same hand","tags":[]}]},
	{"group":"lorebook","items":[{"card_id":62,"library_id":2,"name":"Far book","tags":[]}]}]}`

const cardResult = `{"entity":{"id":71,"library_id":1,"slug":"mira","provenance":"import",
	"revision":{"id":710,"data":{"name":"Mira","description":"A pilot."},"content_hash":"h","message":"","source":"ui"},
	"lorebooks":[77],"collections":[78],"variant_of":{"id":79,"slug":"m","name":"Original"},"variants":[{"id":80,"slug":"v","name":"Variant"}]},
	"bindings":[{"id":81,"instance":3,"name":"A binding"}]}`

// library answers as the core's tools do.
func library(name, _ string) (string, error) {
	switch name {
	case "search_cards":
		return searchResult, nil
	case "find_similar_cards":
		return similarResult, nil
	case "find_related_cards":
		return relatedResult, nil
	case "get_card":
		return cardResult, nil
	}
	return "", fmt.Errorf("no tool %s", name)
}

func picked(ids ...int64) string {
	var picks []string
	for _, id := range ids {
		picks = append(picks, fmt.Sprintf(`{"card_id":%d,"reason":"r"}`, id))
	}
	return `{"message":"m","picks":[` + strings.Join(picks, ",") + `]}`
}

func TestTurnKeepsPicksTheToolsReturned(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{
		call("search", `{"text":"rival"}`),
		call("recommend", `{"message":"Try these.","picks":[{"card_id":41,"reason":"A rival."},{"card_id":999,"reason":"Invented."}]}`),
	}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "a rival", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Picks) != 1 || got.Picks[0].CardID != 41 || got.Picks[0].Name != "Brakka" {
		t.Fatalf("picks: %+v", got.Picks)
	}
	if got.Text != "Try these." {
		t.Fatalf("text: %q", got.Text)
	}
}

func TestTurnOffersOnlyRecommendOnTheLastRound(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`)}}
	_, _ = runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if len(m.requests) != maxRounds {
		t.Fatalf("rounds: %d", len(m.requests))
	}
	last := m.requests[maxRounds-1].Tools
	if len(last) != 1 || last[0].Name != "recommend" {
		t.Fatalf("last round tools: %+v", last)
	}
}

func TestTurnLimitsPicks(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{
		call("search", `{"text":"x"}`),
		call("recommend", picked(41, 41, 41, 41, 41, 41, 41, 41, 41)),
	}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Picks) != 1 {
		t.Fatalf("repeats should collapse: %+v", got.Picks)
	}
}

func TestTurnStopsAtSixPicks(t *testing.T) {
	var items []string
	var ids []int64
	for id := int64(1); id <= 9; id++ {
		items = append(items, fmt.Sprintf(`{"card_id":%d,"library_id":1,"name":"c","tags":[]}`, id))
		ids = append(ids, id)
	}
	lib := func(string, string) (string, error) {
		return `{"basis":"tags","items":[` + strings.Join(items, ",") + `]}`, nil
	}
	m := &script{rounds: []protocol.AIChatResult{call("similar", `{"card_id":1}`), call("recommend", picked(ids...))}}
	got, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Picks) != maxPicks {
		t.Fatalf("picks: %+v", got.Picks)
	}
}

func TestTurnSearchesSemanticallyThenFallsBack(t *testing.T) {
	for _, reason := range []string{
		"semantic search needs a vector source; pick one in Settings under Plugins, Vectors from",
		"semantic search needs pgvector; see docs/running.md",
	} {
		var calls []string
		lib := func(name, arguments string) (string, error) {
			calls = append(calls, arguments)
			if strings.Contains(arguments, `"semantic":true`) {
				return "", errToolFailed(`409 Conflict: {"title":"Conflict","status":409,"detail":"` + reason + `"}`)
			}
			return library(name, arguments)
		}
		m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "Done."}}}
		got, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) != 2 {
			t.Fatalf("calls: %v", calls)
		}
		if !strings.Contains(m.lastResult(), "text search") {
			t.Fatalf("the model wasn't told: %s", m.lastResult())
		}
		if got.Text != "Done." {
			t.Fatalf("text: %q", got.Text)
		}
	}
}

func TestTurnDoesNotFallBackOnOtherSearchFailures(t *testing.T) {
	for _, failure := range []string{
		`400 Bad Request: {"detail":"invalid: semantic search needs text"}`,
		`500 Internal Server Error: {"detail":"boom"}`,
		`409 Conflict: {"detail":"plugin instance is disabled; enable it first"}`,
	} {
		var calls int
		lib := func(string, string) (string, error) {
			calls++
			return "", errToolFailed(failure)
		}
		m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "Sorry."}}}
		if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("%s: searched %d times", failure, calls)
		}
		if !strings.HasPrefix(m.lastResult(), "error: ") {
			t.Fatalf("the model wasn't told: %s", m.lastResult())
		}
	}
}

func TestTurnScopesSearchToTheLibrary(t *testing.T) {
	for _, all := range []bool{false, true} {
		var seen string
		lib := func(_, arguments string) (string, error) {
			seen = arguments
			return `{"hits":[],"total":0}`, nil
		}
		m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "None."}}}
		if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 7, AllLibraries: all}, nil); err != nil {
			t.Fatal(err)
		}
		var args struct {
			Library int64 `json:"library"`
		}
		if err := json.Unmarshal([]byte(seen), &args); err != nil || args.Library != 7 {
			t.Fatalf("arguments: %s", seen)
		}
	}
}

func TestTurnSaysSearchCoversOneLibrary(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1, AllLibraries: true}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.lastResult(), "only this library") {
		t.Fatalf("the model wasn't told: %s", m.lastResult())
	}
}

func TestTurnAsksSearchForTheShortForm(t *testing.T) {
	var seen struct {
		Query struct {
			Semantic bool     `json:"semantic"`
			Fields   []string `json:"fields"`
			Include  []string `json:"include"`
		} `json:"query"`
	}
	lib := func(name, arguments string) (string, error) {
		if err := json.Unmarshal([]byte(arguments), &seen); err != nil {
			t.Fatal(err)
		}
		return library(name, arguments)
	}
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if !seen.Query.Semantic || strings.Join(seen.Query.Fields, ",") != "name,description" ||
		strings.Join(seen.Query.Include, ",") != "tags,score" {
		t.Fatalf("query: %+v", seen.Query)
	}
}

func TestTurnTrimsSearchResults(t *testing.T) {
	long := strings.Repeat("é", 500)
	lib := func(string, string) (string, error) {
		return `{"hits":[{"id":41,"slug":"s","revision_id":1,"fields":{"name":"Brakka","description":"` + long + `"},"tags":["rival"],
			"scores":[{"instance":5,"key":"quality","value":80,"stale":false}]}],"total":1}`, nil
	}
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil); err != nil {
		t.Fatal(err)
	}
	var cards []struct {
		ID          int64           `json:"id"`
		Name        string          `json:"name"`
		Tags        []string        `json:"tags"`
		Description string          `json:"description"`
		Scores      map[string]int  `json:"scores"`
		Slug        json.RawMessage `json:"slug"`
	}
	if err := json.Unmarshal([]byte(m.lastResult()), &cards); err != nil {
		t.Fatalf("%v: %s", err, m.lastResult())
	}
	c := cards[0]
	if c.ID != 41 || c.Name != "Brakka" || c.Tags[0] != "rival" || c.Scores["quality"] != 80 || c.Slug != nil {
		t.Fatalf("card: %+v", c)
	}
	if got := len([]rune(c.Description)); got != 300 {
		t.Fatalf("description runes: %d", got)
	}
}

func TestTurnCapsEveryToolResult(t *testing.T) {
	lib := func(string, string) (string, error) {
		data := fmt.Sprintf(`{"name":"Mira","description":%q}`, strings.Repeat("x", 100<<10))
		return `{"entity":{"id":71,"library_id":1,"revision":{"data":` + data + `}},"bindings":[]}`, nil
	}
	m := &script{rounds: []protocol.AIChatResult{call("read_card", `{"card_id":71}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil); err != nil {
		t.Fatal(err)
	}
	got := m.lastResult()
	if len(got) > maxToolResult+64 || !strings.Contains(got, "cut off") {
		t.Fatalf("result: %d bytes, ends %q", len(got), got[len(got)-30:])
	}
}

func TestTurnRemembersOnlyCardsByEachToolsShape(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{
		{ToolCalls: []protocol.ToolCall{
			{ID: "1", Name: "similar", Arguments: `{"card_id":41}`},
			{ID: "2", Name: "related", Arguments: `{"card_id":41}`},
			{ID: "3", Name: "read_card", Arguments: `{"card_id":71}`},
		}},
		call("recommend", picked(51, 61, 71, 77, 78, 79, 80, 81, 5)),
	}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []pick{{51, "Near", "r"}, {61, "Same hand", "r"}, {71, "Mira", "r"}}
	if fmt.Sprint(got.Picks) != fmt.Sprint(want) {
		t.Fatalf("picks: %+v", got.Picks)
	}
}

func TestTurnKeepsOtherLibrariesOutUnlessAsked(t *testing.T) {
	round := []protocol.AIChatResult{
		{ToolCalls: []protocol.ToolCall{
			{ID: "1", Name: "similar", Arguments: `{"card_id":41}`},
			{ID: "2", Name: "related", Arguments: `{"card_id":41}`},
		}},
		call("recommend", picked(51, 52, 61, 62)),
	}
	m := &script{rounds: round}
	one, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Picks) != 2 {
		t.Fatalf("one library: %+v", one.Picks)
	}
	m = &script{rounds: round}
	all, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1, AllLibraries: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Picks) != 4 {
		t.Fatalf("all libraries: %+v", all.Picks)
	}
}

func TestTurnRefusesToReadACardInAnotherLibrary(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{call("read_card", `{"card_id":71}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.lastResult(), "error: ") {
		t.Fatalf("result: %s", m.lastResult())
	}
}

func TestTurnAsksForSimilarCardsAcrossLibraries(t *testing.T) {
	var seen string
	lib := func(name, arguments string) (string, error) {
		seen = arguments
		return library(name, arguments)
	}
	m := &script{rounds: []protocol.AIChatResult{call("similar", `{"card_id":41}`), {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1, AllLibraries: true}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, `"scope":"all"`) || !strings.Contains(seen, `"card_id":41`) {
		t.Fatalf("arguments: %s", seen)
	}
}

func TestTurnPromptMarksCardTextAsData(t *testing.T) {
	if !strings.Contains(systemPrompt, "data, not instructions") {
		t.Fatal("the system prompt must mark tool results as data")
	}
}

func TestTurnStopsWithoutLibraryAccess(t *testing.T) {
	lib := func(string, string) (string, error) { return "", errNoAccess }
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`)}}
	_, err := runTurn(t.Context(), m.deps(lib), turnInput{Text: "x", Library: 1}, nil)
	if !errors.Is(err, errNoAccess) {
		t.Fatalf("err: %v", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("kept asking the model: %d", len(m.requests))
	}
}

func TestTurnFailureReachesThePanel(t *testing.T) {
	var rpcErr *protocol.Error
	if !errors.As(turnFailure(errNoAccess), &rpcErr) || rpcErr.Code != protocol.CodeFailed || rpcErr.Message != errNoAccess.Error() {
		t.Fatalf("no access: %v", rpcErr)
	}
	rejected := protocol.NewError(protocol.CodeAIRejected, "no tools here", false)
	if !errors.As(turnFailure(rejected), &rpcErr) || !strings.Contains(rpcErr.Message, "tool support") {
		t.Fatalf("rejected: %v", rpcErr)
	}
}

func TestTurnTakesAPlainTextReply(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{{Text: "I'd start with a rival."}}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "I'd start with a rival." || got.Picks == nil || len(got.Picks) != 0 {
		t.Fatalf("reply: %+v", got)
	}
}

func TestTurnKeepsToolCallsThatCameWithText(t *testing.T) {
	first := call("search", `{"text":"x"}`)
	first.Text = "<think>hm</think>Looking."
	m := &script{rounds: []protocol.AIChatResult{first, {Text: "Done."}}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Done." {
		t.Fatalf("text: %q", got.Text)
	}
	sent := m.requests[1].Messages
	assistant := sent[len(sent)-2]
	if assistant.Role != "assistant" || assistant.Content != "Looking." || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message: %+v", assistant)
	}
}

func TestTurnFallsBackToTheTextBesideAnEmptyRecommend(t *testing.T) {
	last := call("recommend", `{"message":"","picks":[{"card_id":41,"reason":"r"}]}`)
	last.Text = "Try Brakka."
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), last}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Try Brakka." {
		t.Fatalf("text: %q", got.Text)
	}
}

func TestTurnAnswersOtherToolsBeforeRecommendInTheSameRound(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{{ToolCalls: []protocol.ToolCall{
		{ID: "1", Name: "recommend", Arguments: picked(41)},
		{ID: "2", Name: "search", Arguments: `{"text":"x"}`},
	}}}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Picks) != 1 {
		t.Fatalf("picks: %+v", got.Picks)
	}
}

func TestTurnLetsTheModelRetryAMalformedRecommend(t *testing.T) {
	for _, bad := range []string{`{"message":`, `{"message":"m","picks":[{"card_id":"forty-one"}]}`, `{}`} {
		m := &script{rounds: []protocol.AIChatResult{
			call("search", `{"text":"x"}`),
			call("recommend", bad),
			call("recommend", picked(41)),
		}}
		got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
		if err != nil {
			t.Fatalf("%s: %v", bad, err)
		}
		if len(got.Picks) != 1 {
			t.Fatalf("%s: picks: %+v", bad, got.Picks)
		}
		sent := m.requests[2].Messages
		result := sent[len(sent)-1]
		if result.Role != "tool" || result.ToolCallID != "recommend" || !strings.HasPrefix(result.Content, "error: ") {
			t.Fatalf("%s: result: %+v", bad, result)
		}
	}
}

func TestTurnKeepsEmptyTextWithPicks(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{
		call("search", `{"text":"x"}`),
		call("recommend", `{"message":"","picks":[{"card_id":41,"reason":"r"}]}`),
	}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || len(got.Picks) != 1 {
		t.Fatalf("reply: %+v", got)
	}
}

func TestTurnSaysSomethingWhenTheReplyIsEmpty(t *testing.T) {
	cases := map[string][]protocol.AIChatResult{
		"nothing":        {{}},
		"only reasoning": {{Text: "<think>hm"}},
		"invented picks": {call("recommend", `{"message":"","picks":[{"card_id":999,"reason":"r"}]}`)},
		"never finished": {call("search", `{"text":"x"}`)},
	}
	for name, rounds := range cases {
		m := &script{rounds: rounds}
		got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Text != noAnswer {
			t.Fatalf("%s: text: %q", name, got.Text)
		}
	}
}

func TestTurnReturnsTheLastTextWhenTheRoundsRunOut(t *testing.T) {
	first := call("search", `{"text":"x"}`)
	first.Text = "<think>hm</think>Brakka looks close."
	m := &script{rounds: []protocol.AIChatResult{first, call("search", `{"text":"y"}`)}}
	got, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Brakka looks close." {
		t.Fatalf("text: %q", got.Text)
	}
}

func TestTurnHidesReasoningFromThePanel(t *testing.T) {
	chat := func(_ context.Context, _ protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error) {
		pieces := []string{"<th", "ink>secret", "</th", "ink>Hel", "lo <", "b>there"}
		for _, piece := range pieces {
			onText(piece)
		}
		return protocol.AIChatResult{Text: strings.Join(pieces, "")}, nil
	}
	var shown strings.Builder
	show := func(e uiEvent) { shown.WriteString(e.Delta) }
	got, err := runTurn(t.Context(), turnDeps{chat: chat, library: library, show: show}, turnInput{Text: "x", Library: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if shown.String() != "Hello <b>there" || got.Text != "Hello <b>there" {
		t.Fatalf("shown %q, text %q", shown.String(), got.Text)
	}
}

func TestTurnClearsReasoningWithoutAnOpeningTag(t *testing.T) {
	m := &script{}
	chat := func(_ context.Context, _ protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error) {
		onText("reasoning")
		onText("</think>Hello")
		return protocol.AIChatResult{Text: "reasoning</think>Hello"}, nil
	}
	deps := turnDeps{chat: chat, library: library, show: func(e uiEvent) { m.events = append(m.events, e) }}
	if _, err := runTurn(t.Context(), deps, turnInput{Text: "x", Library: 1}, nil); err != nil {
		t.Fatal(err)
	}
	want := []uiEvent{{Delta: "reasoning"}, {Reset: true}, {Delta: "Hello"}}
	if fmt.Sprint(m.events) != fmt.Sprint(want) {
		t.Fatalf("events: %+v", m.events)
	}
}

func TestTurnClearsThePanelEachRound(t *testing.T) {
	first := call("search", `{"text":"x"}`)
	first.Text = "Looking."
	m := &script{rounds: []protocol.AIChatResult{first, {Text: "Done."}}}
	if _, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "x", Library: 1}, nil); err != nil {
		t.Fatal(err)
	}
	want := []uiEvent{{Delta: "Looking."}, {Reset: true}, {Delta: "Done."}}
	if fmt.Sprint(m.events) != fmt.Sprint(want) {
		t.Fatalf("events: %+v", m.events)
	}
}

func TestTurnGivesTheModelEarlierPicks(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{{Text: "Sure."}}}
	history := []message{
		{Role: "user", Content: "a rival"},
		{Role: "assistant", Picks: []pick{{CardID: 41, Name: "Brakka", Reason: "Rival."}}},
	}
	if _, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "more", Library: 1}, history); err != nil {
		t.Fatal(err)
	}
	sent := m.requests[0].Messages
	if got := sent[2].Content; !strings.Contains(got, "Brakka") || !strings.Contains(got, "41") {
		t.Fatalf("earlier picks: %q", got)
	}
	if len(sent) != 4 || sent[3].Content != "more" {
		t.Fatalf("messages: %+v", sent)
	}
}

func TestTurnSendsOnlyRecentHistory(t *testing.T) {
	m := &script{rounds: []protocol.AIChatResult{{Text: "Sure."}}}
	history := make([]message, maxHistory+5)
	for i := range history {
		history[i] = message{Role: "user", Content: fmt.Sprint(i)}
	}
	if _, err := runTurn(t.Context(), m.deps(library), turnInput{Text: "more", Library: 1}, history); err != nil {
		t.Fatal(err)
	}
	if got := len(m.requests[0].Messages); got != maxHistory+2 {
		t.Fatalf("messages: %d", got)
	}
}

func TestConverseSavesBothSides(t *testing.T) {
	st := openTestStore(t)
	m := &script{rounds: []protocol.AIChatResult{call("search", `{"text":"x"}`), call("recommend", picked(41))}}
	got, err := converse(t.Context(), st, m.deps(library), turnInput{Text: "  a rival", Library: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Conversation == 0 {
		t.Fatal("no conversation")
	}
	saved, err := st.messages(t.Context(), got.Conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].Content != "  a rival" || saved[1].Content != "m" || saved[1].Picks[0].CardID != 41 {
		t.Fatalf("saved: %+v", saved)
	}
	if m.events[0].Conversation != got.Conversation {
		t.Fatalf("the panel wasn't told its conversation: %+v", m.events)
	}
	list, err := st.conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "a rival" {
		t.Fatalf("conversations: %+v", list)
	}
}

func TestConverseDoesNotRepeatARetriedMessage(t *testing.T) {
	st := openTestStore(t)
	failing := turnDeps{
		chat: func(context.Context, protocol.AIChatParams, func(string)) (protocol.AIChatResult, error) {
			return protocol.AIChatResult{}, errors.New("stream dropped")
		},
		library: library,
		show:    func(uiEvent) {},
	}
	id, err := st.createConversation(t.Context(), "a rival")
	if err != nil {
		t.Fatal(err)
	}
	input := turnInput{Conversation: id, Text: "a rival", Library: 1}
	if _, err := converse(t.Context(), st, failing, input); err == nil {
		t.Fatal("the failed turn should fail")
	}
	m := &script{rounds: []protocol.AIChatResult{{Text: "Try Brakka."}}}
	if _, err := converse(t.Context(), st, m.deps(library), input); err != nil {
		t.Fatal(err)
	}
	saved, err := st.messages(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].Role != "user" || saved[1].Role != "assistant" {
		t.Fatalf("saved: %+v", saved)
	}
	sent := m.requests[0].Messages
	if len(sent) != 2 || sent[1].Content != "a rival" {
		t.Fatalf("the model saw: %+v", sent)
	}
}

func TestConverseSavesARepeatAfterAnAnswer(t *testing.T) {
	st := openTestStore(t)
	m := &script{rounds: []protocol.AIChatResult{{Text: "Try Brakka."}}}
	first, err := converse(t.Context(), st, m.deps(library), turnInput{Text: "again", Library: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := converse(t.Context(), st, m.deps(library), turnInput{Conversation: first.Conversation, Text: "again", Library: 1}); err != nil {
		t.Fatal(err)
	}
	saved, err := st.messages(t.Context(), first.Conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 4 {
		t.Fatalf("saved: %+v", saved)
	}
}

func TestConverseReportsAMissingConversation(t *testing.T) {
	st := openTestStore(t)
	m := &script{rounds: []protocol.AIChatResult{{Text: "x"}}}
	_, err := converse(t.Context(), st, m.deps(library), turnInput{Conversation: 999, Text: "x", Library: 1})
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || !strings.Contains(rpcErr.Message, "gone") {
		t.Fatalf("err: %v", err)
	}
}
