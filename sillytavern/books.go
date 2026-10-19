package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/inkandale/innkeeper/protocol"
)

// bookFields are the character_book fields SillyTavern doesn't use. They
// live in the world file's extensions.innkeeper.
var bookFields = []string{"description", "scan_depth", "token_budget", "recursive_scanning", "extensions"}

// stashedFields are entry fields SillyTavern drops or rewrites, kept in the
// entry's extensions.innkeeper.
var stashedFields = []string{"id", "name", "position", "priority", "use_regex"}

// setting is a SillyTavern entry field that a character_book entry keeps in
// its extensions. null is what SillyTavern's converter writes for a null.
type setting struct {
	camel, snake string
	null         any
}

// settings mirror convertCharacterBook and convertWorldInfoToCharacterBook.
var settings = []setting{
	{"excludeRecursion", "exclude_recursion", nil},
	{"preventRecursion", "prevent_recursion", false},
	{"delayUntilRecursion", "delay_until_recursion", false},
	{"probability", "probability", nil},
	{"useProbability", "useProbability", false},
	{"depth", "depth", 4.0},
	{"selectiveLogic", "selectiveLogic", 0.0},
	{"outletName", "outlet_name", ""},
	{"group", "group", ""},
	{"groupOverride", "group_override", false},
	{"groupWeight", "group_weight", nil},
	{"scanDepth", "scan_depth", nil},
	{"matchWholeWords", "match_whole_words", nil},
	{"useGroupScoring", "use_group_scoring", false},
	{"automationId", "automation_id", ""},
	{"role", "role", 0.0},
	{"vectorized", "vectorized", false},
	{"sticky", "sticky", nil},
	{"cooldown", "cooldown", nil},
	{"delay", "delay", nil},
	{"matchPersonaDescription", "match_persona_description", false},
	{"matchCharacterDescription", "match_character_description", false},
	{"matchCharacterPersonality", "match_character_personality", false},
	{"matchCharacterDepthPrompt", "match_character_depth_prompt", false},
	{"matchScenario", "match_scenario", false},
	{"matchCreatorNotes", "match_creator_notes", false},
	{"triggers", "triggers", []any{}},
	{"ignoreBudget", "ignore_budget", false},
}

// worldFile is an item of /api/worldinfo/list.
type worldFile struct {
	FileID     string         `json:"file_id"`
	Name       string         `json:"name"`
	Extensions map[string]any `json:"extensions"`
}

// createMu makes picking a free file name and writing it one step.
var createMu sync.Mutex

func (p *plugin) putLorebook(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetPutLorebookParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var book map[string]any
	if err := json.Unmarshal(in.Lorebook, &book); err != nil || book == nil {
		return nil, protocol.InvalidParams(errors.New("lorebook is not a JSON object"))
	}
	files, err := p.worldFiles(ctx)
	if err != nil {
		return nil, err
	}
	id, key := in.RemoteID, in.Key
	if id == "" && key != "" {
		for _, f := range files {
			if f.key() == key {
				id = f.FileID
				break
			}
		}
	}
	if id != "" {
		// /get answers a missing file with an empty one, so /list decides.
		i := slices.IndexFunc(files, func(f worldFile) bool { return f.FileID == id })
		if i < 0 {
			return nil, missingWorld(id)
		}
		if err := p.writeWorld(ctx, id, book, cmp.Or(key, files[i].key())); err != nil {
			return nil, err
		}
		return protocol.TargetPutResult{RemoteID: id}, nil
	}
	// ponytail: covers one plugin process; two workers creating same-named
	// books at once can still pick one file. Needs a lock in SillyTavern.
	createMu.Lock()
	defer createMu.Unlock()
	if files, err = p.worldFiles(ctx); err != nil {
		return nil, err
	}
	name, _ := book["name"].(string)
	want := freeName(files, name)
	if err := p.writeWorld(ctx, want, book, key); err != nil {
		return nil, err
	}
	// The file SillyTavern wrote is the ID, whatever fileID predicted.
	if files, err = p.worldFiles(ctx); err != nil {
		return nil, err
	}
	for _, f := range files {
		if key != "" && f.key() == key || key == "" && f.FileID == want {
			return protocol.TargetPutResult{RemoteID: f.FileID}, nil
		}
	}
	return nil, protocol.Fail(fmt.Sprintf("SillyTavern's world file list doesn't show the new file %q", want), true)
}

// key is the sync key of the push that created the file, or "".
func (f worldFile) key() string {
	k, _ := object(f.Extensions[innkeeperKey])["sync_key"].(string)
	return k
}

// removeLorebook deletes a world file. SillyTavern's delete fails with a 500
// for a missing file, so look for it first.
func (p *plugin) removeLorebook(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetDeleteParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	files, err := p.worldFiles(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(files, func(f worldFile) bool { return f.FileID == in.RemoteID }) {
		return nil, missingWorld(in.RemoteID)
	}
	if err := p.c.call(ctx, "/api/worldinfo/delete", map[string]any{"name": in.RemoteID}, nil); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

func missingWorld(id string) error {
	return protocol.NewError(protocol.CodeRemoteNotFound, id+": no such world file", false)
}

func (p *plugin) worldFiles(ctx context.Context) ([]worldFile, error) {
	var files []worldFile
	err := p.c.call(ctx, "/api/worldinfo/list", map[string]any{}, &files)
	return files, err
}

// writeWorld replaces world file id with book in one write, so a failure
// leaves the old file.
func (p *plugin) writeWorld(ctx context.Context, id string, book map[string]any, key string) error {
	stash := map[string]any{}
	if key != "" {
		stash["sync_key"] = key
	}
	for _, k := range bookFields {
		if v, ok := book[k]; ok {
			stash[k] = v
		}
	}
	entries := map[string]any{}
	list, _ := book["entries"].([]any)
	for i, e := range list {
		entries[strconv.Itoa(i)] = toWorldEntry(object(e), i)
	}
	data := map[string]any{"entries": entries, "extensions": map[string]any{innkeeperKey: stash}}
	if name, _ := book["name"].(string); name != "" {
		data["name"] = name
	}
	return p.c.call(ctx, "/api/worldinfo/edit", map[string]any{"name": id, "data": data}, nil)
}

// freeName returns the file ID for a new world file named name, one that no
// file has. /edit overwrites a file without asking.
func freeName(files []worldFile, name string) string {
	base := fileID(truncate(cmp.Or(name, "Lorebook"), 200))
	if base == "" {
		base = "Lorebook"
	}
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s %d", base, n)
		}
		// Case-insensitive, since the server's file system may be.
		if !slices.ContainsFunc(files, func(f worldFile) bool { return strings.EqualFold(f.FileID, id) }) {
			return id
		}
	}
}

// truncate cuts s to at most n bytes, on a character boundary.
func truncate(s string, n int) string {
	for len(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// reservedName is sanitize-filename's Windows names. JavaScript's "." skips
// line terminators.
var reservedName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\.[^\n\r\x{2028}\x{2029}]*)?$`)

// fileID is the file ID SillyTavern's /list gives a world file named name:
// sanitize-filename 1.6.3's sanitize(name + ".json"), without ".json", or ""
// for a name whose file /list skips or that can't be written.
func fileID(name string) string {
	s := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/?<>\:*|"`, r) || r < 0x20 || r >= 0x80 && r <= 0x9f {
			return -1
		}
		return r
	}, name+".json")
	if reservedName.MatchString(s) {
		s = ""
	}
	// /list skips a file without the extension, and path.parse(".json") has none.
	if s = truncate(s, 255); s == ".json" || !strings.HasSuffix(s, ".json") {
		return ""
	}
	return strings.TrimSuffix(s, ".json")
}

// toWorldEntry is convertCharacterBook's entry, with only the settings the
// entry has, so a file read back before the browser saves it is unchanged.
func toWorldEntry(e map[string]any, index int) map[string]any {
	ext := map[string]any{}
	for k, v := range object(e["extensions"]) {
		if k != innkeeperKey {
			ext[k] = v
		}
	}
	comment, _ := e["comment"].(string)
	name, _ := e["name"].(string)
	comment = cmp.Or(comment, name)
	var position any = topPosition(e["position"])
	if v := ext["position"]; v != nil {
		position = v
	}
	content, _ := e["content"].(string)
	displayIndex := ext["display_index"]
	if displayIndex == nil {
		displayIndex = index
	}
	out := map[string]any{
		"uid":          index,
		"displayIndex": displayIndex,
		"key":          listOr(e["keys"]),
		"keysecondary": listOr(e["secondary_keys"]),
		"comment":      comment,
		"content":      content,
		"constant":     e["constant"] == true,
		"selective":    e["selective"] == true,
		"disable":      e["enabled"] == false, // SillyTavern's import disables it; silently hidden lore is worse
		"addMemo":      comment != "",
		"position":     position,
	}
	if v, ok := e["insertion_order"]; ok {
		out["order"] = v
	}
	if v, ok := e["case_sensitive"].(bool); ok {
		out["caseSensitive"] = v
	} else if v, ok := ext["case_sensitive"]; ok && v != nil {
		out["caseSensitive"] = v
	}
	for _, s := range settings {
		if v, ok := ext[s.snake]; ok && v != nil {
			out[s.camel] = v
		}
	}
	stash := map[string]any{}
	for _, k := range stashedFields {
		if v, ok := e[k]; ok {
			stash[k] = v
		}
	}
	orig := map[string]any{}
	for k, v := range e {
		orig[k] = v
	}
	if _, ok := e["extensions"]; ok {
		origExt := map[string]any{}
		for k, v := range ext {
			origExt[k] = v
		}
		orig["extensions"] = origExt
	}
	ext[innkeeperKey] = stash
	out["extensions"] = ext
	stash["fields"] = readBackDiffs(orig, out)
	return out
}

// readBackDiffs lists the fields of orig that fromWorldEntry(st) reads back
// differently, each with a hash of how it reads back and the pushed value, or
// no value if orig lacks it. content is left out so it is never copied:
// SillyTavern holds any string as pushed, so this only skips a missing or
// non-string content, which reads back as "".
func readBackDiffs(orig, st map[string]any) map[string]any {
	// Through JSON, so numbers compare as a stored file's do.
	var stored map[string]any
	raw, _ := json.Marshal(st)
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	sent := fromWorldEntry(stored)
	out := map[string]any{}
	for _, m := range []map[string]any{sent, orig} {
		for k := range m {
			if k == "content" || jsonHash(sent[k]) == jsonHash(orig[k]) {
				continue
			}
			rec := map[string]any{"sent": jsonHash(sent[k])}
			if v, ok := orig[k]; ok {
				rec["value"] = v
			}
			out[k] = rec
		}
	}
	return out
}

func listOr(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func (p *plugin) getLorebook(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetGetParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	files, err := p.worldFiles(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(files, func(f worldFile) bool { return f.FileID == in.RemoteID }) {
		return nil, missingWorld(in.RemoteID)
	}
	var data map[string]any
	if err := p.c.call(ctx, "/api/worldinfo/get", map[string]any{"name": in.RemoteID}, &data); err != nil {
		return nil, err
	}
	stash, fromInnkeeper := object(data["extensions"])[innkeeperKey].(map[string]any)
	book := map[string]any{}
	// A book pushed without a name reads back without one while the file has
	// none; a book made in SillyTavern is named by its file.
	if name, _ := data["name"].(string); name != "" || !fromInnkeeper {
		book["name"] = cmp.Or(name, in.RemoteID)
	}
	for _, k := range bookFields {
		if v, ok := stash[k]; ok {
			book[k] = v
		}
	}
	type item struct {
		e   map[string]any
		raw []byte
	}
	var stored []item
	for _, e := range object(data["entries"]) {
		raw, _ := json.Marshal(e)
		stored = append(stored, item{object(e), raw})
	}
	// uid is the index at push; the browser gives an added entry the lowest
	// free uid. The JSON breaks ties between duplicates, so the order can't flap.
	slices.SortFunc(stored, func(a, b item) int {
		return cmp.Or(cmp.Compare(number(a.e["uid"]), number(b.e["uid"])), bytes.Compare(a.raw, b.raw))
	})
	entries := make([]any, len(stored))
	for i, it := range stored {
		entries[i] = asPushed(it.e)
	}
	book["entries"] = entries
	raw, err := json.Marshal(book)
	if err != nil {
		return nil, err
	}
	return protocol.TargetGetLorebookResult{Lorebook: raw}, nil
}

func number(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 1 << 50
}

// topPosition is the number SillyTavern gives a character_book position.
func topPosition(v any) float64 {
	if v == "before_char" {
		return 0
	}
	return 1
}

// fromWorldEntry is convertWorldInfoToCharacterBook's entry for st, with the
// stashed fields restored. Settings come back only where st has them.
func fromWorldEntry(st map[string]any) map[string]any {
	ext := map[string]any{}
	for k, v := range object(st["extensions"]) {
		ext[k] = v
	}
	stash, pushed := ext[innkeeperKey].(map[string]any)
	delete(ext, innkeeperKey)
	e := map[string]any{"enabled": st["disable"] != true, "extensions": ext}
	for from, to := range map[string]string{"key": "keys", "keysecondary": "secondary_keys", "comment": "comment",
		"content": "content", "constant": "constant", "selective": "selective", "order": "insertion_order"} {
		if v, ok := st[from]; ok {
			e[to] = v
		}
	}
	pos, hasPos := st["position"].(float64)
	_, extPos := ext["position"]
	position := any("after_char")
	if hasPos && pos == 0 {
		position = "before_char"
	}
	// Restore the pushed position while it still gives SillyTavern's.
	if pushed && (extPos || hasPos && topPosition(stash["position"]) == pos) {
		position = stash["position"]
	}
	if position != nil {
		e["position"] = position
	}
	if hasPos && (extPos || pos != topPosition(position)) {
		ext["position"] = pos
	}
	if v, ok := st["displayIndex"]; ok {
		if _, in := ext["display_index"]; in || number(v) != number(st["uid"]) {
			ext["display_index"] = v
		}
	}
	if v, ok := st["caseSensitive"]; ok {
		if _, in := ext["case_sensitive"]; in || v == nil {
			ext["case_sensitive"] = v
		} else {
			e["case_sensitive"] = v
		}
	}
	for _, s := range settings {
		if v, ok := st[s.camel]; ok {
			if v == nil {
				v = s.null
			}
			ext[s.snake] = v
		}
	}
	// An entry added in the browser has no id; its uid could clash with a pushed one.
	for _, k := range []string{"id", "name", "priority", "use_regex"} {
		if v, ok := stash[k]; ok {
			e[k] = v
		}
	}
	return e
}

// asPushed is fromWorldEntry(st) with each field readBackDiffs recorded given
// back as pushed while it still reads back as it did after the push, so a
// field SillyTavern can't hold, or fills in with a default, reads back
// unchanged.
func asPushed(st map[string]any) map[string]any {
	e := fromWorldEntry(st)
	for k, r := range object(object(object(st["extensions"])[innkeeperKey])["fields"]) {
		rec := object(r)
		if jsonHash(e[k]) != rec["sent"] {
			continue
		}
		if v, ok := rec["value"]; ok {
			e[k] = v
		} else {
			delete(e, k)
		}
	}
	return e
}

// jsonHash is a short hash of v's JSON, enough to tell whether a field changed.
func jsonHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func (p *plugin) listLorebooks(ctx context.Context) (protocol.TargetListResult, error) {
	files, err := p.worldFiles(ctx)
	if err != nil {
		return protocol.TargetListResult{}, err
	}
	items := make([]protocol.TargetItem, 0, len(files))
	for _, f := range files {
		items = append(items, protocol.TargetItem{RemoteID: f.FileID, Name: f.Name})
	}
	return protocol.TargetListResult{Items: items}, nil
}
