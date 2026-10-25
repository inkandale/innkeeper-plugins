package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// appender is how an action adds the model's items to a list in the
// entity's data, keeping the items already there.
type appender struct {
	key  string // the data key the model returns and the list grows under
	item string // one item, in progress messages
	// merge is the draft with the model's items added, or the draft
	// unchanged when none survive.
	merge func(r *run, raw json.RawMessage) (json.RawMessage, error)
	// existing is the list for the prompt, beside a card. Nil sends none.
	existing func(r *run) ([]string, error)
}

var greetingsAppender = &appender{key: "alternate_greetings", item: "greeting", merge: (*run).withGreetings,
	existing: (*run).greetings}

var entriesAppender = &appender{key: "entries", item: "entry", merge: (*run).withEntries}

// greetings is the draft's alternate greetings.
func (r *run) greetings() ([]string, error) {
	var g []string
	if raw := r.draft["alternate_greetings"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, errors.New("the card's alternate_greetings isn't a list of text")
		}
	}
	return g, nil
}

// withGreetings adds the model's new greetings after the draft's, less
// blanks and repeats.
func (r *run) withGreetings(raw json.RawMessage) (json.RawMessage, error) {
	greetings, err := r.greetings()
	if err != nil {
		return nil, err
	}
	var added []string
	if err := json.Unmarshal(raw, &added); err != nil {
		return nil, errors.New("the model's alternate_greetings isn't a list of text")
	}
	n := len(greetings)
	for _, g := range added {
		if g = strings.TrimSpace(g); g != "" && !slices.Contains(greetings, g) {
			greetings = append(greetings, g)
		}
	}
	if len(greetings) == n {
		return encode(r.draft)
	}
	return r.draftWith("alternate_greetings", greetings)
}

// briefEntry is the part of a lore entry the model reads and writes.
type briefEntry struct {
	Comment string   `json:"comment,omitempty"`
	Keys    []string `json:"keys"`
	Content string   `json:"content"`
}

// entry is a new CCv3 lore entry, with the defaults core gives a blank one
// and no id, which the protocol leaves to the core.
type entry struct {
	briefEntry
	Enabled        bool           `json:"enabled"`
	InsertionOrder int            `json:"insertion_order"`
	Position       string         `json:"position"`
	UseRegex       bool           `json:"use_regex"`
	Extensions     map[string]any `json:"extensions"`
}

// entries is the draft lorebook's entries as they are.
func (r *run) entries() ([]json.RawMessage, error) {
	var list []json.RawMessage
	if raw := r.draft["entries"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, errors.New("the lorebook's entries aren't a list")
		}
	}
	return list, nil
}

// briefEntries is what the model sees of the draft's entries. A field of the
// wrong type reads as empty, and a value that isn't an object is left out;
// the proposal keeps both as they were.
func briefEntries(list []json.RawMessage) []briefEntry {
	brief := []briefEntry{}
	for _, raw := range list {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			continue
		}
		var b briefEntry
		_ = json.Unmarshal(fields["comment"], &b.Comment)
		_ = json.Unmarshal(fields["keys"], &b.Keys)
		_ = json.Unmarshal(fields["content"], &b.Content)
		brief = append(brief, b)
	}
	return brief
}

// sentLorebook is the lorebook as the model sees it.
func (r *run) sentLorebook() (map[string]any, error) {
	book := map[string]any{}
	for _, k := range []string{"name", "description"} {
		v, err := typed(k, r.draft[k])
		if err != nil {
			return nil, fmt.Errorf("the lorebook's %s: %w", k, err)
		}
		book[k] = v
	}
	list, err := r.entries()
	if err != nil {
		return nil, err
	}
	book["entries"] = briefEntries(list)
	if r.evaluates != nil || r.edits != nil {
		book["entries"] = reviewedEntries(list)
	}
	return book, nil
}

// withEntries adds the model's new entries after the draft's, which keep
// their bytes. A new entry is dropped when one of its keys is an earlier
// entry's first key or more than half its keys are taken; otherwise it
// loses only its taken keys. Keys compare ignoring case.
func (r *run) withEntries(raw json.RawMessage) (json.RawMessage, error) {
	list, err := r.entries()
	if err != nil {
		return nil, err
	}
	var added []briefEntry
	if err := json.Unmarshal(raw, &added); err != nil {
		return nil, fmt.Errorf("the model's entries: %w", err)
	}
	taken, first := map[string]bool{}, map[string]bool{}
	note := func(keys []string) {
		for i, k := range keys {
			taken[strings.ToLower(k)] = true
			if i == 0 {
				first[strings.ToLower(k)] = true
			}
		}
	}
	for _, b := range briefEntries(list) {
		note(b.Keys)
	}
	n := len(list)
	for _, b := range added {
		var keys, fresh []string
		clash := false
		for _, k := range b.Keys {
			k = strings.TrimSpace(k)
			if k == "" || slices.ContainsFunc(keys, func(s string) bool { return strings.EqualFold(s, k) }) {
				continue
			}
			keys = append(keys, k)
			clash = clash || first[strings.ToLower(k)]
			if !taken[strings.ToLower(k)] {
				fresh = append(fresh, k)
			}
		}
		b.Keys, b.Content, b.Comment = fresh, strings.TrimSpace(b.Content), strings.TrimSpace(b.Comment)
		if clash || 2*len(fresh) < len(keys) || len(fresh) == 0 || b.Content == "" {
			continue
		}
		note(fresh)
		e, err := encode(entry{briefEntry: b, Enabled: true, InsertionOrder: 100, Position: "before_char",
			Extensions: map[string]any{}})
		if err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	if len(list) == n {
		return encode(r.draft)
	}
	return r.draftWith("entries", list)
}

// draftWith is the draft with key set to v.
func (r *run) draftWith(key string, v any) (json.RawMessage, error) {
	out := maps.Clone(r.draft)
	var err error
	if out[key], err = encode(v); err != nil {
		return nil, err
	}
	return encode(out)
}

// encode is v as compact JSON without HTML escapes, so raw values the draft
// holds keep their bytes.
func encode(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}
