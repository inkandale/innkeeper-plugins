package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

const evaluated = `{"summary": "Solid.", "overall_score": 82, "card_type": "character", "dimensions": {
"consistency": {"score": 90, "comment": "ok"}, "voice": {"score": 120, "comment": "ok"},
"first_impression": {"score": -5, "comment": "ok"}, "roleplayability": {"score": 70, "comment": "ok"},
"token_efficiency": {"score": 60.5, "comment": "ok"}, "field_governance": {"score": 50, "comment": "ok"}},
"contradictions": [], "misplaced_content": [], "suggestions": []}`

func enrichParams(names ...string) protocol.EnrichRunParams {
	p := protocol.EnrichRunParams{Run: "R1"}
	for i, name := range names {
		data, _ := json.Marshal(map[string]string{"name": name, "description": "A towering orc."})
		p.Revisions = append(p.Revisions, protocol.EnrichRevision{CardID: int64(i + 1), RevisionID: int64(100 + i), Data: data})
	}
	return p
}

func (h *host) enrich(t *testing.T, p protocol.EnrichRunParams) (protocol.EnrichRunResult, error) {
	t.Helper()
	var res protocol.EnrichRunResult
	err := h.conn.Call(context.Background(), protocol.MethodEnrichRun, p, &res)
	return res, err
}

func TestEnrichMapsEvaluationToDeclaredKeys(t *testing.T) {
	_, cfg := withLLM(t, streamed(evaluated))
	h := startPlugin(t, cfg)
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].RevisionID != 100 || res.Results[0].Error != "" {
		t.Fatalf("results %+v", res.Results)
	}
	got := map[string]float64{}
	for k, v := range res.Results[0].Values {
		var n float64
		if err := json.Unmarshal(v, &n); err != nil {
			t.Fatalf("%s = %s: %v", k, v, err)
		}
		got[k] = n
	}
	want := map[string]float64{"quality": 82, "consistency": 90, "voice": 100, "first_impression": 0,
		"roleplayability": 70, "token_efficiency": 60.5, "field_governance": 50}
	if !maps.Equal(got, want) {
		t.Fatalf("values %v, want %v (out-of-range scores clamped)", got, want)
	}
	m, err := protocol.LoadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range res.Results[0].Values {
		if err := m.Enrichments[k].Check(v); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	var meta map[string]string
	if err := json.Unmarshal(res.Results[0].Meta, &meta); err != nil || meta["model"] != "m" || !strings.HasPrefix(meta["prompt"], "evaluate-") {
		t.Fatalf("meta %s: %v", res.Results[0].Meta, err)
	}
	if len(h.progress) != 1 || h.progress[0].Run != "R1" || h.progress[0].Message != "Evaluating Brakka (1 of 1)" || h.progress[0].Total != 1 {
		t.Fatalf("progress %+v", h.progress)
	}
}

func TestEnrichFailedRevisionKeepsTheBatch(t *testing.T) {
	llm, cfg := withLLM(t, fakeReply{status: 400}, streamed("I can't score this."), streamed(evaluated))
	h := startPlugin(t, cfg)
	res, err := h.enrich(t, enrichParams("Brakka", "Mira", "Ash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 3 || len(llm.requests) != 3 {
		t.Fatalf("results %+v after %d requests", res.Results, len(llm.requests))
	}
	for i, r := range res.Results[:2] {
		if r.Error == "" || r.Values != nil || r.Meta != nil {
			t.Errorf("revision %d: %+v, want only an error", r.RevisionID, res.Results[i])
		}
	}
	if r := res.Results[2]; r.RevisionID != 102 || r.Error != "" || len(r.Values) != 7 {
		t.Fatalf("third revision %+v", r)
	}
	var messages []string
	for _, p := range h.progress {
		messages = append(messages, p.Message)
	}
	if !slices.Equal(messages, []string{"Evaluating Brakka (1 of 3)", "Evaluating Mira (2 of 3)", "Evaluating Ash (3 of 3)"}) {
		t.Fatalf("progress %q", messages)
	}
}

func TestEnrichWithoutSettingsFailsTheCall(t *testing.T) {
	h := startPlugin(t, map[string]any{})
	_, err := h.enrich(t, enrichParams("Brakka"))
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code == -32601 || !strings.Contains(rpc.Message, "connection") {
		t.Fatalf("err = %v, want a settings error", err)
	}
}

func TestEnrichCancelStopsFurtherCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	llm, cfg := withLLM(t, streamed(evaluated), streamed(evaluated))
	llm.onRequest = cancel // The owner pressed Stop during the first call.
	h := startPlugin(t, cfg)
	params, _ := json.Marshal(enrichParams("Brakka", "Mira"))
	if _, err := h.srv.enrich(ctx, h.srv.cur.Load(), params); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.requests) > 1 {
		t.Fatalf("%d requests after cancel, want at most 1", len(llm.requests))
	}
}

// The manifest declares quality and one key per evaluate dimension, labeled
// as the report labels it, all scored 0 to 100 as the clamp assumes.
func TestEnrichmentsMatchDimensions(t *testing.T) {
	m, err := protocol.LoadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{qualityKey: "Quality"}
	for _, d := range dimensions {
		want[d.key] = d.label
	}
	got := map[string]string{}
	for k, e := range m.Enrichments {
		got[k] = e.Label
		if e.Type != "score" || !slices.Equal(e.Range, []float64{0, 100}) || e.Grid != (k == qualityKey) {
			t.Errorf("enrichment %s = %+v", k, e)
		}
	}
	if !maps.Equal(got, want) {
		t.Fatalf("enrichments %v, want %v", got, want)
	}
}

func TestEnrichSkipsDimensionsWithoutAScore(t *testing.T) {
	_, cfg := withLLM(t, streamed(`{"summary": "Thin.", "overall_score": 40, "dimensions": {
"consistency": {"comment": "no score"}, "voice": {"score": null, "comment": "null"}, "roleplayability": {"score": 55}}}`))
	h := startPlugin(t, cfg)
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(res.Results[0].Values)); !slices.Equal(got, []string{"quality", "roleplayability"}) {
		t.Fatalf("values %v, want only the scored keys", res.Results[0].Values)
	}
}

func TestEnrichReportWithoutScoresIsAnError(t *testing.T) {
	_, cfg := withLLM(t, streamed(`{"summary": "x"}`))
	h := startPlugin(t, cfg)
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Results[0]; r.Values != nil || !strings.Contains(r.Error, "no scores") {
		t.Fatalf("result %+v, want a no-scores error", r)
	}
}

func TestEnrichOffersNoTools(t *testing.T) {
	llm, h := skillsHost(t, streamed(evaluated))
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil || res.Results[0].Error != "" {
		t.Fatalf("results %+v: %v", res.Results, err)
	}
	if _, ok := llm.requests[0]["tools"]; ok || strings.Contains(systemPrompt(llm.requests[0]), "load_skill") {
		t.Fatalf("enrich offered tools: %v", llm.requests[0]["tools"])
	}
}

func TestEnrichPromptVersionFollowsTheSetup(t *testing.T) {
	prompt := func(change func(map[string]any)) string {
		t.Helper()
		_, cfg := withLLM(t, streamed(evaluated))
		change(cfg)
		res, err := startPlugin(t, cfg).enrich(t, enrichParams("Brakka"))
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]string
		if err := json.Unmarshal(res.Results[0].Meta, &meta); err != nil {
			t.Fatal(err)
		}
		return meta["prompt"]
	}
	base := prompt(func(map[string]any) {})
	if again := prompt(func(map[string]any) {}); again != base {
		t.Fatalf("same setup: %s then %s", base, again)
	}
	for name, change := range map[string]func(map[string]any){
		"house_style": func(c map[string]any) { c["prompts"] = map[string]any{"house_style": "British spelling."} },
		"override": func(c map[string]any) {
			c["prompts"] = map[string]any{"house_style": "No em dashes.", "evaluate": "Be harsh."}
		},
		"no instructions": func(c map[string]any) {
			c["prompts"] = map[string]any{"house_style": "No em dashes.", "evaluate": ""}
		},
		"temperature": func(c map[string]any) { c["temperature"] = 0.9 },
		"max_tokens":  func(c map[string]any) { c["max_tokens"] = 1000 },
	} {
		if got := prompt(change); got == base {
			t.Errorf("changing %s kept prompt %s", name, got)
		}
	}
	if other := prompt(func(c map[string]any) {
		c["prompts"] = map[string]any{"house_style": "No em dashes.", "revise": "x"}
	}); other != base {
		t.Errorf("another action's override changed the prompt version")
	}
}

// confident answers every decide question at level 3 of 4 (75) with confidence 0.9.
func confident() *fakeDecider {
	f := &fakeDecider{level: map[string]int{}, confidence: map[string]float64{}}
	for _, q := range decideQuestions {
		f.level[q.key], f.confidence[q.key] = 3, 0.9
	}
	return f
}

func values(t *testing.T, r protocol.EnrichResult) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	for k, v := range r.Values {
		var n float64
		if err := json.Unmarshal(v, &n); err != nil {
			t.Fatalf("%s = %s: %v", k, v, err)
		}
		got[k] = n
	}
	return got
}

func TestEnrichScoresWithDecide(t *testing.T) {
	llm, cfg := withLLM(t)
	d := confident()
	d.level["voice"] = 4
	h := startPlugin(t, withDecider(t, cfg, d))
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	// Quality is the mean of all four, craft included; craft isn't stored.
	want := map[string]float64{"first_impression": 75, "voice": 100, "roleplayability": 75, "quality": 81.25}
	if got := values(t, r); !maps.Equal(got, want) || r.Error != "" {
		t.Fatalf("values %v (%s), want %v", got, r.Error, want)
	}
	var meta map[string]string
	if err := json.Unmarshal(r.Meta, &meta); err != nil || meta["model"] != "d3" || !strings.HasPrefix(meta["prompt"], "decide-") {
		t.Fatalf("meta %s: %v", r.Meta, err)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("the chat model was called %d times", len(llm.requests))
	}
	if len(d.requests) != 1 || len(d.requests[0].Questions) != 4 || !strings.Contains(string(d.requests[0].State), "A towering orc.") {
		t.Fatalf("decide requests %+v", d.requests)
	}
}

func TestEnrichEscalatesLowConfidenceToTheChatModel(t *testing.T) {
	llm, cfg := withLLM(t, streamed(evaluated))
	d := confident()
	d.confidence["voice"], d.confidence["craft"] = 0, 0
	cfg["escalate_below"] = 0.8
	h := startPlugin(t, withDecider(t, cfg, d))
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	if got := values(t, r); got["consistency"] != 90 || got["quality"] != 82 {
		t.Fatalf("values %v, want the chat model's", got)
	}
	if !strings.Contains(string(r.Meta), `"evaluate-`) || len(llm.requests) != 1 {
		t.Fatalf("meta %s, chat calls %d", r.Meta, len(llm.requests))
	}
}

func TestEnrichFallsBackToChatWhenDecideFails(t *testing.T) {
	llm, cfg := withLLM(t, streamed(evaluated))
	d := &fakeDecider{err: protocol.NewError(protocol.CodeAIRejected, "prompt too long", false)}
	h := startPlugin(t, withDecider(t, cfg, d))
	res, err := h.enrich(t, enrichParams("Brakka"))
	if err != nil {
		t.Fatal(err)
	}
	if got := values(t, res.Results[0]); got["quality"] != 82 || len(llm.requests) != 1 {
		t.Fatalf("values %v, chat calls %d", got, len(llm.requests))
	}
}
