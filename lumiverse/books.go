package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// bookFields are character_book fields Lumiverse doesn't store. The plugin
// keeps them in the world book's metadata.
var bookFields = []string{"scan_depth", "token_budget", "recursive_scanning", "extensions"}

type worldBook struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

type page[T any] struct {
	Data  []T `json:"data"`
	Total int `json:"total"`
}

func (p *plugin) putLorebook(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetPutLorebookParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var book map[string]any
	if err := json.Unmarshal(in.Lorebook, &book); err != nil || book == nil {
		return nil, protocol.InvalidParams(fmt.Errorf("lorebook is not a JSON object"))
	}
	id, key := in.RemoteID, in.Key
	if id == "" && key != "" {
		found, err := p.findBook(ctx, key)
		if err != nil {
			return nil, err
		}
		id = found.ID
	}
	ccv3 := map[string]any{}
	for _, f := range bookFields {
		if v, ok := book[f]; ok {
			ccv3[f] = v
		}
	}
	pushed := map[string]any{}
	for _, k := range []string{"name", "description"} {
		if v, ok := book[k]; ok {
			pushed[k] = v
		}
	}
	info := bookInfo(pushed)
	if id == "" {
		info["metadata"] = map[string]any{"source": "innkeeper", "sync_key": key, "ccv3": ccv3, "pushed": pushed}
		var created worldBook
		if err := p.c.call(ctx, "POST", "/world-books", info, &created); err != nil {
			return nil, err
		}
		id = created.ID
	} else {
		// A replace keeps the book's ID, so characters stay linked.
		var cur worldBook
		if err := p.c.call(ctx, "GET", "/world-books/"+url.PathEscape(id)+"?limit=1", nil, &cur); err != nil {
			return nil, err
		}
		meta := cur.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		meta["source"], meta["ccv3"], meta["pushed"] = "innkeeper", ccv3, pushed
		if key != "" {
			meta["sync_key"] = key
		}
		info["metadata"] = meta
		if err := p.c.call(ctx, "PUT", "/world-books/"+url.PathEscape(id), info, nil); err != nil {
			return nil, err
		}
		if err := p.clearEntries(ctx, id); err != nil {
			return nil, err
		}
	}
	entries, _ := book["entries"].([]any)
	for i, e := range entries {
		// ponytail: one request per entry, and a failure leaves the book
		// partial until the job's retry replaces it again.
		if err := p.c.call(ctx, "POST", "/world-books/"+url.PathEscape(id)+"/entries", toEntry(object(e), i), nil); err != nil {
			return nil, fmt.Errorf("entry %d of %d: %w", i+1, len(entries), err)
		}
	}
	return protocol.TargetPutResult{RemoteID: id}, nil
}

// bookInfo is the name and description Lumiverse stores for book.
func bookInfo(book map[string]any) map[string]any {
	name, _ := book["name"].(string)
	description, _ := book["description"].(string)
	return map[string]any{"name": cmp.Or(name, "Lorebook"), "description": description}
}

// findBook returns the world book a put with key created, if any.
//
// ponytail: pages through every world book, since Lumiverse can't filter on
// metadata; fine for hundreds of books.
func (p *plugin) findBook(ctx context.Context, key string) (worldBook, error) {
	for offset := 0; ; offset += 1000 {
		var books page[worldBook]
		if err := p.c.call(ctx, "GET", fmt.Sprintf("/world-books?limit=1000&offset=%d", offset), nil, &books); err != nil {
			return worldBook{}, err
		}
		for _, b := range books.Data {
			if b.Metadata["source"] == "innkeeper" && b.Metadata["sync_key"] == key {
				return b, nil
			}
		}
		if len(books.Data) == 0 || offset+len(books.Data) >= books.Total {
			return worldBook{}, nil
		}
	}
}

// list returns every item a paged Lumiverse list route has. path already
// has a query string.
func (p *plugin) list(ctx context.Context, path string) (protocol.TargetListResult, error) {
	items := []protocol.TargetItem{}
	for offset := 0; ; offset += 1000 {
		var got page[struct{ ID, Name string }]
		if err := p.c.call(ctx, "GET", fmt.Sprintf("%s&limit=1000&offset=%d", path, offset), nil, &got); err != nil {
			return protocol.TargetListResult{}, err
		}
		for _, it := range got.Data {
			items = append(items, protocol.TargetItem{RemoteID: it.ID, Name: it.Name})
		}
		if len(got.Data) == 0 || offset+len(got.Data) >= got.Total {
			return protocol.TargetListResult{Items: items}, nil
		}
	}
}

// remove deletes the character or world book under path named by params'
// remote_id. Lumiverse answers 404 for one it doesn't have.
func (p *plugin) remove(ctx context.Context, path string, params json.RawMessage) (any, error) {
	var in protocol.TargetDeleteParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if err := p.c.call(ctx, "DELETE", path+url.PathEscape(in.RemoteID), nil, nil); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// clearEntries deletes every entry of a world book.
func (p *plugin) clearEntries(ctx context.Context, id string) error {
	for {
		var entries page[struct {
			ID string `json:"id"`
		}]
		if err := p.c.call(ctx, "GET", "/world-books/"+url.PathEscape(id)+"/entries?limit=1000", nil, &entries); err != nil {
			return err
		}
		if len(entries.Data) == 0 {
			return nil
		}
		ids := make([]string, len(entries.Data))
		for i, e := range entries.Data {
			ids[i] = e.ID
		}
		if err := p.c.call(ctx, "POST", "/world-books/"+url.PathEscape(id)+"/entries/bulk",
			map[string]any{"action": "delete", "entry_ids": ids}, nil); err != nil {
			return err
		}
	}
}

// positions maps CCv3 positions to Lumiverse's.
var positions = map[string]int{
	"before": 0, "before_char": 0, "before_character": 0,
	"after": 1, "after_char": 1, "after_character": 1,
	"before_an": 2, "before_authors_note": 2, "before_author_note": 2,
	"after_an": 3, "after_authors_note": 3, "after_author_note": 3,
	"at_depth": 4, "depth": 4,
	"before_em": 5, "before_example": 5, "before_examples": 5, "before_example_messages": 5,
	"after_em": 6, "after_example": 6, "after_examples": 6, "after_example_messages": 6,
}

// setting is a Lumiverse entry field that Lumiverse's import reads from a
// CCv3 entry's own keys, then from its extensions, with a default.
type setting struct {
	field string
	from  []string
	def   any
}

var settings = []setting{
	{"depth", []string{"depth"}, 4.0},
	{"role", []string{"role"}, nil},
	{"selective", []string{"selective"}, false},
	{"constant", []string{"constant"}, false},
	{"case_sensitive", []string{"case_sensitive", "caseSensitive"}, false},
	{"match_whole_words", []string{"match_whole_words", "matchWholeWords"}, false},
	{"group_name", []string{"group", "group_name"}, ""},
	{"group_override", []string{"group_override", "groupOverride"}, false},
	{"group_weight", []string{"group_weight", "groupWeight"}, 100.0},
	{"probability", []string{"probability"}, 100.0},
	{"scan_depth", []string{"scan_depth", "scanDepth"}, nil},
	{"automation_id", []string{"automation_id", "automationId"}, nil},
	{"selective_logic", []string{"selectiveLogic", "selective_logic"}, 0.0},
	{"use_probability", []string{"useProbability", "use_probability"}, true},
	{"use_regex", []string{"use_regex", "useRegex"}, false},
	{"prevent_recursion", []string{"prevent_recursion", "preventRecursion"}, false},
	{"exclude_recursion", []string{"exclude_recursion", "excludeRecursion"}, false},
	{"delay_until_recursion", []string{"delay_until_recursion", "delayUntilRecursion"}, false},
	{"priority", []string{"priority"}, 10.0},
	{"sticky", []string{"sticky"}, 0.0},
	{"cooldown", []string{"cooldown"}, 0.0},
	{"delay", []string{"delay"}, 0.0},
	{"vectorized", []string{"vectorized"}, false},
}

func (st setting) value(e, ext map[string]any) any {
	for _, src := range []map[string]any{e, ext} {
		for _, k := range st.from {
			if v, ok := src[k]; ok && v != nil {
				return v
			}
		}
	}
	return st.def
}

// lumiverseFields maps a CCv3 entry to the fields of a Lumiverse entry, as
// Lumiverse's own import does. The same mapping of what Lumiverse exports
// tells whether the entry changed there.
func lumiverseFields(e map[string]any, index int) map[string]any {
	ext := object(e["extensions"])
	comment, _ := e["comment"].(string)
	name, _ := e["name"].(string)
	enabled, ok := e["enabled"].(bool)
	content, _ := e["content"].(string)
	order := e["insertion_order"]
	if order == nil {
		order = index
	}
	out := map[string]any{
		"key":          listOr(e["keys"]),
		"keysecondary": listOr(e["secondary_keys"]),
		"content":      content,
		"comment":      cmp.Or(comment, name),
		"disabled":     ok && !enabled,
		"order_value":  order,
		"position":     position(e["position"]),
	}
	for _, st := range settings {
		if v := st.value(e, ext); v != nil {
			out[st.field] = v
		}
	}
	return out
}

// toEntry is the Lumiverse entry for a CCv3 entry. extensions.innkeeper
// keeps the entry as pushed and its place in the book, which Lumiverse
// would lose.
func toEntry(e map[string]any, index int) map[string]any {
	ext := map[string]any{}
	for k, v := range object(e["extensions"]) {
		if k != innkeeperKey {
			ext[k] = v
		}
	}
	orig := map[string]any{}
	for k, v := range e {
		orig[k] = v
	}
	orig["extensions"] = ext
	out := lumiverseFields(orig, index)
	stashed := map[string]any{}
	for k, v := range ext {
		stashed[k] = v
	}
	stashed[innkeeperKey] = map[string]any{"index": index, "entry": orig}
	out["extensions"] = stashed
	return out
}

func position(v any) any {
	switch p := v.(type) {
	case float64:
		return p
	case string:
		p = strings.ToLower(strings.TrimSpace(p))
		if n, err := strconv.ParseFloat(p, 64); err == nil {
			return n
		}
		if n, ok := positions[p]; ok {
			return n
		}
	}
	return 0
}

func (p *plugin) getLorebook(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetGetParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	path := "/world-books/" + url.PathEscape(in.RemoteID)
	var cur worldBook
	if err := p.c.call(ctx, "GET", path+"?limit=1", nil, &cur); err != nil {
		return nil, err
	}
	var book map[string]any
	if err := p.c.call(ctx, "GET", path+"/export?format=character_book", nil, &book); err != nil {
		return nil, err
	}
	for k, v := range object(cur.Metadata["ccv3"]) {
		book[k] = v
	}
	// A name or description Lumiverse still holds as pushed reads back as
	// pushed, including absent.
	if pushed, ok := cur.Metadata["pushed"].(map[string]any); ok {
		for k, sent := range bookInfo(pushed) {
			if book[k] != sent {
				continue
			}
			if v, ok := pushed[k]; ok {
				book[k] = v
			} else {
				delete(book, k)
			}
		}
	}
	entries, _ := book["entries"].([]any)
	// Lumiverse orders entries by insertion order; restore the book's order.
	// Entries added in Lumiverse go last.
	slices.SortStableFunc(entries, func(a, b any) int { return cmp.Compare(entryIndex(a), entryIndex(b)) })
	for i, e := range entries {
		entries[i] = fromEntry(object(e))
	}
	book["entries"] = entries
	raw, err := json.Marshal(book)
	if err != nil {
		return nil, err
	}
	return protocol.TargetGetLorebookResult{Lorebook: raw}, nil
}

// fromEntry returns the entry as pushed if Lumiverse still holds what the
// push sent, or else Lumiverse's version, with the pushed id and name.
func fromEntry(e map[string]any) any {
	ext := object(e["extensions"])
	stash := object(ext[innkeeperKey])
	delete(ext, innkeeperKey)
	orig := object(stash["entry"])
	if len(orig) == 0 {
		// Added in Lumiverse: its id is only a position in the export.
		delete(e, "id")
		return e
	}
	index, _ := stash["index"].(float64)
	if sameJSON(lumiverseFields(orig, int(index)), lumiverseFields(e, int(index))) {
		return orig
	}
	for _, k := range []string{"id", "name"} {
		if v, ok := orig[k]; ok {
			e[k] = v
		} else {
			delete(e, k)
		}
	}
	return e
}

func sameJSON(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ra, rb)
}

func entryIndex(e any) float64 {
	i, ok := object(object(object(e)["extensions"])[innkeeperKey])["index"].(float64)
	if !ok {
		return 1 << 50
	}
	return i
}
