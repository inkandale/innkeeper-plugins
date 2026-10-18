package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/config"
	llmerrors "github.com/mozilla-ai/any-llm-go/errors"
)

func toolCall(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}})
	return string(b)
}

func argsDelta(args string) string {
	b, _ := json.Marshal(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": args}}}})
	return string(b)
}

func TestChatStreamsDeltasUnderTheRunToken(t *testing.T) {
	llm := &fakeLLM{chat: []string{content("Hel"), content("lo")}}
	h := started(t, withLLM(t, llm))
	temperature := 0.3
	var res protocol.AIChatResult
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{ServiceCall: protocol.ServiceCall{Run: "r1"},
		Messages: []protocol.ChatMessage{{Role: "user", Content: "Say hello."}}, Temperature: &temperature}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || len(res.ToolCalls) != 0 || res.Model != "m" {
		t.Fatalf("result %+v", res)
	}
	want := []protocol.AIDelta{{Run: "r1", Text: "Hel"}, {Run: "r1", Text: "lo"}}
	if fmt.Sprint(h.deltas) != fmt.Sprint(want) {
		t.Fatalf("deltas %+v, want %+v", h.deltas, want)
	}
	req := llm.lastRequest()
	if req["model"] != "m" || req["stream"] != true || req["temperature"] != 0.3 {
		t.Fatalf("request %v", req)
	}
}

func TestChatWithoutARunSendsNoDeltas(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{chat: []string{content("Hi")}}))
	var res protocol.AIChatResult
	if err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hi" || len(h.deltas) != 0 {
		t.Fatalf("result %+v, deltas %+v", res, h.deltas)
	}
}

func TestChatReturnsWholeToolCalls(t *testing.T) {
	llm := &fakeLLM{chat: []string{toolCall("x", "get_card", ""), argsDelta(`{"id"`), argsDelta(`: 1}`)}}
	h := started(t, withLLM(t, llm))
	var res protocol.AIChatResult
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{
		Messages: []protocol.ChatMessage{
			{Role: "user", Content: "Look up card 7."},
			{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: "a", Name: "get_card", Arguments: `{"id": 7}`}}},
			{Role: "tool", ToolCallID: "a", Content: "Billy"},
		},
		Tools: []protocol.ChatTool{{Name: "get_card", Description: "Get a card.", Parameters: json.RawMessage(`{"type": "object"}`)}},
	}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolCalls) != 1 || !reflect.DeepEqual(res.ToolCalls[0], protocol.ToolCall{ID: "x", Name: "get_card", Arguments: `{"id": 1}`}) {
		t.Fatalf("tool calls %+v", res.ToolCalls)
	}
	req := llm.lastRequest()
	msgs := req["messages"].([]any)
	call := msgs[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["id"] != "a" || msgs[2].(map[string]any)["tool_call_id"] != "a" {
		t.Fatalf("messages %v", msgs)
	}
	tool := req["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if tool["name"] != "get_card" || tool["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("tools %v", req["tools"])
	}
}

func TestChatSendsImageParts(t *testing.T) {
	llm := &fakeLLM{chat: []string{content("A cat.")}}
	h := started(t, withLLM(t, llm))
	var res protocol.AIChatResult
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Parts: []protocol.ContentPart{
		{Type: "text", Text: "What is this?"}, {Type: "image_url", ImageURL: "data:image/png;base64,iVBORw0KGgo="}}}}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	parts := llm.lastRequest()["messages"].([]any)[0].(map[string]any)["content"].([]any)
	image := parts[1].(map[string]any)
	if len(parts) != 2 || image["image_url"].(map[string]any)["url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("content %v", parts)
	}
}

func TestChatRefusalIsAIRejected(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{status: 400}))
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, nil)
	if rpcCode(err) != protocol.CodeAIRejected {
		t.Fatalf("err = %v, want code %d", err, protocol.CodeAIRejected)
	}
}

func TestChatContextLengthAndContentFilterAreAIRejected(t *testing.T) {
	for _, code := range []string{"context_length_exceeded", "content_filter"} {
		h := started(t, withLLM(t, &fakeLLM{status: 400, code: code}))
		err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, nil)
		if rpcCode(err) != protocol.CodeAIRejected {
			t.Errorf("%s: err = %v, want code %d", code, err, protocol.CodeAIRejected)
		}
	}
}

// wantRetryable fails unless err is a retryable internal error.
func wantRetryable(t *testing.T, name string, err error) {
	t.Helper()
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeInternalError || !rpcErr.Retryable() {
		t.Errorf("%s: err = %v, want a retryable %d", name, err, protocol.CodeInternalError)
	}
}

// streamError is modelError of one chat request to provider, built with opts.
func streamError(t *testing.T, provider string, opts ...config.Option) error {
	t.Helper()
	llm, err := providers[provider](opts...)
	if err != nil {
		t.Fatal(err)
	}
	c := &connection{settings: settings{Provider: provider, Model: "m"}, llm: llm}
	_, _, err = stream(context.Background(), c, anyllm.CompletionParams{Model: "m", MaxTokens: new(int),
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "Hi"}}}, func(string) {})
	if err == nil {
		t.Fatalf("%s: no error", provider)
	}
	return modelError(err)
}

func TestChatOverloadIsRetryable(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{status: 503}))
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, nil)
	wantRetryable(t, "503", err)

	ts := httptest.NewServer(&fakeLLM{status: 529})
	t.Cleanup(ts.Close)
	wantRetryable(t, "anthropic 529", streamError(t, "anthropic", config.WithBaseURL(ts.URL), config.WithAPIKey("k")))
}

func TestClientTimeoutIsRetryable(t *testing.T) {
	stall := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-stall }))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(stall) })
	wantRetryable(t, "client timeout", streamError(t, "openai-compatible", config.WithBaseURL(ts.URL), config.WithTimeout(50*time.Millisecond)))
}

func TestChatNotOfferedIsMethodNotFound(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{}, "embed"))
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, nil)
	if rpcCode(err) != protocol.CodeMethodNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestChatNeedsMessages(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{}))
	if err := h.call(protocol.MethodAIChat, protocol.AIChatParams{}, nil); rpcCode(err) != protocol.CodeInvalidParams {
		t.Fatalf("err = %v", err)
	}
}

func TestRateLimitIsRetryable(t *testing.T) {
	var rpcErr *protocol.Error
	if !errors.As(modelError(fmt.Errorf("openai: %w", llmerrors.ErrRateLimit)), &rpcErr) || !rpcErr.Retryable() {
		t.Fatalf("rate limit: %v", rpcErr)
	}
}

func TestMergeToolCallsAcrossProviders(t *testing.T) {
	call := func(id, name, args string) anyllm.ToolCall {
		return anyllm.ToolCall{ID: id, Function: anyllm.FunctionCall{Name: name, Arguments: args}}
	}
	merged := func(provider string, chunks ...anyllm.ToolCall) []anyllm.ToolCall {
		var calls []anyllm.ToolCall
		for _, d := range chunks {
			calls = mergeToolCalls(provider, calls, []anyllm.ToolCall{d})
		}
		return calls
	}
	// Anthropic resends each call whole under its ID.
	calls := merged("anthropic", call("a", "get_card", `{"id"`), call("a", "get_card", `{"id": 1}`), call("b", "get_card", `{"id": 2}`))
	if len(calls) != 2 || calls[0].Function.Arguments != `{"id": 1}` || calls[1].ID != "b" || calls[0].Type != "function" {
		t.Fatalf("anthropic: calls %+v", calls)
	}
	// any-llm-go numbers Ollama's calls within each chunk, so IDs repeat across calls.
	calls = merged("ollama", call("call_0", "get_card", `{"id": 1}`), call("call_0", "get_card", `{"id": 2}`))
	if len(calls) != 2 || calls[1].Function.Arguments != `{"id": 2}` {
		t.Fatalf("ollama: calls %+v", calls)
	}
	// OpenAI-compatible fragments: the ID on the first only, or on every one.
	for _, chunks := range [][]anyllm.ToolCall{
		{call("x", "get_card", ""), call("", "", `{"id"`), call("", "", `: 1}`), call("y", "get_lorebook", `{"id": 2}`)},
		{call("x", "get_card", ""), call("x", "", `{"id"`), call("x", "", `: 1}`), call("y", "get_lorebook", `{"id": 2}`)},
	} {
		calls = merged("openai-compatible", chunks...)
		if len(calls) != 2 || calls[0].Function.Arguments != `{"id": 1}` || calls[1].Function.Name != "get_lorebook" {
			t.Fatalf("openai-compatible: calls %+v", calls)
		}
	}
}

// fakeGemini answers Gemini's streaming API with one tool call signed "sig"
// and records each request body.
func fakeGemini(t *testing.T) *[]map[string]any {
	t.Helper()
	var requests []map[string]any
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"candidates": [{"content": {"role": "model", "parts": [{"functionCall": {"name": "get_card", "args": {"id": 1}}, "thoughtSignature": "c2ln"}]}, "finishReason": "STOP"}]}`+"\n\n")
	}))
	t.Cleanup(ts.Close)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", ts.URL)
	t.Setenv("GEMINI_API_KEY", "k")
	return &requests
}

func TestChatReturnsAToolCallsProviderExtra(t *testing.T) {
	fakeGemini(t)
	h := started(t, map[string]any{"provider": "gemini", "model": "m"})
	var res protocol.AIChatResult
	if err := h.call(protocol.MethodAIChat, protocol.AIChatParams{
		Messages: []protocol.ChatMessage{{Role: "user", Content: "Look up card 1."}}}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ToolCalls) != 1 || string(res.ToolCalls[0].Extra) != `{"gemini":{"thought_signature":"c2ln"}}` {
		t.Fatalf("tool calls %+v", res.ToolCalls)
	}
}

func TestChatSendsAnAssistantToolCallsExtraToTheProvider(t *testing.T) {
	requests := fakeGemini(t)
	h := started(t, map[string]any{"provider": "gemini", "model": "m"})
	var res protocol.AIChatResult
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{
		{Role: "user", Content: "Look up card 7."},
		{Role: "assistant", ToolCalls: []protocol.ToolCall{{ID: "a", Name: "get_card", Arguments: `{"id": 7}`,
			Extra: json.RawMessage(`{"gemini": {"thought_signature": "bXlzaWc="}}`)}}},
		{Role: "tool", ToolCallID: "a", Content: "Billy"},
	}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	model := (*requests)[0]["contents"].([]any)[1].(map[string]any)
	part := model["parts"].([]any)[0].(map[string]any)
	if part["thoughtSignature"] != "bXlzaWc=" {
		t.Fatalf("model turn %v", model)
	}
}

// A 4xx the provider didn't classify, such as 422, won't succeed on retry.
func TestChatUnclassifiedClientErrorIsAIRejected(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{status: 422}))
	err := h.call(protocol.MethodAIChat, protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Hi"}}}, nil)
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeAIRejected || rpcErr.Retryable() {
		t.Fatalf("err = %v, want a final %d", err, protocol.CodeAIRejected)
	}
}
