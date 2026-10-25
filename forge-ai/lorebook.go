package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// reviewedEntry is what an evaluation or an edit sees of an entry: also its
// number, what else a chat must mention to insert it, and whether it's
// always inserted.
type reviewedEntry struct {
	Entry int `json:"entry"`
	briefEntry
	SecondaryKeys []string `json:"secondary_keys,omitempty"`
	Constant      bool     `json:"constant,omitempty"`
}

// reviewedEntries is what an evaluation or an edit sees of the draft's
// entries, read as leniently as briefEntries. An entry's number is its
// place in the list, counting from 1.
func reviewedEntries(list []json.RawMessage) []reviewedEntry {
	out := []reviewedEntry{}
	for i, raw := range list {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			continue
		}
		e := reviewedEntry{Entry: i + 1, briefEntry: briefEntries([]json.RawMessage{raw})[0]}
		_ = json.Unmarshal(fields["secondary_keys"], &e.SecondaryKeys)
		_ = json.Unmarshal(fields["constant"], &e.Constant)
		out = append(out, e)
	}
	return out
}

// editor is how an action's reply changes the draft in place.
type editor struct {
	keys  []string // the keys the model returns
	merge func(r *run, got map[string]json.RawMessage) (json.RawMessage, error)
}

var lorebookEditor = &editor{keys: []string{"name", "description", "entries", "removed"}, merge: (*run).withEdits}

// entryEdit is one entry the model changes, by number, or adds, without
// one. A property it leaves out keeps its value.
type entryEdit struct {
	Entry         int       `json:"entry"`
	Comment       *string   `json:"comment"`
	Keys          *[]string `json:"keys"`
	SecondaryKeys *[]string `json:"secondary_keys"`
	Content       *string   `json:"content"`
	Constant      *bool     `json:"constant"`
}

// apply is raw with the edit's properties set, and whether any changed.
// Its other properties keep their bytes.
func (ed entryEdit) apply(raw json.RawMessage) (json.RawMessage, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return raw, false, nil
	}
	changed := false
	set := func(key string, v any) error {
		b, err := encode(v)
		if err != nil {
			return err
		}
		var was, now any
		_ = json.Unmarshal(fields[key], &was)
		_ = json.Unmarshal(b, &now)
		if _, ok := fields[key]; !ok || !reflect.DeepEqual(was, now) {
			fields[key], changed = b, true
		}
		return nil
	}
	var err error
	if ed.Comment != nil {
		err = set("comment", strings.TrimSpace(*ed.Comment))
	}
	if err == nil && ed.Keys != nil {
		err = set("keys", trimmedKeys(*ed.Keys))
	}
	if err == nil && ed.SecondaryKeys != nil {
		err = set("secondary_keys", trimmedKeys(*ed.SecondaryKeys))
	}
	if err == nil && ed.Content != nil {
		err = set("content", strings.TrimSpace(*ed.Content))
	}
	if err == nil && ed.Constant != nil {
		err = set("constant", *ed.Constant)
	}
	if err != nil || !changed {
		return raw, false, err
	}
	out, err := encode(fields)
	return out, true, err
}

// trimmedKeys is keys less blanks and repeats, which compare ignoring case.
func trimmedKeys(keys []string) []string {
	out := []string{}
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" && !slices.ContainsFunc(out, func(s string) bool { return strings.EqualFold(s, k) }) {
			out = append(out, k)
		}
	}
	return out
}

// withEdits is the draft with the model's changes: a new name or
// description, entries changed, removed, and added after the rest. An
// unknown entry number is ignored, and a new entry needs keys and content.
func (r *run) withEdits(got map[string]json.RawMessage) (json.RawMessage, error) {
	out := maps.Clone(r.draft)
	changed := false
	for _, k := range []string{"name", "description"} {
		raw, ok := got[k]
		if !ok || string(raw) == "null" {
			continue
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("the model's %s isn't text", k)
		}
		was, err := typed(k, r.draft[k])
		if err != nil {
			return nil, fmt.Errorf("the lorebook's %s: %w", k, err)
		}
		if v = strings.TrimSpace(v); v == was {
			continue
		}
		if out[k], err = encode(v); err != nil {
			return nil, err
		}
		changed = true
	}
	var edits []entryEdit
	var removed []int
	if raw, ok := got["entries"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &edits); err != nil {
			return nil, fmt.Errorf("the model's entries: %w", err)
		}
	}
	if raw, ok := got["removed"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &removed); err != nil {
			return nil, fmt.Errorf("the model's removed isn't a list of entry numbers")
		}
	}
	list, err := r.entries()
	if err != nil {
		return nil, err
	}
	known := func(n int) bool { return n >= 1 && n <= len(list) }
	var added []json.RawMessage
	for _, ed := range edits {
		if ed.Entry == 0 {
			e, err := ed.newEntry()
			if err != nil {
				return nil, err
			}
			if e != nil {
				added = append(added, e)
			}
			continue
		}
		if !known(ed.Entry) {
			continue
		}
		e, ok, err := ed.apply(list[ed.Entry-1])
		if err != nil {
			return nil, err
		}
		list[ed.Entry-1], changed = e, changed || ok
	}
	kept := []json.RawMessage{}
	for i, e := range list {
		if slices.Contains(removed, i+1) {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if len(added) > 0 {
		kept, changed = append(kept, added...), true
	}
	if !changed {
		return encode(r.draft)
	}
	if out["entries"], err = encode(kept); err != nil {
		return nil, err
	}
	return encode(out)
}

// newEntry is the edit as a new entry with the defaults lore-entries gives
// one, or nil without keys or content.
func (ed entryEdit) newEntry() (json.RawMessage, error) {
	if ed.Keys == nil || ed.Content == nil || len(trimmedKeys(*ed.Keys)) == 0 || strings.TrimSpace(*ed.Content) == "" {
		return nil, nil
	}
	blank, err := encode(entry{Enabled: true, InsertionOrder: 100, Position: "before_char", Extensions: map[string]any{}})
	if err != nil {
		return nil, err
	}
	e, _, err := ed.apply(blank)
	return e, err
}
