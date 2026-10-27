package main

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

func questions() map[string]protocol.DecideQuestion {
	return map[string]protocol.DecideQuestion{
		"kind":  {Type: "choice", Instructions: "What is it?", Criteria: json.RawMessage(`{"greeting": "An opening line", "lore": "World facts"}`)},
		"voice": {Type: "score", Instructions: "How distinct is the voice?", Criteria: json.RawMessage(`["generic", "some", "distinct"]`)},
		"hook":  {Type: "noul", Instructions: "The greeting gives the user something to answer."},
	}
}

func decideParams() protocol.AIDecideParams {
	return protocol.AIDecideParams{State: json.RawMessage(`"Hi! *She waves from the tavern door.*"`), Questions: questions()}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDecideKindOffersOnlyDecide(t *testing.T) {
	_, res, err := start(t, withLLM(t, &fakeLLM{}, "decide"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Services, []string{protocol.MethodAIDecide}) {
		t.Fatalf("services = %v", res.Services)
	}
}

func TestChatKindAlsoOffersDecide(t *testing.T) {
	_, res, err := start(t, withLLM(t, &fakeLLM{}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Services, []string{protocol.MethodAIChat, protocol.MethodAIDecide}) {
		t.Fatalf("services = %v", res.Services)
	}
}

func TestDecideKindNeedsABaseURL(t *testing.T) {
	_, _, err := start(t, map[string]any{"provider": "llamacpp", "model": "d3", "kinds": []any{"decide"}})
	if err == nil || !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecideCallsSystemOne(t *testing.T) {
	llm := &fakeLLM{decide: `{"model": "d3", "answers": {"hook": {"type": "noul", "noul": 0.8}, "kind": {"type": "choice", "choice": "greeting", "probabilities": {"greeting": 0.9, "lore": 0.1}, "confidence": 0.8}, "voice": {"type": "score", "score": 1.5, "probabilities": {"0": 0, "1": 0.5, "2": 0.5}, "confidence": 0.5}}}`}
	h := started(t, withLLM(t, llm, "decide"))
	var res protocol.AIDecideResult
	if err := h.call(protocol.MethodAIDecide, decideParams(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Model != "d3" || res.Answers["kind"].Choice != "greeting" || *res.Answers["voice"].Score != 1.5 || *res.Answers["hook"].Noul != 0.8 {
		t.Fatalf("result %+v", res)
	}
	req := llm.lastRequest()
	qs, _ := req["questions"].(map[string]any)
	if !strings.HasSuffix(llm.paths[len(llm.paths)-1], "/systemone") || req["model"] != "m" || req["state"] != "Hi! *She waves from the tavern door.*" || len(qs) != 3 {
		t.Fatalf("request %s %v", llm.paths[len(llm.paths)-1], req)
	}
}

func TestDecideRefusesAReplyMissingAnAnswer(t *testing.T) {
	llm := &fakeLLM{decide: `{"model": "d3", "answers": {"hook": {"type": "noul", "noul": 0.8}}}`}
	h := started(t, withLLM(t, llm, "decide"))
	if err := h.call(protocol.MethodAIDecide, decideParams(), nil); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecideServerErrors(t *testing.T) {
	for status, want := range map[int]int{400: protocol.CodeAIRejected, 503: protocol.CodeInternalError} {
		llm := &fakeLLM{status: status}
		h := started(t, withLLM(t, llm, "decide"))
		err := h.call(protocol.MethodAIDecide, decideParams(), nil)
		var rpcErr *protocol.Error
		if rpcCode(err) != want || !strings.Contains(err.Error(), "refused by the fake") {
			t.Errorf("status %d: err = %v, want code %d", status, err, want)
		}
		if ok := errors.As(err, &rpcErr); status == 503 && (!ok || !rpcErr.Retryable()) {
			t.Errorf("status 503 isn't retryable: %v", err)
		}
	}
}

func TestDecideThroughChat(t *testing.T) {
	llm := &fakeLLM{chat: []string{content("Here you go:\n```json\n" +
		`{"kind": {"greeting": 3, "lore": 1}, "voice": {"0": 0, "1": 1, "2": 1}, "hook": 0.7}` + "\n```")}}
	h := started(t, withLLM(t, llm))
	var res protocol.AIDecideResult
	if err := h.call(protocol.MethodAIDecide, decideParams(), &res); err != nil {
		t.Fatal(err)
	}
	kind, voice, hook := res.Answers["kind"], res.Answers["voice"], res.Answers["hook"]
	// Probabilities are normalized; confidence is computed, then halved for a chat model.
	if kind.Choice != "greeting" || !near(kind.Probabilities["greeting"], 0.75) || !near(*kind.Confidence, 0.25) {
		t.Errorf("kind %+v conf %v", kind, *kind.Confidence)
	}
	if !near(*voice.Score, 1.5) || !near(voice.Probabilities["2"], 0.5) || *voice.Confidence >= 0.5 {
		t.Errorf("voice %+v conf %v", voice, *voice.Confidence)
	}
	if !near(*hook.Noul, 0.7) || hook.Confidence != nil {
		t.Errorf("hook %+v", hook)
	}
	if res.Model != "m" {
		t.Errorf("model %q", res.Model)
	}
	prompt := llm.lastRequest()["messages"].([]any)[0].(map[string]any)["content"].(string)
	for _, want := range []string{"kind", "greeting: An opening line", "2: distinct", "The greeting gives the user something to answer.", "She waves"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestDecideThroughChatRetriesAnUnreadableReply(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{chat: []string{content("I think it's a greeting.")}}))
	err := h.call(protocol.MethodAIDecide, decideParams(), nil)
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || !rpcErr.Retryable() {
		t.Fatalf("err = %v, want a retryable error", err)
	}
}

func TestDecideNeedsQuestions(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{}, "decide"))
	if err := h.call(protocol.MethodAIDecide, protocol.AIDecideParams{State: json.RawMessage(`"x"`)}, nil); rpcCode(err) != protocol.CodeInvalidParams {
		t.Fatalf("err = %v", err)
	}
}

func TestTestConnectionChecksDecide(t *testing.T) {
	llm := &fakeLLM{}
	var res protocol.SettingsRunResult
	h := started(t, map[string]any{})
	if err := h.call(protocol.MethodSettingsRun, protocol.SettingsRunParams{Action: "test",
		SettingsDraft: protocol.SettingsDraft{Config: withLLM(t, llm, "decide")}}, &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Checks) != 1 || res.Checks[0].Name != "Decisions" {
		t.Fatalf("run = %+v", res)
	}
}
