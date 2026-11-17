package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
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

// entryEdit is one entry the model changes, by number, or adds, without
// one. A property it leaves out keeps its value.
type entryEdit struct {
	Entry         int
	Comment       *string
	Keys          *[]string
	SecondaryKeys *[]string
	Content       *string
	Constant      *bool
}

// parseEntryText reads an entry the model wrote in the entry-format
// template's layout: header lines, such as "Keys: a, b", then the content.
// A header it leaves out is nil, so an edit keeps that property.
func parseEntryText(text string) entryEdit {
	var ed entryEdit
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	i := 0
	for ; i < len(lines); i++ {
		name, value, ok := strings.Cut(lines[i], ":")
		if !ok || !ed.setHeader(name, strings.Trim(value, "*_ \t")) {
			break
		}
	}
	if content := strings.TrimSpace(strings.Join(lines[i:], "\n")); content != "" {
		ed.Content = &content
	}
	return ed
}

// setHeader sets the property an entry header names, or reports false for
// a line that isn't a header, which starts the content. A header left empty
// sets nothing.
func (ed *entryEdit) setHeader(name, value string) bool {
	var set func()
	switch strings.ToLower(strings.Trim(name, "*_ ")) {
	case "title":
		set = func() { ed.Comment = &value }
	case "keys":
		set = func() { keys := splitKeys(value); ed.Keys = &keys }
	case "secondary keys":
		set = func() { keys := splitKeys(value); ed.SecondaryKeys = &keys }
	case "constant":
		set = func() {
			constant := strings.EqualFold(value, "yes") || strings.EqualFold(value, "true")
			ed.Constant = &constant
		}
	default:
		return false
	}
	if value != "" {
		set()
	}
	return true
}

// splitKeys is a comma-separated list of keys, trimmed.
func splitKeys(s string) []string {
	return trimmedKeys(strings.Split(s, ","))
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

// lorebookEdit is a change to a lorebook: a new name or description,
// entries changed or added, and entries removed, by number.
type lorebookEdit struct {
	Name, Description *string
	Entries           []entryEdit
	Removed           []int
}

// withEdits is the draft with ed applied: entries changed, removed, and
// added after the rest. An unknown entry number is ignored, and a new entry
// needs keys and content.
func (r *run) withEdits(ed lorebookEdit) (json.RawMessage, error) {
	out := maps.Clone(r.draft)
	changed := false
	for k, v := range map[string]*string{"name": ed.Name, "description": ed.Description} {
		if v == nil {
			continue
		}
		was, err := typed(k, r.draft[k])
		if err != nil {
			return nil, fmt.Errorf("the lorebook's %s: %w", k, err)
		}
		text := strings.TrimSpace(*v)
		if text == was {
			continue
		}
		if out[k], err = encode(text); err != nil {
			return nil, err
		}
		changed = true
	}
	edits, removed := ed.Entries, ed.Removed
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

// lorebookPlan is revise-lorebook's plan: a new name, a note on what to
// change in the description, a note per entry to change or, without an
// entry number, to add, and the entries to delete.
type lorebookPlan struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Entries     []struct {
		Entry int    `json:"entry"`
		Note  string `json:"note"`
	} `json:"entries"`
	Removed []int `json:"removed"`
}

// reviseLorebook plans the edit in one call, then writes the description
// and each changed or new entry in a call of its own. Deletions come last,
// so the plan's entry numbers hold for every step.
func (srv *server) reviseLorebook(ctx context.Context, in *instance, r *run) (*protocol.ActionRunResult, error) {
	_ = in.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Total: 1, Message: "Planning " + r.Label})
	var plan lorebookPlan
	planning := *r
	planning.template = "revise-lorebook-plan"
	planning.takes = func(r *run, text string) (json.RawMessage, error) {
		obj, err := parseObject(text)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(obj)
		if err == nil {
			err = json.Unmarshal(b, &plan)
		}
		if err != nil {
			return nil, fmt.Errorf("the model's plan: %w", err)
		}
		return encode(r.draft)
	}
	planned, err := srv.perform(ctx, in, &planning, true, noProgress)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if name := strings.TrimSpace(plan.Name); name != "" {
		proposal, err := r.withEdits(lorebookEdit{Name: &name})
		if err == nil {
			err = json.Unmarshal(proposal, &r.draft)
		}
		if err != nil {
			return nil, err
		}
	}
	var steps []step
	if note := strings.TrimSpace(plan.Description); note != "" {
		steps = append(steps, step{label: "Description", field: "description", note: note, takes: func(r *run, text string) (json.RawMessage, error) {
			return r.withEdits(lorebookEdit{Description: &text})
		}})
	}
	list, err := r.entries()
	if err != nil {
		return nil, err
	}
	for _, e := range plan.Entries {
		note := strings.TrimSpace(e.Note)
		if note == "" || e.Entry < 0 || e.Entry > len(list) {
			continue
		}
		label := "New entry"
		if e.Entry > 0 {
			label = fmt.Sprintf("Entry %d", e.Entry)
		}
		steps = append(steps, step{label: label, note: note, takes: func(r *run, text string) (json.RawMessage, error) {
			ed := parseEntryText(text)
			ed.Entry = e.Entry
			return r.withEdits(lorebookEdit{Entries: []entryEdit{ed}})
		}})
	}
	for i := range steps {
		steps[i].message = fmt.Sprintf("%s: %s (%d of %d)", r.Label, steps[i].label, i+1, len(steps))
	}
	res, err := srv.runSteps(ctx, in, r, steps, []string{"Plan:\n" + planned.Raw})
	if err != nil {
		return nil, err
	}
	res.Proposal, err = r.withEdits(lorebookEdit{Removed: plan.Removed})
	return res, err
}
