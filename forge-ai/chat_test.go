package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

var hello = protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "hi"}}}

// finishing is a toolbox that closes on finish, whose calls accept takes.
// Library calls answer "[]".
func finishing(accept func(arguments string) string) toolbox {
	return toolbox{
		tools:   libraryTools,
		call:    func(context.Context, string, string) (string, error) { return "[]", nil },
		closing: &closer{tool: tool("finish", "Finish the run.", map[string]any{}), accept: accept},
	}
}

// names lists the tools one request to the model offered.
func names(req map[string]any) []string {
	var out []string
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		out = append(out, t.(map[string]any)["name"].(string))
	}
	return out
}

func TestClosingToolEndsTheRun(t *testing.T) {
	llm, cfg := withLLM(t, toolCall("call_1", "get_card", `{"id": 7}`), toolCall("call_2", "finish", `{"ok": true}`), streamed("never asked"))
	h := startPlugin(t, cfg)
	var took []string
	res, err := h.srv.complete(t.Context(), h.in, "1.1", hello, finishing(func(args string) string {
		took = append(took, args)
		return ""
	}), noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 2 || !slices.Equal(took, []string{`{"ok": true}`}) || res.ToolCalls[0].Name != "finish" {
		t.Fatalf("%d requests, took %q, result %+v", len(llm.requests), took, res)
	}
	if got := names(llm.requests[0]); len(got) != len(libraryTools)+1 || got[len(got)-1] != "finish" {
		t.Fatalf("the first round offered %v", got)
	}
}

func TestClosingToolIsAllTheLastRoundOffers(t *testing.T) {
	var replies []fakeReply
	for range maxToolRounds {
		replies = append(replies, toolCall("call_1", "get_card", `{"id": 7}`))
	}
	llm, cfg := withLLM(t, replies...)
	h := startPlugin(t, cfg)
	if _, err := h.srv.complete(t.Context(), h.in, "1.1", hello, finishing(func(string) string { return "" }), noProgress); err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != maxToolRounds {
		t.Fatalf("%d requests, want %d", len(llm.requests), maxToolRounds)
	}
	if got := names(llm.requests[maxToolRounds-1]); !slices.Equal(got, []string{"finish"}) {
		t.Fatalf("the last round offered %v", got)
	}
}

func TestClosingToolComplaintGoesBackToTheModel(t *testing.T) {
	llm, cfg := withLLM(t, toolCall("call_1", "finish", `{}`), toolCall("call_2", "finish", `{"ok": true}`))
	h := startPlugin(t, cfg)
	accept := func(args string) string {
		if args == "{}" {
			return "error: say ok"
		}
		return ""
	}
	if _, err := h.srv.complete(t.Context(), h.in, "1.1", hello, finishing(accept), noProgress); err != nil {
		t.Fatal(err)
	}
	msgs := llm.requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["content"] != "error: say ok" || last["tool_call_id"] != "call_1" {
		t.Fatalf("last message %v", last)
	}
}

func TestOtherToolsAnswerBeforeTheClosingTool(t *testing.T) {
	both := fakeReply{calls: []protocol.ToolCall{{ID: "call_1", Name: "finish", Arguments: `{}`}, {ID: "call_2", Name: "get_card", Arguments: `{"id": 7}`}}}
	_, cfg := withLLM(t, both)
	h := startPlugin(t, cfg)
	var order []string
	box := finishing(func(string) string {
		order = append(order, "finish")
		return ""
	})
	box.call = func(_ context.Context, name, _ string) (string, error) {
		order = append(order, name)
		return "[]", nil
	}
	if _, err := h.srv.complete(t.Context(), h.in, "1.1", hello, box, noProgress); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"get_card", "finish"}) {
		t.Fatalf("order %v", order)
	}
}

func TestRefusedToolsFailARunThatNeedsThem(t *testing.T) {
	llm, cfg := withLLM(t, fakeReply{status: 400}, streamed("never asked"))
	h := startPlugin(t, cfg)
	_, err := h.srv.complete(t.Context(), h.in, "1.1", hello, finishing(func(string) string { return "" }), noProgress)
	if !errors.Is(err, errToolsRejected) || len(llm.requests) != 1 {
		t.Fatalf("err %v after %d requests", err, len(llm.requests))
	}
}

func TestReasoningStaysOutOfToolRounds(t *testing.T) {
	thinking := toolCall("call_1", "get_card", `{"id": 7}`)
	thinking.deltas = []string{"<think>hm</think>", " Looking."}
	llm, cfg := withLLM(t, thinking, streamed("done"))
	h := startPlugin(t, cfg)
	box := toolbox{tools: libraryTools, call: func(context.Context, string, string) (string, error) { return "[]", nil }}
	if _, err := h.srv.complete(t.Context(), h.in, "1.1", hello, box, noProgress); err != nil {
		t.Fatal(err)
	}
	msgs := llm.requests[1]["messages"].([]any)
	if sent := msgs[len(msgs)-2].(map[string]any); sent["role"] != "assistant" || sent["content"] != "Looking." {
		t.Fatalf("assistant message %v", sent)
	}
}
