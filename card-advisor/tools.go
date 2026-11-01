package main

import (
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
)

// errToolFailed is a library tool's failure, which the model sees.
type errToolFailed string

func (e errToolFailed) Error() string { return string(e) }

// errNoAccess is its own error because no retry by the model can help.
var errNoAccess = errors.New("Turn on Library access in this plugin's settings first.")

func tool(name, description string, properties map[string]any, required ...string) protocol.ChatTool {
	params, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required})
	return protocol.ChatTool{Name: name, Description: description, Parameters: params}
}

var recommendTool = tool("recommend", "Finish with your reply and picks.", map[string]any{
	"message": map[string]any{"type": "string", "description": "Your reply to the owner, in Markdown"},
	"picks": map[string]any{"type": "array", "maxItems": maxPicks, "items": map[string]any{"type": "object",
		"required": []string{"card_id", "reason"}, "properties": map[string]any{
			"card_id": map[string]any{"type": "integer"},
			"reason":  map[string]any{"type": "string", "description": "One sentence on why it fits"},
		}}},
}, "message", "picks")

var searchTools = []protocol.ChatTool{
	tool("search", "Find cards by meaning.", map[string]any{"text": map[string]any{"type": "string"}}, "text"),
	tool("similar", "Find cards that read like a card.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
	tool("related", "Find cards by the same creator, or sharing a lorebook or collection.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
	tool("read_card", "Read one card in full.", map[string]any{"card_id": map[string]any{"type": "integer"}}, "card_id"),
	recommendTool,
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

// answer runs a tool. A failure goes back to the model as the tool's result.
func (t *turn) answer(c protocol.ToolCall) string {
	var args struct {
		Text   string `json:"text"`
		CardID int64  `json:"card_id"`
	}
	if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
		return "error: the arguments aren't valid JSON"
	}
	var out string
	var err error
	switch c.Name {
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
			card.Scores[s.Key] = s.Value
		}
		t.seen[h.ID] = name
		cards = append(cards, card)
	}
	return note + brief(cards), nil
}

var errUnreadable = errors.New("the library's reply couldn't be read")

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
	t.seen[res.Entity.ID] = data.Name
	return string(res.Entity.Revision.Data), nil
}

func (t *turn) inScope(library int64) bool {
	return t.input.AllLibraries || library == t.input.Library
}

func (t *turn) keep(item discovered, group string) shortCard {
	t.seen[item.CardID] = item.Name
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
	out, err := t.deps.library(name, string(raw))
	if errors.Is(err, errNoAccess) {
		t.fatal = err
	}
	return out, err
}
