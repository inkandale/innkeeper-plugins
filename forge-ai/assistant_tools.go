package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	// maxToolResult caps what one tool call adds to the model's context.
	maxToolResult = 12 << 10
	searchSize    = 20
	// descriptionRunes is how much of a description a search result carries.
	descriptionRunes = 300
	// maxCardName caps a card's name, which every later turn replays.
	maxCardName = 100
)

// errToolFailed is a library tool's failure, which the model sees.
type errToolFailed string

func (e errToolFailed) Error() string { return string(e) }

// errNoAccess is its own error because no retry by the model can help.
var errNoAccess = errors.New("Turn on Library access in this plugin's settings first.")

var errUnreadable = errors.New("the library's reply couldn't be read")

// searchTools find and read cards in the library.
var searchTools = []protocol.ChatTool{
	tool("search", "Find cards by meaning.", map[string]any{"text": map[string]any{"type": "string"}}, "text"),
	tool("similar", "Find cards that read like a card.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
	tool("related", "Find cards by the same creator, or sharing a lorebook or collection.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
	tool("read_card", "Read one card in full.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
}

// turnInput is one turn as the panel sends it.
type turnInput struct {
	Conversation int64  `json:"conversation"`
	Text         string `json:"text"`
	Library      int64  `json:"library"`
	AllLibraries bool   `json:"all_libraries"`
	// Draft is the card open in the Forge, sent only with the cards.edit grant.
	Draft *draft `json:"draft"`
}

// turn is the state of one turn's tool calls.
type turn struct {
	input turnInput
	// library calls one of the core's library tools.
	library func(name, arguments string) (string, error)
	// seen maps each card the tools returned to its name. Blocks can name only these.
	seen map[int64]string
	// fatal is a tool failure that ends the turn.
	fatal error
	// editable holds the draft's text fields: what the model sees and a proposal may change.
	editable map[string]string
	// reply holds the blocks of the reply call that passed the checks.
	reply []block
}

// shortCard is a card as a list result shows it to the model.
type shortCard struct {
	ID          int64                      `json:"id"`
	Name        string                     `json:"name"`
	Group       string                     `json:"group,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	Description string                     `json:"description,omitempty"`
	Similarity  float64                    `json:"similarity,omitempty"`
	Scores      map[string]json.RawMessage `json:"scores,omitempty"`
}

// discovered is one of find_similar_cards' or find_related_cards' items.
type discovered struct {
	CardID    int64    `json:"card_id"`
	LibraryID int64    `json:"library_id"`
	Name      string   `json:"name"`
	Tags      []string `json:"tags"`
	Score     float64  `json:"score"`
}

// answer runs a search tool. A failure goes back to the model as the tool's result.
func (t *turn) answer(name, arguments string) string {
	var args struct {
		Text   string `json:"text"`
		CardID int64  `json:"card_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "error: the arguments aren't valid JSON"
	}
	var out string
	var err error
	switch name {
	case "search":
		out, err = t.search(args.Text)
	case "similar":
		out, err = t.similar(args.CardID)
	case "related":
		out, err = t.related(args.CardID)
	case "read_card":
		out, err = t.readCard(args.CardID)
	default:
		err = errors.New("no such tool")
	}
	if err != nil {
		out = "error: " + err.Error()
	}
	return capped(out)
}

func capped(s string) string {
	if len(s) <= maxToolResult {
		return s
	}
	return strings.ToValidUTF8(s[:maxToolResult], "") + "\n[cut off at 12 KiB]"
}

func (t *turn) search(text string) (string, error) {
	query := map[string]any{"text": text, "semantic": true, "fields": []string{"name", "description"},
		"include": []string{"tags", "score"}, "page": map[string]any{"size": searchSize}}
	// search_cards covers one library and has no all-libraries scope.
	args := map[string]any{"query": query, "library": t.input.Library}
	note := ""
	if t.input.AllLibraries {
		note = "This search covers only this library; similar and related reach the others.\n"
	}
	out, err := t.call("search_cards", args)
	if noVectorSource(err) {
		delete(query, "semantic")
		note += "Search by meaning isn't set up, so this is a plain text search; say so in your reply.\n"
		out, err = t.call("search_cards", args)
	}
	if err != nil {
		return "", err
	}
	var res struct {
		Hits []struct {
			ID     int64          `json:"id"`
			Fields map[string]any `json:"fields"`
			Tags   []string       `json:"tags"`
			Scores []struct {
				Key   string          `json:"key"`
				Value json.RawMessage `json:"value"`
			} `json:"scores"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errUnreadable
	}
	cards := []shortCard{}
	for _, h := range res.Hits {
		name, _ := h.Fields["name"].(string)
		description, _ := h.Fields["description"].(string)
		runes := []rune(description)
		card := shortCard{ID: h.ID, Name: name, Tags: h.Tags, Description: string(runes[:min(len(runes), descriptionRunes)])}
		for _, s := range h.Scores {
			if card.Scores == nil {
				card.Scores = map[string]json.RawMessage{}
			}
			// Core orders a card's scores highest priority first.
			if _, taken := card.Scores[s.Key]; !taken {
				card.Scores[s.Key] = s.Value
			}
		}
		t.remember(h.ID, name)
		cards = append(cards, card)
	}
	return note + brief(cards), nil
}

// noVectorSource matches the core's library.ErrNoVectorInstance and
// ErrNoVectors, which reach a plugin only as tool error text. "semantic
// search needs text" is a different failure, so the prefix alone won't do.
func noVectorSource(err error) bool {
	var failed errToolFailed
	return errors.As(err, &failed) && (strings.Contains(string(failed), "semantic search needs a vector source") ||
		strings.Contains(string(failed), "semantic search needs pgvector"))
}

func (t *turn) similar(id int64) (string, error) {
	scope := "library"
	if t.input.AllLibraries {
		scope = "all"
	}
	out, err := t.call("find_similar_cards", map[string]any{"card_id": id, "scope": scope})
	if err != nil {
		return "", err
	}
	var res struct {
		Items []discovered `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errUnreadable
	}
	cards := []shortCard{}
	for _, item := range res.Items {
		if t.inScope(item.LibraryID) {
			cards = append(cards, t.keep(item, ""))
		}
	}
	return brief(cards), nil
}

func (t *turn) related(id int64) (string, error) {
	out, err := t.call("find_related_cards", map[string]any{"card_id": id})
	if err != nil {
		return "", err
	}
	var res struct {
		Groups []struct {
			Group string       `json:"group"`
			Items []discovered `json:"items"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errUnreadable
	}
	cards := []shortCard{}
	for _, g := range res.Groups {
		for _, item := range g.Items {
			if t.inScope(item.LibraryID) {
				cards = append(cards, t.keep(item, g.Group))
			}
		}
	}
	return brief(cards), nil
}

func (t *turn) readCard(id int64) (string, error) {
	out, err := t.call("get_card", map[string]any{"id": id})
	if err != nil {
		return "", err
	}
	var res struct {
		Entity struct {
			ID        int64 `json:"id"`
			LibraryID int64 `json:"library_id"`
			Revision  struct {
				Data json.RawMessage `json:"data"`
			} `json:"revision"`
		} `json:"entity"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", errUnreadable
	}
	if !t.inScope(res.Entity.LibraryID) {
		return "", errors.New("that card is in another library")
	}
	var data struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res.Entity.Revision.Data, &data); err != nil {
		return "", errUnreadable
	}
	t.remember(res.Entity.ID, data.Name)
	return string(res.Entity.Revision.Data), nil
}

func (t *turn) inScope(library int64) bool {
	return t.input.AllLibraries || library == t.input.Library
}

// remember flattens the name because a block stores it for later turns to replay.
func (t *turn) remember(id int64, name string) {
	t.seen[id] = oneLine(name, maxCardName)
}

func (t *turn) keep(item discovered, group string) shortCard {
	t.remember(item.CardID, item.Name)
	return shortCard{ID: item.CardID, Name: item.Name, Group: group, Tags: item.Tags, Similarity: item.Score}
}

func brief(cards []shortCard) string {
	raw, _ := json.Marshal(cards)
	return string(raw)
}

func (t *turn) call(name string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	out, err := t.library(name, string(raw))
	if errors.Is(err, errNoAccess) {
		t.fatal = err
	}
	return out, err
}

// oneLine flattens text to a single line of at most limit runes.
func oneLine(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	return string(runes[:min(len(runes), limit)])
}

// assistantLibrary calls the core's library tools for run. Unlike
// coreLibrary, it ends the turn without Library access and keeps a tool's
// error text apart, which search reads to fall back to text.
func assistantLibrary(ctx context.Context, conn *protocol.Conn, run string) func(name, arguments string) (string, error) {
	return func(name, arguments string) (string, error) {
		var res protocol.MCPCallResult
		err := conn.Call(ctx, protocol.MethodLibraryCall,
			protocol.LibraryCallParams{Run: run, Name: name, Arguments: json.RawMessage(arguments)}, &res)
		var rpcErr *protocol.Error
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeForbidden {
			return "", errNoAccess
		}
		if err != nil {
			return "", err
		}
		var text []string
		for _, c := range res.Content {
			text = append(text, c.Text)
		}
		out := strings.Join(text, "\n")
		if res.IsError {
			return "", errToolFailed(out)
		}
		return out, nil
	}
}
