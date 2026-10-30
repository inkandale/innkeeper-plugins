# Card embeddings 4: the `card-embed` plugin

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One plugin, `card-embed`, turns each card into a vector, after summarizing its premise with a chat model when one is set, and answers `vector.query` for semantic search. Forge stops summarizing.

**Architecture:** An enrich plugin with two AI connections. For each card it optionally asks `ai.chat` for a premise and themes (the prompt and parser move from forge-ai), embeds that text or the card's own fields with `ai.embed`, and returns `premise`, `themes` and an `embedding` vector value. It skips embedding when core doesn't report the `vectors` feature.

**Tech stack:** Go, the Innkeeper plugin protocol.

**Spec:** [card-embed design](https://github.com/inkandale/innkeeper/blob/develop/docs/superpowers/specs/2026-10-30-card-embed-design.md). Core plans 2 and 3 must be merged to `develop` first: this plan uses `protocol.VectorValue`, `MethodVectorQuery`, `FeatureVectors`, `AIEmbedResult.Model`, `enrich_on_change`, and the kit's embeddings route.

---

## Conventions

- Work in `~/git/innkeeper-plugins-card-embed` (branch `card-embed`).
- Each plugin is its own folder with `plugin.toml`, Go files in
  `package main`, and an entry in `index.toml`. `make check` runs the
  contract kit on every plugin in the index.
- Comments say why, in one line. Match the surrounding plugins' style
  (`crucible/` is the closest small enrich plugin).

## Files

| File | Change |
|---|---|
| `go.mod`, `go.sum` | Pin the core commit with plans 2 and 3 |
| `ai/embed.go`, `ai/embed_test.go` | Name the model in `ai.embed`'s reply |
| `card-embed/plugin.toml` | New |
| `card-embed/main.go` | Settings, `Serve`, `vector.query` |
| `card-embed/summary.go`, `card-embed/prompts/summary.tmpl` | Moved from forge-ai |
| `card-embed/enrich.go` | Per-card summarize-then-embed |
| `card-embed/main_test.go`, `card-embed/summary_test.go` | Tests |
| `card-embed/README.md` | Setup, for owners |
| `forge-ai/enrich.go`, `forge-ai/plugin.toml`, `forge-ai/README.md`, `forge-ai/*_test.go` | Drop the summary |
| `forge-ai/summary.go`, `forge-ai/summary_test.go`, `forge-ai/prompts/summary.tmpl` | Deleted (moved) |
| `index.toml` | `card-embed` entry; bump `ai` and `forge-ai` |

---

### Task 1: Pin core and name the embed model

**Files:** `go.mod`, `go.sum`, `ai/embed.go`, `ai/embed_test.go`

- [ ] **Step 1: Pin core**

```bash
go get github.com/inkandale/innkeeper@develop && go mod tidy && go build ./...
```

Expected: builds. If something fails because of plan 2's rename
(`EnrichOnImport` → `EnrichOnChange`, or the retired `embed` flag), fix the
reference; `card-lint`'s `plugin.toml` may keep `enrich_on_import`, which
core still reads, but rename it to `enrich_on_change` now.

- [ ] **Step 2: Write the failing test**

In `ai/embed_test.go`, extend the existing successful embed test to assert
the reply's `Model` equals the instance's configured model (the test's
config sets one; use that value).

```bash
go test ./ai -run Embed
```

Expected: FAIL: `Model` is empty.

- [ ] **Step 3: Implement**

In `ai/embed.go`, `embed`'s return:

```go
	return protocol.AIEmbedResult{Vectors: vectors, Model: c.settings.Model}, nil
```

- [ ] **Step 4: Test and commit**

```bash
go test ./ai/... && go vet ./...
git add go.mod go.sum ai card-lint
git commit -m "feat(ai): name the model in ai.embed's reply"
```

---

### Task 2: Move the summarizer into `card-embed`

**Files:** create `card-embed/summary.go`, `card-embed/prompts/summary.tmpl`,
`card-embed/summary_test.go`

- [ ] **Step 1: Move the files with history**

```bash
mkdir -p card-embed/prompts
git mv forge-ai/prompts/summary.tmpl card-embed/prompts/summary.tmpl
git mv forge-ai/summary.go card-embed/summary.go
git mv forge-ai/summary_test.go card-embed/summary_test.go
```

forge-ai won't build until Task 5; that's expected.

- [ ] **Step 2: Make `summary.go` stand alone**

Replace `card-embed/summary.go`'s `summarize` method (the one that calls
`srv.chat`) with a function over the core connection, and embed the
prompt here, since forge-ai's `promptFiles` stays behind:

```go
//go:embed prompts/summary.tmpl
var summaryPrompt string

// promptVersion names the summary prompt in a vector's space, so vectors
// from an edited prompt aren't compared with older ones.
var promptVersion = func() string {
	h := sha256.Sum256([]byte(summaryPrompt))
	return hex.EncodeToString(h[:4])
}()

// summarize asks the summary connection for a revision's premise and themes,
// at temperature 0 so a card always gets the same summary.
func summarize(ctx context.Context, conn *protocol.Conn, s *settings, data json.RawMessage) (map[string]json.RawMessage, error) {
	text, err := summaryText(data)
	if err != nil {
		return nil, err
	}
	temperature, maxTokens := 0.0, 512
	var res protocol.AIChatResult
	err = conn.Call(ctx, protocol.MethodAIChat, protocol.AIChatParams{
		ServiceCall: protocol.ServiceCall{Instance: s.Summary},
		Messages:    []protocol.ChatMessage{{Role: "user", Content: summaryPrompt + text}},
		Temperature: &temperature, MaxTokens: &maxTokens,
	}, &res)
	if err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}
	return summaryValues(res.Text)
}
```

Keep `premiseKey`, `themesKey`, `summaryChars`, `summaryFields`,
`summaryLine`, `summaryText` and `summaryValues` as they are. Fix the
imports (`crypto/sha256`, `encoding/hex`, `embed` as `_ "embed"`).

- [ ] **Step 3: Commit with the next task**

`summary_test.go` tests only `summaryText` and `summaryValues`, which
didn't change. They run once the package builds in Task 3.

---

### Task 3: The plugin

**Files:** create `card-embed/plugin.toml`, `card-embed/main.go`,
`card-embed/enrich.go`, `card-embed/main_test.go`

- [ ] **Step 1: The manifest**

`card-embed/plugin.toml`:

```toml
name = "card-embed"
title = "Similar cards"
version = "0.1.0"
protocol = 1
command = ["./card-embed"]
capabilities = ["enrich"]
description = "Finds similar cards and powers search by meaning: embeds each card, after a chat model sums up its premise if you pick one. Needs the AI plugin with an embedding model, and pgvector in PostgreSQL."
category = "AI"
uses = ["ai.chat", "ai.embed"]
# Each change costs one embedding call, and one chat call with a summary connection.
enrich_on_change = true
# A backfill of a whole archive is one run.
enrich_max_cards = 1000000

[config]
type = "object"
required = ["embed_connection"]

[config.properties.embed_connection]
type = "integer"
title = "Embedding connection"
format = "instance"
x-offers = "ai.embed"
description = "The AI connection that turns text into vectors. Changing its model means re-running this on your cards."

[config.properties.summary_connection]
type = "integer"
title = "Summary connection"
format = "instance"
x-offers = "ai.chat"
description = "Optional: a chat model that sums up each card's premise and themes first, so similar cards share a premise rather than wording. A small model is enough, such as Gemma 4 E4B."

[config.properties.concurrency]
x-advanced = true
type = "integer"
title = "Cards summarized at once"
minimum = 1
maximum = 32
default = 4
description = "Raise it for a hosted or batching model server; lower it for one that runs one request at a time."

[enrichments]
premise = { type = "text", label = "Premise" }
themes = { type = "list", label = "Themes" }
embedding = { type = "vector", label = "Embedding" }
```

- [ ] **Step 2: Write the failing tests**

`card-embed/main_test.go`. A fake core answers `ai.chat` and `ai.embed`
and records calls:

```go
package main

import (
	"context"
	"encoding/json"
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
// and ai.chat replies with reply.
type core struct {
	mu     sync.Mutex
	reply  string
	embeds []string
	chats  int
	conn   *protocol.Conn
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
			res := protocol.AIEmbedResult{Model: "m1"}
			for _, s := range p.Texts {
				c.embeds = append(c.embeds, s)
				res.Vectors = append(res.Vectors, []float32{float32(len(s)), 1})
			}
			return res, nil
		case protocol.MethodAIChat:
			c.chats++
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
	if !strings.HasPrefix(v.Space, "embed:m1|summary:8|prompt:") || c.embeds[0] != "A smith keeps a grudge.\ngrudges, forge" {
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
	c.reply = "I'd rather not."
	r := c.enrich(t, brakka).Results[0]
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
```

If a second `initialize` on one connection isn't allowed, have `start`
return the `InitializeResult` and assert on that instead.

- [ ] **Step 3: Run them and see them fail**

```bash
go test ./card-embed
```

Expected: compile errors: `undefined: plugin`.

- [ ] **Step 4: `main.go`**

```go
// Command card-embed is an enrich plugin. It gives each card a vector for
// similar cards and search by meaning, from a summary of its premise when a
// chat connection is set, and answers vector.query with the same model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/inkandale/innkeeper/protocol"
)

// settings is an instance's config and what core told it at initialize.
type settings struct {
	Embed, Summary int64
	Concurrency    int
	// Vectors is whether core stores vectors; without it, embedding is wasted.
	Vectors bool
	conn    *protocol.Conn
}

func parseSettings(p protocol.InitializeParams, conn *protocol.Conn) *settings {
	id := func(key string) int64 { f, _ := p.Config[key].(float64); return int64(f) }
	s := &settings{Embed: id("embed_connection"), Summary: id("summary_connection"), Concurrency: 4,
		Vectors: slices.Contains(p.Features, protocol.FeatureVectors), conn: conn}
	if n, ok := p.Config["concurrency"].(float64); ok && n >= 1 {
		s.Concurrency = int(n)
	}
	return s
}

func plugin() protocol.Plugin[settings] {
	return protocol.Plugin[settings]{
		// Core's config check skips required settings, so start without them and fail when used.
		Init: func(ctx context.Context, p protocol.InitializeParams) (*settings, error) {
			return parseSettings(p, protocol.Peer(ctx)), nil
		},
		Methods: map[string]func(*settings, context.Context, json.RawMessage) (any, error){
			protocol.MethodEnrichRun:   (*settings).enrich,
			protocol.MethodVectorQuery: (*settings).query,
		},
		Services: func(*settings) []string { return []string{protocol.MethodVectorQuery} },
	}
}

func main() { protocol.Serve(plugin()) }

// space names the models and prompt behind a vector. The embed model comes
// from its reply; the summary model is named by its connection, since
// vector.query never calls it.
func (s *settings) space(embedModel string) string {
	out := "embed:" + embedModel
	if embedModel == "" {
		out = "embed:instance " + strconv.FormatInt(s.Embed, 10)
	}
	if s.Summary != 0 {
		out += fmt.Sprintf("|summary:%d|prompt:%s", s.Summary, promptVersion)
	}
	return out
}

// embed returns one vector per text from the embed connection, and its space.
func (s *settings) embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	if s.Embed == 0 {
		return nil, "", errors.New("pick an embedding connection in this plugin's settings")
	}
	var res protocol.AIEmbedResult
	if err := s.conn.Call(ctx, protocol.MethodAIEmbed, protocol.AIEmbedParams{
		ServiceCall: protocol.ServiceCall{Instance: s.Embed}, Texts: texts}, &res); err != nil {
		return nil, "", fmt.Errorf("embed: %w", err)
	}
	if err := res.Validate(len(texts)); err != nil {
		return nil, "", fmt.Errorf("embed: %w", err)
	}
	return res.Vectors, s.space(res.Model), nil
}

// query embeds a search query as written: a query reads like a premise already.
func (s *settings) query(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.VectorQueryParams
	if err := json.Unmarshal(params, &p); err != nil || p.Text == "" {
		return nil, protocol.InvalidParams(errors.New("vector.query needs text"))
	}
	vectors, space, err := s.embed(ctx, []string{p.Text})
	if err != nil {
		return nil, err
	}
	return protocol.VectorValue{Space: space, Vector: vectors[0]}, nil
}
```

Check `protocol.AIEmbedParams` embeds `ServiceCall` (core's plan 2 relies
on it for `callService`); if the field is named differently, follow it.
If `protocol.Peer(ctx)` is nil inside `Init`, take the conn the way
forge-ai's `Init` does (search `forge-ai/main.go` for `conn`).

- [ ] **Step 5: `enrich.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper/protocol"
)

// rawChars caps a card's own text sent to the embedding model; most read
// fewer than 8,192 tokens.
const rawChars = 8000

// enrich summarizes a batch's cards, up to Concurrency at once, then embeds
// them in one call. A card whose summary fails gets no vector, so no card
// has vectors from two kinds of text.
func (s *settings) enrich(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, protocol.InvalidParams(fmt.Errorf("revisions must hold 1 to %d", protocol.MaxEnrichBatch))
	}
	if s.Summary == 0 && !s.Vectors {
		return nil, errors.New("nothing to write: pick a summary connection, or install pgvector so vectors can be stored")
	}
	results := make([]protocol.EnrichResult, len(p.Revisions))
	texts := make([]string, len(p.Revisions))
	var wg sync.WaitGroup
	slots := make(chan struct{}, s.Concurrency)
	for i, rev := range p.Revisions {
		results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Values: map[string]json.RawMessage{}}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			text, err := s.cardText(ctx, rev, results[i].Values)
			if err != nil {
				results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Error: err.Error()}
				return
			}
			texts[i] = text
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Vectors {
		s.addVectors(ctx, results, texts)
	}
	return &protocol.EnrichRunResult{Results: results}, nil
}

// cardText is the text to embed for rev: its premise and themes when a
// summary connection is set, written into values, else its own fields.
func (s *settings) cardText(ctx context.Context, rev protocol.EnrichRevision, values map[string]json.RawMessage) (string, error) {
	if s.Summary == 0 {
		return rawText(rev.Data)
	}
	summary, err := summarize(ctx, s.conn, s, rev.Data)
	if err != nil {
		return "", err
	}
	values[premiseKey], values[themesKey] = summary[premiseKey], summary[themesKey]
	var premise string
	var themes []string
	_ = json.Unmarshal(summary[premiseKey], &premise)
	_ = json.Unmarshal(summary[themesKey], &themes)
	return premise + "\n" + strings.Join(themes, ", "), nil
}

// rawText is the fields that say who the character is, leaving out
// greetings and example dialogue so the vector isn't about prose style.
func rawText(data json.RawMessage) (string, error) {
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		return "", fmt.Errorf("read card: %w", err)
	}
	var parts []string
	for _, f := range []string{"name", "description", "personality", "scenario", "creator_notes"} {
		if v, _ := card[f].(string); strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		return "", errors.New("the card has no text to embed")
	}
	if len(text) > rawChars {
		text = strings.ToValidUTF8(text[:rawChars], "")
	}
	return text, nil
}

// addVectors embeds the texts of the cards that got one, in one call. If
// that call fails, each card is tried alone, so one text the model rejects
// fails only its own card.
func (s *settings) addVectors(ctx context.Context, results []protocol.EnrichResult, texts []string) {
	var idx []int
	var batch []string
	for i, t := range texts {
		if results[i].Error == "" {
			idx, batch = append(idx, i), append(batch, t)
		}
	}
	if len(batch) == 0 {
		return
	}
	set := func(i int, v []float32, space string) {
		raw, _ := json.Marshal(protocol.VectorValue{Space: space, Vector: v})
		results[i].Values["embedding"] = raw
	}
	if vectors, space, err := s.embed(ctx, batch); err == nil {
		for k, i := range idx {
			set(i, vectors[k], space)
		}
		return
	}
	for k, i := range idx {
		vectors, space, err := s.embed(ctx, batch[k:k+1])
		if err != nil {
			results[i] = protocol.EnrichResult{RevisionID: results[i].RevisionID, Error: err.Error()}
			continue
		}
		set(i, vectors[0], space)
	}
}
```

- [ ] **Step 6: Run the tests**

```bash
go test ./card-embed && go vet ./card-embed
```

Expected: PASS, including the moved `summary_test.go`.

- [ ] **Step 7: Commit**

```bash
git add card-embed
git commit -m "feat(card-embed): embed cards, after a premise summary when set, and answer vector.query"
```

---

### Task 4: List it and check the contract

**Files:** `index.toml`, `card-embed/README.md`

- [ ] **Step 1: Add the index entry**

In `index.toml`, after `crucible`'s entry:

```toml
[[plugin]]
name = "card-embed"
title = "Similar cards"
category = "AI"
path = "card-embed"
version = "0.1.0"
protocol = 1
capabilities = ["enrich"]
description = "Finds similar cards and powers search by meaning: embeds each card, after a chat model sums up its premise if you pick one. Needs the AI plugin with an embedding model, and pgvector in PostgreSQL."
```

Copy any field the other entries have that this one lacks, such as
`uses`, from `forge-ai`'s entry.

- [ ] **Step 2: Run the contract kit**

```bash
make check
```

Expected: every plugin passes. `card-embed` declares `uses`, so the kit
points both connections at the `ai` plugin and its fake model, which serves
embeddings after core plan 2's Task 9. If `ai/check-provider.json`'s
`kinds` lacks `embed`, add it:
`"kinds": ["chat", "embed"]`.

- [ ] **Step 3: Write the README**

Invoke the `documentation-writing` skill first. `card-embed/README.md`, for
an owner setting it up:

```markdown
# Similar cards

Gives each card a vector, so the card page shows similar cards and search
can rank by meaning.

## Set up

1. In the AI plugin, add a connection that provides embeddings.
2. Optional: add a chat connection for summaries. Similar cards then match
   on what a card is about rather than how it's written. A small model is
   enough; Gemma 4 E4B summarized 125 cards in 18 seconds on one RTX 5090.
3. Add a Similar cards instance with those connections.
4. In Settings, under Plugins, set **Vectors from** to it.
5. Run it from the library's selection bar on the cards you have. New and
   edited cards are done as they change.

PostgreSQL needs pgvector. Without it, the plugin still writes premises and
themes, and similar cards rank by shared tags.

## Values

| Key | What |
|---|---|
| `premise` | Two or three sentences on the card's setup, with no proper names |
| `themes` | Five to ten lowercase tags |
| `embedding` | The card's vector; core keeps it for similar cards and search |

Changing the embedding model, the summary connection, or the summary
prompt starts a new set of vectors: run the plugin on your cards again.
Until then, those cards fall back to similar-by-tags.
```

- [ ] **Step 4: Commit**

```bash
git add index.toml card-embed/README.md ai/check-provider.json
git commit -m "feat(card-embed): list it in the index"
```

---

### Task 5: Forge stops summarizing

**Files:** `forge-ai/enrich.go`, `forge-ai/plugin.toml`, `forge-ai/README.md`,
`forge-ai/enrich_test.go`, `forge-ai/run_test.go`, `index.toml`

- [ ] **Step 1: Update the tests first**

- In `forge-ai/enrich_test.go`: delete
  `TestEnrichSummaryWithoutAPremiseFailsTheRevision` and
  `TestEnrichSkipsTheSummaryWhenScoringFails`; in
  `TestEnrichMapsEvaluationToDeclaredKeys` delete the premise/themes
  assertion (line ~56); in the test at line ~193 expect
  `[]string{"quality", "roleplayability"}`; and delete the
  `premiseKey`/`themesKey` skips at lines ~169 and ~276 along with the
  comment that mentions them.
- In `forge-ai/run_test.go`: delete `fakeLLM`'s `summary` and `summaries`
  fields, `fakeSummary`, and the branch in `answer` that intercepts
  "You index roleplay character cards". Fix `fakeLLM`'s doc comment to drop
  the summary sentence.
- Delete the scratch `forge-ai/gemma_eval_test.go` if it is in this tree.

```bash
go test ./forge-ai 2>&1 | head
```

Expected: compile errors for `srv.summarize`, `premiseKey`, `themesKey`.

- [ ] **Step 2: Drop the summary call**

In `forge-ai/enrich.go`, the goroutine body becomes:

```go
			values, vm, err := srv.scores(ctx, in, p, rev)
			if err != nil {
				results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Error: err.Error()}
				return
			}
			meta, _ := json.Marshal(vm)
			results[i] = protocol.EnrichResult{RevisionID: rev.RevisionID, Values: values, Meta: meta}
```

Remove the `maps` import if nothing else uses it.

In `forge-ai/plugin.toml`, delete the `premise` and `themes` enrichments
and bump `version` to `0.13.0`. In `forge-ai/README.md`, delete the
summary section (the table of `premise` and `themes`, and the paragraph
about `embed`), and add one line under scoring: "For similar cards and
search by meaning, use the Similar cards plugin."

In `index.toml`, bump `forge-ai` to `0.13.0` and `ai` to the next patch
version, matching each plugin's `plugin.toml`.

- [ ] **Step 3: Run everything**

```bash
go vet ./... && go test ./... && make check
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add forge-ai index.toml ai/plugin.toml
git commit -m "feat(forge-ai): leave summaries to the Similar cards plugin"
```

---

### Task 6: Live check

Not a CI step: it uses the 5090 and the live database read-only.

- [ ] **Step 1:** Serve Gemma 4 E4B on the 5090 with
  `~/sglang/serve-oneoff.sh gemma-4-E4B-it --context-length 32768`
  (qwen-27b is off; leave it off), and tunnel port 8090.
- [ ] **Step 2:** Point the five-card harness from the evaluation
  (`/private/tmp/claude-501/-Users-dgibbons/10c821db-4496-43ee-9449-2a952e6ebc2b/scratchpad/ikp/forge-ai/gemma_eval_test.go`)
  at `card-embed` instead: copy it into `card-embed/` as an untracked file,
  answer `ai.chat` from SGLang and `ai.embed` from the Qwen3 embedding
  endpoint the cluster's `llm` gateway serves, and run it on
  the five cards in
  `/private/tmp/claude-501/-Users-dgibbons/10c821db-4496-43ee-9449-2a952e6ebc2b/scratchpad/fe/scored.json`. Expect five results, each with a premise,
  themes and a vector, in about two seconds.
- [ ] **Step 3:** Stop SGLang when done.
