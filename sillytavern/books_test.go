package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

func testBook(name string, entries ...map[string]any) map[string]any {
	list := []any{}
	for _, e := range entries {
		list = append(list, e)
	}
	return map[string]any{"name": name, "description": "Smithing.", "scan_depth": 6.0, "token_budget": 500.0,
		"recursive_scanning": true, "extensions": map[string]any{"x": 1.0}, "entries": list}
}

// testEntry has every field the mapping writes back, so it reads back unchanged.
func testEntry(id any, name, content string, order float64) map[string]any {
	return map[string]any{"id": id, "name": name, "comment": name + " note", "keys": []any{name},
		"secondary_keys": []any{}, "content": content, "enabled": true, "constant": false, "selective": false,
		"insertion_order": order, "position": "after_char", "extensions": map[string]any{}}
}

func putBookErr(p *plugin, in protocol.TargetPutLorebookParams) (string, error) {
	raw, _ := json.Marshal(in)
	res, err := p.putLorebook(context.Background(), raw)
	if err != nil {
		return "", err
	}
	return res.(protocol.TargetPutResult).RemoteID, nil
}

func putBook(t *testing.T, p *plugin, key string, book map[string]any) string {
	t.Helper()
	id, err := putBookErr(p, protocol.TargetPutLorebookParams{Key: key, Lorebook: mustJSON(t, book)})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func getBook(t *testing.T, p *plugin, id string) map[string]any {
	t.Helper()
	res, err := p.getLorebook(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id}))
	if err != nil {
		t.Fatal(err)
	}
	var book map[string]any
	if err := json.Unmarshal(res.(protocol.TargetGetLorebookResult).Lorebook, &book); err != nil {
		t.Fatal(err)
	}
	return book
}

// normal is v as it reads back from JSON.
func normal(t *testing.T, v any) any {
	var out any
	if err := json.Unmarshal(mustJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCreateWorldFilePicksAFreeName(t *testing.T) {
	f := newFake(t)
	f.worlds["Forge lore"] = map[string]any{"entries": map[string]any{}}
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "anvil", "It rings.", 1)))
	if id != "Forge lore 2" {
		t.Fatalf("created %q, want Forge lore 2", id)
	}
	data := f.worlds[id]
	stash := object(object(data["extensions"])[innkeeperKey])
	if data["name"] != "Forge lore" || stash["sync_key"] != "k" || stash["description"] != "Smithing." ||
		stash["scan_depth"] != 6.0 || stash["token_budget"] != 500.0 || stash["recursive_scanning"] != true ||
		object(stash["extensions"])["x"] != 1.0 {
		t.Fatalf("stored %v", data)
	}
	e := object(object(data["entries"])["0"])
	if e["uid"] != 0.0 || e["displayIndex"] != 0.0 || e["comment"] != "anvil note" || e["position"] != 1.0 ||
		e["disable"] != false || e["order"] != 1.0 {
		t.Fatalf("stored entry %v", e)
	}
}

func TestCreateWithoutANameIsLorebook(t *testing.T) {
	f := newFake(t)
	book := testBook("")
	delete(book, "name")
	p := newPlugin(f)
	id := putBook(t, p, "k", book)
	if id != "Lorebook" {
		t.Fatalf("created %q", id)
	}
	if got := getBook(t, p, id); got["name"] != nil {
		t.Fatalf("read back the file name as the name: %v", got["name"])
	}
}

func TestPutWithAKnownKeyReplacesThatFile(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "anvil", "It rings.", 1)))
	again := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "anvil", "It sings.", 1)))
	if again != id || len(f.worlds) != 1 {
		t.Fatalf("re-run made %q; %d files", again, len(f.worlds))
	}
	if object(object(f.worlds[id]["entries"])["0"])["content"] != "It sings." {
		t.Fatalf("file not replaced: %v", f.worlds[id])
	}
}

func TestReplaceKeepsTheFileName(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "a", "A", 1), testEntry(2.0, "b", "B", 2)))
	again, err := putBookErr(p, protocol.TargetPutLorebookParams{RemoteID: id,
		Lorebook: mustJSON(t, testBook("Smithy", testEntry(1.0, "c", "C", 1)))})
	if err != nil || again != id || len(f.worlds) != 1 {
		t.Fatalf("replace: %q, %v, %d files", again, err, len(f.worlds))
	}
	data := f.worlds[id]
	if data["name"] != "Smithy" || len(object(data["entries"])) != 1 ||
		object(object(data["extensions"])[innkeeperKey])["sync_key"] != "k" {
		t.Fatalf("after replace: %v", data)
	}
}

func TestAMissingFileIsNotFound(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	if _, err := putBookErr(p, protocol.TargetPutLorebookParams{RemoteID: "gone",
		Lorebook: mustJSON(t, testBook("Gone"))}); !httpkit.NotFound(err) || len(f.worlds) != 0 {
		t.Fatalf("put to a missing file: %v; %d files", err, len(f.worlds))
	}
	if _, err := p.getLorebook(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: "gone"})); !httpkit.NotFound(err) {
		t.Fatalf("get of a missing file: %v", err)
	}
}

func TestWorldFileRoundTrips(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	a := testEntry("anvil-id", "anvil", "It rings.", 5)
	a["priority"] = 3.0
	a["position"] = "before_char"
	a["case_sensitive"] = true
	a["use_regex"] = true // SillyTavern has no such field
	b := testEntry(7.0, "forge", "It burns.", 5)
	b["extensions"] = map[string]any{"position": 4.0, "depth": 2.0, "probability": 50.0, "useProbability": true,
		"group_weight": 20.0, "custom": "kept"}
	c := testEntry(nil, "tongs", "They grip.", 1)
	delete(c, "id")
	book := testBook("Forge lore", a, b, c)
	id := putBook(t, p, "k", book)
	st := object(object(f.worlds[id]["entries"])["1"])
	if st["position"] != 4.0 || st["depth"] != 2.0 || st["probability"] != 50.0 || st["groupWeight"] != 20.0 {
		t.Fatalf("stored %v", st)
	}
	if got := getBook(t, p, id); !reflect.DeepEqual(got, normal(t, book)) {
		t.Fatalf("read back\n%v\nwant\n%v", mustJSON(t, got), mustJSON(t, book))
	}
}

func TestAnEntryReadsBackAsPushed(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	// No secondary_keys, comment, or extensions, and a field SillyTavern has no place for.
	e := map[string]any{"keys": []any{"anvil"}, "content": "It rings.", "name": "anvil", "mode": "normal"}
	book := map[string]any{"entries": []any{e}}
	id := putBook(t, p, "k", book)
	if got := getBook(t, p, id); !reflect.DeepEqual(got, normal(t, book)) {
		t.Fatalf("read back\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, book))
	}
	if stash := mustJSON(t, object(object(object(f.worlds[id]["entries"])["0"])["extensions"])[innkeeperKey]); strings.Contains(string(stash), "content") || strings.Contains(string(stash), "It rings.") {
		t.Fatalf("the stash holds the content: %s", stash)
	}
	// Fields changed in SillyTavern read back as SillyTavern has them,
	// stashed ones included.
	st := object(object(f.worlds[id]["entries"])["0"])
	st["content"], st["comment"], st["keysecondary"] = "It sings.", "Hammer note", []any{"x"}
	got := object(getBook(t, p, id)["entries"].([]any)[0])
	if got["content"] != "It sings." || got["comment"] != "Hammer note" || !reflect.DeepEqual(got["secondary_keys"], []any{"x"}) ||
		got["mode"] != "normal" {
		t.Fatalf("after edits: entry %v", got)
	}
	// The same after a browser save, which adds defaults first.
	id = putBook(t, p, "k", book)
	st = object(object(f.worlds[id]["entries"])["0"])
	addMissingWorldInfoFields(st)
	st["disable"], st["keysecondary"] = true, []any{"y"}
	got = object(getBook(t, p, id)["entries"].([]any)[0])
	if got["enabled"] != false || !reflect.DeepEqual(got["secondary_keys"], []any{"y"}) || got["mode"] != "normal" {
		t.Fatalf("after a browser save: entry %v", got)
	}
}

// addMissingWorldInfoFields is what SillyTavern's browser does to every entry
// before it saves a world file.
func addMissingWorldInfoFields(e map[string]any) {
	template := map[string]any{"key": []any{}, "keysecondary": []any{}, "comment": "", "content": "",
		"constant": false, "vectorized": false, "selective": true, "selectiveLogic": 0.0, "addMemo": false,
		"order": 100.0, "position": 0.0, "disable": false, "ignoreBudget": false, "excludeRecursion": false,
		"preventRecursion": false, "matchPersonaDescription": false, "matchCharacterDescription": false,
		"matchCharacterPersonality": false, "matchCharacterDepthPrompt": false, "matchScenario": false,
		"matchCreatorNotes": false, "delayUntilRecursion": 0.0, "probability": 100.0, "useProbability": true,
		"depth": 4.0, "outletName": "", "group": "", "groupOverride": false, "groupWeight": 100.0,
		"scanDepth": nil, "caseSensitive": nil, "matchWholeWords": nil, "useGroupScoring": nil,
		"automationId": "", "role": 0.0, "sticky": nil, "cooldown": nil, "delay": nil, "triggers": []any{}}
	for k, v := range template {
		if _, ok := e[k]; !ok {
			e[k] = v
		}
	}
}

func TestABrowserSaveReadsBackWithTemplateDefaults(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	a := testEntry(4.0, "anvil", "It rings.", 2)
	a["priority"] = 3.0
	id := putBook(t, p, "k", testBook("Forge lore", a))
	st := object(object(f.worlds[id]["entries"])["0"])
	addMissingWorldInfoFields(st)
	st["position"] = 0.0 // the owner moves it before the character
	e := object(getBook(t, p, id)["entries"].([]any)[0])
	ext := object(e["extensions"])
	if e["id"] != 4.0 || e["name"] != "anvil" || e["priority"] != 3.0 || e["position"] != "before_char" {
		t.Fatalf("entry %v", e)
	}
	if ext["probability"] != 100.0 || ext["useProbability"] != true || ext["match_scenario"] != false ||
		ext["delay_until_recursion"] != 0.0 || ext["group_weight"] != 100.0 || ext[innkeeperKey] != nil {
		t.Fatalf("extensions %v", ext)
	}
	if _, ok := ext["position"]; ok {
		t.Fatalf("extensions.position is implied by position: %v", ext)
	}
}

func TestAnEntryAddedInTheBrowserHasNoID(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "a", "A", 1)))
	added := map[string]any{"uid": 1.0, "displayIndex": 1.0, "key": []any{"b"}, "content": "B"}
	addMissingWorldInfoFields(added)
	object(f.worlds[id]["entries"])["1"] = added
	entries := getBook(t, p, id)["entries"].([]any)
	if len(entries) != 2 || object(entries[1])["content"] != "B" {
		t.Fatalf("entries %v", entries)
	}
	if _, ok := object(entries[1])["id"]; ok {
		t.Fatalf("an entry added in the browser has an id: %v", entries[1])
	}
}

func TestListLorebooks(t *testing.T) {
	f := newFake(t)
	f.worlds["Dwarves"] = map[string]any{"entries": map[string]any{}}
	f.worlds["Elves 2"] = map[string]any{"name": "Elves", "entries": map[string]any{}}
	res, err := newPlugin(f).listLorebooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.TargetItem{{RemoteID: "Dwarves", Name: "Dwarves"}, {RemoteID: "Elves 2", Name: "Elves"}}
	if !reflect.DeepEqual(res.Items, want) {
		t.Fatalf("items %v", res.Items)
	}
}

// Cases checked against sanitize-filename 1.6.3 and Node's path.parse.
func TestFileIDIsSillyTavernsFileName(t *testing.T) {
	for name, want := range map[string]string{"Forge lore": "Forge lore", `A/B:C*?"<>|\`: "ABC",
		"bell\x07": "bell", "x\u0085y": "xy", "con": "", "LPT1": "", "con 2": "con 2", "con.a\u2028b": "con.a\u2028b",
		"..": "..", "???": "", " x ": " x ", "Foo.": "Foo.", strings.Repeat("a", 251): ""} {
		if got := fileID(name); got != want {
			t.Errorf("fileID(%q) = %q, want %q", name, got, want)
		}
	}
}

// The remote ID is the file /list shows, not the name the plugin asked for.
func TestCreateTakesTheIDFromTheList(t *testing.T) {
	f := newFake(t)
	f.editFile = strings.ToLower
	if id := putBook(t, newPlugin(f), "k", testBook("Forge lore")); id != "forge lore" {
		t.Fatalf("created %q, want forge lore", id)
	}
	f.editFile = func(string) string { return "" }
	_, err := putBookErr(newPlugin(f), protocol.TargetPutLorebookParams{Key: "k2", Lorebook: mustJSON(t, testBook("Smithy"))})
	retryable(t, err)
}

func TestAnEntryWithoutEnabledIsPushedEnabled(t *testing.T) {
	f := newFake(t)
	e := testEntry(1.0, "anvil", "It rings.", 1)
	delete(e, "enabled")
	id := putBook(t, newPlugin(f), "k", testBook("Forge lore", e))
	if st := object(object(f.worlds[id]["entries"])["0"]); st["disable"] != false {
		t.Fatalf("stored %v", st)
	}
}

func TestDisplayIndexRoundTrips(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	a := testEntry(1.0, "anvil", "It rings.", 1)
	a["extensions"] = map[string]any{"display_index": 5.0}
	book := testBook("Forge lore", a, testEntry(2.0, "forge", "It burns.", 2))
	id := putBook(t, p, "k", book)
	if st := object(object(f.worlds[id]["entries"])["0"]); st["displayIndex"] != 5.0 || st["uid"] != 0.0 {
		t.Fatalf("stored %v", st)
	}
	if got := getBook(t, p, id); !reflect.DeepEqual(got, normal(t, book)) {
		t.Fatalf("read back\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, book))
	}
}

// Dragging entries in the browser changes displayIndex, not the entries' order.
func TestABrowserReorderReadsBackAsDisplayIndex(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "anvil", "A", 1), testEntry(2.0, "forge", "F", 2)))
	entries := object(f.worlds[id]["entries"])
	object(entries["0"])["displayIndex"], object(entries["1"])["displayIndex"] = 1.0, 0.0
	got := getBook(t, p, id)["entries"].([]any)
	a, b := object(got[0]), object(got[1])
	if a["name"] != "anvil" || object(a["extensions"])["display_index"] != 1.0 ||
		b["name"] != "forge" || object(b["extensions"])["display_index"] != 0.0 {
		t.Fatalf("entries %v", got)
	}
}

func TestAnEntryWithoutContentIsPushedEmpty(t *testing.T) {
	f := newFake(t)
	e := testEntry(1.0, "anvil", "", 1)
	delete(e, "content")
	id := putBook(t, newPlugin(f), "k", testBook("Forge lore", e))
	if st := object(object(f.worlds[id]["entries"])["0"]); st["content"] != "" {
		t.Fatalf("stored %v", st)
	}
}

// A uid gap left by a browser deletion isn't a reorder.
func TestADeletedEntryLeavesNoDisplayIndex(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "a", "A", 1), testEntry(2.0, "b", "B", 2), testEntry(3.0, "c", "C", 3)))
	delete(object(f.worlds[id]["entries"]), "1")
	got := getBook(t, p, id)["entries"].([]any)
	if len(got) != 2 {
		t.Fatalf("entries %v", got)
	}
	for _, e := range got {
		if _, ok := object(object(e)["extensions"])["display_index"]; ok {
			t.Fatalf("display_index in %v", e)
		}
	}
}

func TestDeleteWorldFile(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := putBook(t, p, "k", testBook("Forge lore", testEntry(1.0, "anvil", "It rings.", 1)))
	del := mustJSON(t, protocol.TargetDeleteParams{RemoteID: id})
	if _, err := p.removeLorebook(context.Background(), del); err != nil {
		t.Fatal(err)
	}
	if len(f.worlds) != 0 {
		t.Fatalf("worlds = %v", f.worlds)
	}
	if _, err := p.removeLorebook(context.Background(), del); !httpkit.NotFound(err) {
		t.Fatalf("deleting it again: %v, want not found", err)
	}
}
