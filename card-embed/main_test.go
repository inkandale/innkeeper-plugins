package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	embedID   = 7
	summaryID = 8
	premise   = "Premise: A smith keeps a grudge.\nThemes: grudges, forge"
)

// core is a fake Innkeeper: ai.embed gives [len(text), 1] from model m1,
// and ai.chat replies with reply. A text containing POISON makes ai.embed
// reject the request as invalid, BROKEN makes it fail plainly; a card
// containing FAILSUM gets a reply with no premise.
type core struct {
	mu         sync.Mutex
	reply      string
	embeds     []string
	embedCalls int
	chats      int
	conn       *protocol.Conn
}

func start(t *testing.T, config map[string]any, features ...string) *core {
	t.Helper()
	c := &core{reply: premise}
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	m, err := protocol.LoadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	pluginConn := protocol.NewConn(toPlugin, fromPlugin, plugin().Handler(m))
	c.conn = protocol.NewConn(toHost, fromHost, func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch method {
		case protocol.MethodAIEmbed:
			var p protocol.AIEmbedParams
			_ = json.Unmarshal(raw, &p)
			c.embedCalls++
			res := protocol.AIEmbedResult{Model: "m1"}
			for _, s := range p.Texts {
				if strings.Contains(s, "POISON") {
					return nil, &protocol.Error{Code: protocol.CodeAIRejected, Message: "input rejected"}
				}
				if strings.Contains(s, "BROKEN") {
					return nil, errors.New("key refused")
				}
			}
			for _, s := range p.Texts {
				c.embeds = append(c.embeds, s)
				res.Vectors = append(res.Vectors, []float32{float32(len(s)), 1})
			}
			return res, nil
		case protocol.MethodAIChat:
			c.chats++
			var p protocol.AIChatParams
			_ = json.Unmarshal(raw, &p)
			if strings.Contains(p.Messages[0].Content, "FAILSUM") {
				return protocol.AIChatResult{Text: "I'd rather not.", Model: "gemma"}, nil
			}
			return protocol.AIChatResult{Text: c.reply, Model: "gemma"}, nil
		}
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go pluginConn.Run(ctx)
	go c.conn.Run(ctx)
	t.Cleanup(func() { cancel(); fromHost.Close(); fromPlugin.Close() })
	var res protocol.InitializeResult
	if err := c.conn.Call(ctx, protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1, Config: config, Features: features}, &res); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *core) enrich(t *testing.T, cards ...string) protocol.EnrichRunResult {
	t.Helper()
	p := protocol.EnrichRunParams{Run: "R"}
	for i, card := range cards {
		p.Revisions = append(p.Revisions, protocol.EnrichRevision{CardID: int64(i + 1), RevisionID: int64(100 + i), Data: json.RawMessage(card)})
	}
	var res protocol.EnrichRunResult
	if err := c.conn.Call(context.Background(), protocol.MethodEnrichRun, p, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func vectorOf(t *testing.T, r protocol.EnrichResult) protocol.VectorValue {
	t.Helper()
	var v protocol.VectorValue
	if err := json.Unmarshal(r.Values["embedding"], &v); err != nil {
		t.Fatalf("embedding %s: %v", r.Values["embedding"], err)
	}
	return v
}

const brakka = `{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hm."}`

func TestEmbedsTheCardsOwnFieldsWithoutASummaryConnection(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID}, protocol.FeatureVectors)
	r := c.enrich(t, brakka).Results[0]
	if r.Error != "" || len(r.Values) != 1 {
		t.Fatalf("result %+v", r)
	}
	if v := vectorOf(t, r); v.Space != "embed:m1" || len(c.embeds) != 1 || c.embeds[0] != "Brakka\nA dwarf smith." {
		t.Fatalf("vector %+v, embedded %q", v, c.embeds)
	}
}

func TestEmbedsThePremiseWithASummaryConnection(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID, "summary_connection": summaryID}, protocol.FeatureVectors)
	r := c.enrich(t, brakka).Results[0]
	if r.Error != "" || string(r.Values["premise"]) != `"A smith keeps a grudge."` {
		t.Fatalf("result %+v", r)
	}
	v := vectorOf(t, r)
	h := sha256.Sum256([]byte(summaryPrompt))
	if v.Space != "embed:m1|summary:8|prompt:"+hex.EncodeToString(h[:4]) || c.embeds[0] != "A smith keeps a grudge.\ngrudges, forge" {
		t.Fatalf("vector %+v, embedded %q", v, c.embeds)
	}
}

func TestSkipsEmbeddingWithoutVectors(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID, "summary_connection": summaryID})
	r := c.enrich(t, brakka).Results[0]
	if r.Error != "" || r.Values["embedding"] != nil || r.Values["premise"] == nil || len(c.embeds) != 0 {
		t.Fatalf("result %+v, embedded %q", r, c.embeds)
	}
}

func TestAFailedSummaryFailsTheCardWithoutEmbedding(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID, "summary_connection": summaryID}, protocol.FeatureVectors)
	c.mu.Lock()
	c.reply = "I'd rather not."
	c.mu.Unlock()
	r := c.enrich(t, brakka).Results[0]
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.Contains(r.Error, "no premise") || len(c.embeds) != 0 {
		t.Fatalf("result %+v, embedded %q", r, c.embeds)
	}
}

func TestVectorQueryUsesTheCardsSpace(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID, "summary_connection": summaryID}, protocol.FeatureVectors)
	card := vectorOf(t, c.enrich(t, brakka).Results[0])
	var q protocol.VectorValue
	if err := c.conn.Call(context.Background(), protocol.MethodVectorQuery, protocol.VectorQueryParams{Text: "dwarf smith"}, &q); err != nil {
		t.Fatal(err)
	}
	if q.Space != card.Space || q.Vector[0] != float32(len("dwarf smith")) {
		t.Fatalf("query %+v, card space %s", q, card.Space)
	}
}

func TestOffersVectorQuery(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID})
	var res protocol.InitializeResult
	if err := c.conn.Call(context.Background(), protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1,
		Config: map[string]any{"embed_connection": embedID}}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Services) != 1 || res.Services[0] != protocol.MethodVectorQuery {
		t.Fatalf("services = %v", res.Services)
	}
}

func card(name string) string { return `{"name": "` + name + `"}` }

func TestARejectedTextFailsOnlyItsCard(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID}, protocol.FeatureVectors)
	rs := c.enrich(t, card("Ann"), card("POISON"), card("Barbara")).Results
	if rs[1].Error == "" || rs[1].Values["embedding"] != nil {
		t.Fatalf("poisoned card %+v", rs[1])
	}
	for i, want := range map[int]float32{0: 3, 2: 7} {
		if rs[i].Error != "" || vectorOf(t, rs[i]).Vector[0] != want {
			t.Fatalf("card %d: %+v, want vector %v", i, rs[i], want)
		}
	}
}

func TestAnErrorNotAboutOneCardFailsThemAllInOneCall(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID}, protocol.FeatureVectors)
	rs := c.enrich(t, card("Ann"), card("BROKEN"), card("Barbara")).Results
	for i, r := range rs {
		if !strings.Contains(r.Error, "key refused") {
			t.Fatalf("card %d: %+v", i, r)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.embedCalls != 1 {
		t.Fatalf("ai.embed called %d times", c.embedCalls)
	}
}

func TestACardWhoseSummaryFailsIsSkippedMidBatch(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID, "summary_connection": summaryID}, protocol.FeatureVectors)
	rs := c.enrich(t, brakka, card("FAILSUM"), brakka).Results
	if !strings.Contains(rs[1].Error, "no premise") || rs[1].Values["embedding"] != nil {
		t.Fatalf("failed card %+v", rs[1])
	}
	for _, i := range []int{0, 2} {
		if rs[i].Error != "" || vectorOf(t, rs[i]).Vector[0] != float32(len("A smith keeps a grudge.\ngrudges, forge")) {
			t.Fatalf("card %d: %+v", i, rs[i])
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.embeds) != 2 {
		t.Fatalf("embedded %q", c.embeds)
	}
}

func TestMissingEmbedConnectionFailsBeforeAnySummary(t *testing.T) {
	c := start(t, map[string]any{"summary_connection": summaryID}, protocol.FeatureVectors)
	res, err := callEnrich(c, brakka)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || len(res.Results) != 1 || !strings.Contains(res.Results[0].Error, "embedding connection") ||
		len(res.Results[0].Values) != 0 || c.chats != 0 || len(c.embeds) != 0 {
		t.Fatalf("res %+v err %v, chats %d", res, err, c.chats)
	}
}

func TestNothingToWriteFailsEachCard(t *testing.T) {
	c := start(t, map[string]any{"embed_connection": embedID})
	res, err := callEnrich(c, brakka)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || len(res.Results) != 1 || res.Results[0].RevisionID != 100 || !strings.Contains(res.Results[0].Error, "nothing to write") ||
		len(res.Results[0].Values) != 0 || c.chats != 0 || len(c.embeds) != 0 {
		t.Fatalf("res %+v err %v, chats %d", res, err, c.chats)
	}
}

func callEnrich(c *core, cardJSON string) (protocol.EnrichRunResult, error) {
	p := protocol.EnrichRunParams{Run: "R", Revisions: []protocol.EnrichRevision{{CardID: 1, RevisionID: 100, Data: json.RawMessage(cardJSON)}}}
	var res protocol.EnrichRunResult
	return res, c.conn.Call(context.Background(), protocol.MethodEnrichRun, p, &res)
}
