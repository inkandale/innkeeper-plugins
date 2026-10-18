package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// fakeLLM stands in for an ai instance: the fake host answers each ai.chat
// for its connection with the next reply. requests holds each call's params
// as a JSON object.
type fakeLLM struct {
	mu        sync.Mutex
	replies   []fakeReply
	requests  []map[string]any
	onRequest func()
}

type fakeReply struct {
	status int      // nonzero: refuse with -32010, as a model without tool support does
	deltas []string // the reply's text, streamed piece by piece
	calls  []protocol.ToolCall
}

// streamed splits text into small pieces, as a model streams it.
func streamed(text string) fakeReply {
	var r fakeReply
	for len(text) > 0 {
		n := min(7, len(text))
		r.deltas = append(r.deltas, text[:n])
		text = text[n:]
	}
	return r
}

func toolCall(id, name, args string) fakeReply {
	return fakeReply{calls: []protocol.ToolCall{{ID: id, Name: name, Arguments: args}}}
}

// llms finds a test's fakeLLM by the connection ID in its config.
var (
	llms   sync.Map
	llmIDs atomic.Int64
)

// answer replies to one ai.chat, streaming its text to conn first.
func (f *fakeLLM) answer(conn *protocol.Conn, raw json.RawMessage) (any, error) {
	var body map[string]any
	var p protocol.AIChatParams
	if err := json.Unmarshal(raw, &body); err != nil || json.Unmarshal(raw, &p) != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "bad ai.chat", false)
	}
	f.mu.Lock()
	f.requests = append(f.requests, body)
	hook := f.onRequest
	if len(f.replies) == 0 {
		f.mu.Unlock()
		return nil, protocol.NewError(-32000, "no reply scripted", false)
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if reply.status != 0 {
		return nil, protocol.NewError(protocol.CodeAIRejected, "this model does not support tools", false)
	}
	var text strings.Builder
	for _, d := range reply.deltas {
		_ = conn.Notify(protocol.NotifyAIDelta, protocol.AIDelta{Run: p.Run, Text: d})
		text.WriteString(d)
	}
	return protocol.AIChatResult{Text: text.String(), ToolCalls: reply.calls, Model: "m"}, nil
}

// fakeDecider stands in for an ai instance offering ai.decide. It gives
// every score question the level and confidence in its maps, by question ID,
// or fails with err.
type fakeDecider struct {
	mu         sync.Mutex
	level      map[string]int
	confidence map[string]float64
	err        error
	requests   []protocol.AIDecideParams
}

var deciders sync.Map

func (f *fakeDecider) answer(p protocol.AIDecideParams) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, p)
	if f.err != nil {
		return nil, f.err
	}
	res := protocol.AIDecideResult{Answers: map[string]protocol.DecideAnswer{}, Model: "d3"}
	for id := range p.Questions {
		score, conf := float64(f.level[id]), f.confidence[id]
		res.Answers[id] = protocol.DecideAnswer{Type: "score", Score: &score, Confidence: &conf}
	}
	return res, nil
}

// withDecider adds a decide connection to cfg, answered by f.
func withDecider(t *testing.T, cfg map[string]any, f *fakeDecider) map[string]any {
	id := llmIDs.Add(1)
	deciders.Store(id, f)
	t.Cleanup(func() { deciders.Delete(id) })
	cfg["decide"] = id
	return cfg
}

// host is a fake Innkeeper core: it talks to the plugin's handler over pipes,
// records progress, and answers library.call.
type host struct {
	conn     *protocol.Conn
	mu       sync.Mutex
	progress []protocol.Progress
	calls    []protocol.LibraryCallParams
	library  func(protocol.LibraryCallParams) (any, error)
	srv      *server
}

func startPlugin(t *testing.T, config map[string]any) *host {
	t.Helper()
	return startWith(t, protocol.InitializeParams{Protocol: 1, Config: config})
}

func startWith(t *testing.T, params protocol.InitializeParams) *host {
	t.Helper()
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	srv := &server{}
	srv.conn = protocol.NewConn(toPlugin, fromPlugin, srv.handle)
	h := &host{}
	h.srv = srv
	h.conn = protocol.NewConn(toHost, fromHost, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == protocol.MethodAIDecide {
			var call protocol.AIDecideParams
			_ = json.Unmarshal(params, &call)
			d, ok := deciders.Load(call.Instance)
			if !ok {
				return nil, protocol.NewError(protocol.CodeForbidden, "no such connection", false)
			}
			return d.(*fakeDecider).answer(call)
		}
		if method == protocol.MethodAIChat {
			var call protocol.ServiceCall
			_ = json.Unmarshal(params, &call)
			f, ok := llms.Load(call.Instance)
			if !ok {
				return nil, protocol.NewError(protocol.CodeForbidden, "no such connection", false)
			}
			return f.(*fakeLLM).answer(h.conn, params)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		switch method {
		case protocol.NotifyProgress:
			var p protocol.Progress
			_ = json.Unmarshal(params, &p)
			h.progress = append(h.progress, p)
			return nil, nil
		case protocol.MethodLibraryCall:
			var p protocol.LibraryCallParams
			_ = json.Unmarshal(params, &p)
			h.calls = append(h.calls, p)
			if h.library == nil {
				return nil, protocol.NewError(protocol.CodeForbidden, "library access is off", false)
			}
			return h.library(p)
		}
		return nil, protocol.MethodNotFound(method)
	})
	ctx, cancel := context.WithCancel(context.Background())
	go srv.conn.Run(ctx)
	go h.conn.Run(ctx)
	t.Cleanup(func() {
		cancel()
		fromHost.Close()
		fromPlugin.Close()
	})
	var res protocol.InitializeResult
	if err := h.conn.Call(ctx, protocol.MethodInitialize, params, &res); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *host) run(t *testing.T, p protocol.ActionRunParams) (protocol.ActionRunResult, error) {
	t.Helper()
	var res protocol.ActionRunResult
	err := h.conn.Call(context.Background(), protocol.MethodActionRun, p, &res)
	return res, err
}

func withLLM(t *testing.T, replies ...fakeReply) (*fakeLLM, map[string]any) {
	f := &fakeLLM{replies: replies}
	id := llmIDs.Add(1)
	llms.Store(id, f)
	t.Cleanup(func() { llms.Delete(id) })
	return f, map[string]any{"connection": id, "prompts": map[string]any{"house_style": "No em dashes."}}
}

const generated = `<think>An orc courier, I think.</think>{"name": "Ash", "description": "A courier.", "personality": "Wry.",
"scenario": "A dock.", "first_mes": "Hi.", "mes_example": "<START>", "tags": ["orc"], "system_prompt": "Stay in character.",
"post_history_instructions": "Short."}`

func TestRunStreamsProgressPerField(t *testing.T) {
	llm, cfg := withLLM(t, streamed(generated))
	h := startPlugin(t, cfg)
	res, err := h.run(t, sampleRun(t, "generate"))
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(res.Proposal, &card); err != nil || card["name"] != "Ash" || len(card) != 9 {
		t.Fatalf("proposal %s: %v", res.Proposal, err)
	}
	if res.Raw != generated {
		t.Fatalf("raw = %q", res.Raw)
	}
	var messages []string
	for _, p := range h.progress {
		if p.Run != "1.1" || p.Total != 9 {
			t.Fatalf("progress %+v", p)
		}
		messages = append(messages, p.Message)
	}
	if len(messages) != 9 || messages[0] != "Writing Name (1 of 9)" || messages[2] != "Writing Personality (3 of 9)" ||
		h.progress[2].Done != 2 {
		t.Fatalf("progress %q", messages)
	}
	req := llm.requests[0]
	if run, _ := req["run"].(string); !strings.HasPrefix(run, "1.1/") {
		t.Fatalf("request %v: want a streaming token under the run's", req)
	}
	system := req["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "No em dashes.") {
		t.Fatalf("house style not sent: %q", system)
	}
}

func TestLibraryToolCallAnsweredByHost(t *testing.T) {
	llm, cfg := withLLM(t,
		toolCall("call_1", "get_card", `{"id": 7}`),
		streamed(`{"name": "Brakka", "description": "Billy's brother."}`))
	h := startPlugin(t, cfg)
	h.library = func(p protocol.LibraryCallParams) (any, error) {
		return protocol.MCPCallResult{Content: []protocol.MCPContent{{Type: "text", Text: `{"name": "Billy", "description": "A goblin."}`}}}, nil
	}
	res, err := h.run(t, sampleRun(t, "revise"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Proposal), "Billy's brother.") {
		t.Fatalf("proposal %s", res.Proposal)
	}
	if len(h.calls) != 1 || h.calls[0].Run != "1.1" || h.calls[0].Name != "get_card" || string(h.calls[0].Arguments) != `{"id":7}` {
		t.Fatalf("library calls %+v", h.calls)
	}
	if tools, _ := llm.requests[0]["tools"].([]any); len(tools) != 4 {
		t.Fatalf("first request offered tools %v", llm.requests[0]["tools"])
	}
	msgs := llm.requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" || !strings.Contains(last["content"].(string), "A goblin.") {
		t.Fatalf("tool result message %v", last)
	}
	asked := msgs[len(msgs)-2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if asked["arguments"] != `{"id": 7}` {
		t.Fatalf("assistant tool call %v", asked)
	}
}

func TestToolCallExtraGoesBackToTheModel(t *testing.T) {
	signed := toolCall("call_1", "get_card", `{"id": 7}`)
	signed.calls[0].Extra = json.RawMessage(`{"gemini":{"thought_signature":"c2ln"}}`)
	llm, cfg := withLLM(t, signed, streamed(`{"description": "x"}`))
	h := startPlugin(t, cfg)
	h.library = func(protocol.LibraryCallParams) (any, error) { return protocol.MCPCallResult{}, nil }
	if _, err := h.run(t, sampleRun(t, "revise")); err != nil {
		t.Fatal(err)
	}
	msgs := llm.requests[1]["messages"].([]any)
	asked := msgs[len(msgs)-2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if extra, _ := json.Marshal(asked["extra"]); string(extra) != `{"gemini":{"thought_signature":"c2ln"}}` {
		t.Fatalf("assistant tool call %v", asked)
	}
}

func TestLibraryRefusalReachesTheModel(t *testing.T) {
	llm, cfg := withLLM(t,
		toolCall("call_1", "search_cards", `{"query": {"text": "Billy"}}`),
		streamed(`{"description": "Alone."}`))
	h := startPlugin(t, cfg)
	if _, err := h.run(t, sampleRun(t, "revise")); err != nil {
		t.Fatal(err)
	}
	msgs := llm.requests[1]["messages"].([]any)
	if got := msgs[len(msgs)-1].(map[string]any)["content"].(string); !strings.Contains(got, "library access is off") {
		t.Fatalf("tool result %q", got)
	}
}

func TestNoToolsWithoutLibrary(t *testing.T) {
	llm, cfg := withLLM(t, streamed(`{"description": "A towering orc."}`))
	h := startPlugin(t, cfg)
	if _, err := h.run(t, sampleRun(t, "tighten")); err != nil {
		t.Fatal(err)
	}
	if _, ok := llm.requests[0]["tools"]; ok {
		t.Fatalf("tighten offered tools: %v", llm.requests[0]["tools"])
	}
}

func TestRetriesWithoutToolsWhenRejected(t *testing.T) {
	llm, cfg := withLLM(t, fakeReply{status: 400}, streamed(`{"description": "Billy's brother."}`))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "revise")
	p.Context.Cards = []protocol.ContextItem{{ID: 7, Name: "Billy", Data: json.RawMessage(`{"name": "Billy"}`)}}
	if _, err := h.run(t, p); err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 2 || llm.requests[1]["tools"] != nil {
		t.Fatalf("requests %v", llm.requests)
	}
	msgs := llm.requests[1]["messages"].([]any)
	if user := msgs[1].(map[string]any)["content"].(string); !strings.Contains(user, "Card: Billy") {
		t.Fatalf("explicit context dropped: %q", user)
	}
}

func TestModelErrorFailsTheRun(t *testing.T) {
	_, cfg := withLLM(t, fakeReply{status: 400})
	h := startPlugin(t, cfg)
	if _, err := h.run(t, sampleRun(t, "tighten")); err == nil {
		t.Fatal("a rejected request succeeded")
	}
}

func TestRunWithoutSettingsFails(t *testing.T) {
	h := startPlugin(t, map[string]any{})
	_, err := h.run(t, sampleRun(t, "tighten"))
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code == -32601 || !strings.Contains(rpc.Message, "connection") {
		t.Fatalf("err = %v, want a settings error", err)
	}
}

func TestSettingsDefaultsWithEmptyConfig(t *testing.T) {
	h := startPlugin(t, map[string]any{})
	var res protocol.SettingsDefaultsResult
	if err := h.conn.Call(context.Background(), protocol.MethodSettingsDefaults, nil, &res); err != nil {
		t.Fatal(err)
	}
	if text, _ := res.Defaults["/prompts/tighten"].(string); !strings.Contains(text, "Tighten it") {
		t.Fatalf("defaults %v", res.Defaults)
	}
}

func TestActionList(t *testing.T) {
	h := startPlugin(t, map[string]any{})
	var res protocol.ActionListResult
	if err := h.conn.Call(context.Background(), protocol.MethodActionList, nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != len(actions) || res.Actions[0].ID != "generate" {
		t.Fatalf("actions %+v", res.Actions)
	}
}

func TestReferenceImageSent(t *testing.T) {
	llm, cfg := withLLM(t, streamed(generated))
	h := startPlugin(t, cfg)
	img := t.TempDir() + "/ref.png"
	// A PNG signature is enough for content sniffing.
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\n0000"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := sampleRun(t, "generate")
	p.Images.Reference = img
	if _, err := h.run(t, p); err != nil {
		t.Fatal(err)
	}
	parts := llm.requests[0]["messages"].([]any)[1].(map[string]any)["parts"].([]any)
	url := parts[1].(map[string]any)["image_url"].(string)
	if len(parts) != 2 || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("user content %v", parts)
	}
	if text := parts[0].(map[string]any)["text"].(string); !strings.Contains(text, "image of the character is attached") {
		t.Fatalf("user text %q", text)
	}
}

func TestFinalRoundOffersNoTools(t *testing.T) {
	var replies []fakeReply
	for range maxToolRounds - 1 {
		replies = append(replies, toolCall("call_1", "get_card", `{"id": 7}`))
	}
	llm, cfg := withLLM(t, append(replies, streamed(`{"description": "Done."}`))...)
	h := startPlugin(t, cfg)
	if _, err := h.run(t, sampleRun(t, "revise")); err != nil {
		t.Fatal(err)
	}
	if n := len(llm.requests); n != maxToolRounds {
		t.Fatalf("%d requests, want %d", n, maxToolRounds)
	}
	if _, ok := llm.requests[maxToolRounds-2]["tools"]; !ok {
		t.Fatal("the round before the last offered no tools")
	}
	if _, ok := llm.requests[maxToolRounds-1]["tools"]; ok {
		t.Fatal("the final round offered tools")
	}
}

func TestNoRetryWithoutToolsAfterToolResults(t *testing.T) {
	llm, cfg := withLLM(t, toolCall("call_1", "get_card", `{"id": 7}`), fakeReply{status: 400})
	h := startPlugin(t, cfg)
	if _, err := h.run(t, sampleRun(t, "revise")); err == nil {
		t.Fatal("a rejected second round succeeded")
	}
	if len(llm.requests) != 2 {
		t.Fatalf("%d requests, want 2", len(llm.requests))
	}
}

func TestEndedRunStopsTheToolLoop(t *testing.T) {
	llm, cfg := withLLM(t, toolCall("call_1", "get_card", `{"id": 7}`), streamed(`{"description": "x"}`))
	h := startPlugin(t, cfg)
	h.library = func(protocol.LibraryCallParams) (any, error) {
		return nil, protocol.NewError(-32602, "run 1.1 has finished", false)
	}
	if _, err := h.run(t, sampleRun(t, "revise")); err == nil {
		t.Fatal("the run went on after its token ended")
	}
	if len(llm.requests) != 1 {
		t.Fatalf("%d requests after the run ended, want 1", len(llm.requests))
	}
}

func TestCancelStopsTheToolLoop(t *testing.T) {
	llm, cfg := withLLM(t, toolCall("call_1", "get_card", `{"id": 7}`), streamed(`{"description": "x"}`))
	h := startPlugin(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	lib := func(context.Context, string, string) (string, error) {
		cancel() // The owner pressed Stop while the tool ran.
		return "{}", nil
	}
	params := protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "hi"}}}
	noProgress := func() func(string) { return func(string) {} }
	if _, err := h.srv.complete(ctx, h.srv.cur.Load(), "1.1", params, toolbox{tools: libraryTools, call: lib}, noProgress); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("%d requests after cancel, want 1", len(llm.requests))
	}
}

func TestProgressRestartsEachRound(t *testing.T) {
	_, cfg := withLLM(t,
		fakeReply{deltas: []string{`{"description": "draft"`}, calls: []protocol.ToolCall{{ID: "call_1", Name: "get_card", Arguments: `{"id": 7}`}}},
		streamed(`{"bogus": 1, "description": "Billy's brother."}`))
	h := startPlugin(t, cfg)
	h.library = func(protocol.LibraryCallParams) (any, error) { return protocol.MCPCallResult{}, nil }
	if _, err := h.run(t, sampleRun(t, "revise")); err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, p := range h.progress {
		if p.Done >= p.Total {
			t.Fatalf("progress %+v: done must stay below total", p)
		}
		messages = append(messages, p.Message)
	}
	if want := "Writing Description (1 of 1)"; len(messages) != 2 || messages[0] != want || messages[1] != want {
		t.Fatalf("progress %q", messages)
	}
}

func TestLoreEntriesRunProgressPerEntry(t *testing.T) {
	out := `{"entries": [{"comment": "The pass", "keys": ["Iron Pass"], "content": "A narrow road."},
		{"keys": ["Billy"], "content": "Brakka's brother."}, {"keys": ["Snow"], "content": "Always."}]}`
	llm, cfg := withLLM(t, streamed(out))
	h := startPlugin(t, cfg)
	p := sampleRun(t, "lore-entries")
	p.Inputs = json.RawMessage(`{"count": 2}`)
	res, err := h.run(t, p)
	if err != nil {
		t.Fatal(err)
	}
	var book struct{ Entries []map[string]any }
	if err := json.Unmarshal(res.Proposal, &book); err != nil || len(book.Entries) != 4 || book.Entries[0]["id"] != 1.0 {
		t.Fatalf("proposal %s: %v", res.Proposal, err)
	}
	var messages []string
	for _, pr := range h.progress {
		if pr.Total != 2 || pr.Done >= pr.Total {
			t.Fatalf("progress %+v", pr)
		}
		messages = append(messages, pr.Message)
	}
	if len(messages) != 2 || messages[0] != "Writing entry 1 of 2" || messages[1] != "Writing entry 2 of 2" {
		t.Fatalf("progress %q", messages)
	}
	if tools, _ := llm.requests[0]["tools"].([]any); len(tools) != 4 {
		t.Fatalf("lore-entries offered tools %v", llm.requests[0]["tools"])
	}
}
