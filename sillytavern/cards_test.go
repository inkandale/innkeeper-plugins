package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

func newPlugin(f *fake) *plugin { return &plugin{c: newTestClient(f, "account")} }

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testCard(fields map[string]any) json.RawMessage {
	data := map[string]any{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hail.",
		"creator_notes": "Gruff.", "tags": []any{"dwarf"}, "character_version": "2",
		"extensions": map[string]any{"depth_prompt": map[string]any{"depth": 4.0, "prompt": "Grumble."}}}
	for k, v := range fields {
		data[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
	return raw
}

// avatarFile writes a one-pixel PNG where a blob store would keep it, named by sha.
func avatarFile(t *testing.T, sha string) string {
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Gray{Y: sha[0]})
	path := filepath.Join(t.TempDir(), sha)
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func putErr(p *plugin, in protocol.TargetPutParams) error {
	raw, _ := json.Marshal(in)
	_, err := p.put(context.Background(), raw)
	return err
}

func put(t *testing.T, p *plugin, in protocol.TargetPutParams) string {
	t.Helper()
	res, err := p.put(context.Background(), mustJSON(t, in))
	if err != nil {
		t.Fatal(err)
	}
	return res.(protocol.TargetPutResult).RemoteID
}

// stored returns a character's stored JSON, its data, and its extensions.
func stored(f *fake, id string) (c, data, ext map[string]any) {
	c = f.characters[id]
	data = object(c["data"])
	return c, data, object(data["extensions"])
}

func TestCreateCharacter(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"character_book": map[string]any{"entries": []any{}}}),
		Key: "1:2:3", Avatar: avatarFile(t, "aaa"), LorebookRemoteIDs: []string{"Dwarves"}})
	if id != "Brakka.png" {
		t.Fatalf("remote id = %q", id)
	}
	raw, _, err := card.Decode(f.images[id])
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &imported)
	ext := object(imported.Data["extensions"])
	if fmt.Sprint(ext[innkeeperKey]) != "map[avatar:aaa key:1:2:3]" || ext["world"] != "Dwarves" ||
		ext["depth_prompt"] == nil || imported.Data["character_book"] != nil {
		t.Fatalf("imported card = %v", imported.Data)
	}
	if c, _, _ := stored(f, id); c["first_mes"] != "Hail." || len(f.merges) != 0 || f.avatars[id] != 0 {
		t.Fatalf("character = %v, %d merges, %d avatar uploads", c, len(f.merges), f.avatars[id])
	}
	// SillyTavern names a second Brakka Brakka1.png.
	id = put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "4:5:6"})
	if _, _, ext := stored(f, id); id != "Brakka1.png" || ext["world"] != "" || fmt.Sprint(ext[innkeeperKey]) != "map[key:4:5:6]" {
		t.Fatalf("second create: %q, extensions %v", id, ext)
	}
}

func TestAFailedImportFails(t *testing.T) {
	f := newFake(t)
	f.badImport = true
	if err := putErr(newPlugin(f), protocol.TargetPutParams{Card: testCard(nil), Key: "k"}); err == nil || len(f.characters) != 0 {
		t.Fatalf("got %v and %d characters", err, len(f.characters))
	}
}

func TestCreateKeepsANameSillyTavernCleans(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"name": "UKCAT/UCAT"}), Key: "k"})
	if c, data, _ := stored(f, id); id != "UKCATUCAT.png" || data["name"] != "UKCAT/UCAT" || c["name"] != "UKCAT/UCAT" {
		t.Fatalf("%q: name %v, data.name %v", id, c["name"], data["name"])
	}
}

// A re-run after a crash finds the character its key made, and brings it up to date.
func TestPutWithAKnownKeyReusesTheCharacter(t *testing.T) {
	for _, lazy := range []bool{false, true} {
		t.Run(fmt.Sprint("lazy=", lazy), func(t *testing.T) {
			f := newFake(t)
			p := newPlugin(f)
			first := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k1"})
			put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k2"})
			f.lazy = lazy
			again := put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"first_mes": "Well met."}), Key: "k1"})
			if c, _, _ := stored(f, first); again != first || len(f.characters) != 2 || c["first_mes"] != "Well met." {
				t.Fatalf("re-run made %s; %d characters; first_mes %v", again, len(f.characters), c["first_mes"])
			}
		})
	}
}

func TestUpdateCharacter(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	avatar := avatarFile(t, "aaa")
	id := put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"nickname": "Bra"}), Key: "k",
		Avatar: avatar, LorebookRemoteIDs: []string{"Dwarves"}})
	// What a browser save adds.
	_, data, ext := stored(f, id)
	ext["fav"], ext["talkativeness"] = true, "0.5"
	data["character_book"] = map[string]any{"entries": []any{}}

	put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"first_mes": "Well met.", "extensions": map[string]any{}}),
		RemoteID: id, Avatar: avatar})
	m := f.merges[len(f.merges)-1]
	if m["avatar"] != id || m["first_mes"] != "Well met." || m["creatorcomment"] != "Gruff." || m["personality"] != "" ||
		fmt.Sprint(m["tags"]) != "[dwarf]" {
		t.Fatalf("merge top level = %v", m)
	}
	sent := object(m["data"])
	for _, k := range v2Fields {
		if _, ok := sent[k]; !ok {
			t.Errorf("merge didn't send data.%s", k)
		}
	}
	sentExt := object(sent["extensions"])
	if sent["personality"] != "" || sent["nickname"] != unset || sent["source"] != nil || sent["character_book"] != unset ||
		sentExt["depth_prompt"] != unset || sentExt["talkativeness"] != unset || sentExt["world"] != "" {
		t.Fatalf("merge data = %v", sent)
	}
	if _, ok := sentExt["fav"]; ok {
		t.Fatalf("merge sent fav: %v", sentExt)
	}
	c, data, ext := stored(f, id)
	if c["first_mes"] != "Well met." || data["first_mes"] != "Well met." || data["character_book"] != nil ||
		ext["depth_prompt"] != nil || ext["fav"] != true || fmt.Sprint(ext[innkeeperKey]) != "map[avatar:aaa key:k]" {
		t.Fatalf("character = %v", c)
	}
	res, err := p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.(protocol.TargetGetResult).Card), "nickname") {
		t.Fatalf("read back a nickname the card lacks: %s", res.(protocol.TargetGetResult).Card)
	}
	if f.avatars[id] != 0 {
		t.Fatalf("uploaded the same avatar %d times", f.avatars[id])
	}
	put(t, p, protocol.TargetPutParams{Card: testCard(nil), RemoteID: id, Avatar: avatarFile(t, "bbb")})
	if _, _, ext := stored(f, id); f.avatars[id] != 1 || fmt.Sprint(ext[innkeeperKey]) != "map[avatar:bbb key:k]" {
		t.Fatalf("%d avatar uploads; extensions %v", f.avatars[id], ext)
	}
}

// SillyTavern's merge can't put an object where a stored value isn't one.
func TestAnObjectReplacesAStoredNonObject(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k"})
	_, _, ext := stored(f, id)
	ext["foo"], ext["bar"] = nil, "xy"
	put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"extensions": map[string]any{
		"foo": map[string]any{"k": 1}, "bar": map[string]any{"k": 2}}}), RemoteID: id})
	if _, _, ext := stored(f, id); fmt.Sprint(ext["foo"], ext["bar"]) != "map[k:1] map[k:2]" {
		t.Fatalf("extensions = %v", ext)
	}
}

func TestUpdateOfAMissingCharacter(t *testing.T) {
	f := newFake(t)
	if err := putErr(newPlugin(f), protocol.TargetPutParams{Card: testCard(nil), RemoteID: "Gone.png"}); !httpkit.NotFound(err) || len(f.merges) != 0 {
		t.Fatalf("got %v after %d merges", err, len(f.merges))
	}
}

func TestTwoLorebooksFailWithoutWriting(t *testing.T) {
	f := newFake(t)
	pe := final(t, putErr(newPlugin(f), protocol.TargetPutParams{Card: testCard(nil), Key: "k", LorebookRemoteIDs: []string{"a", "b"}}))
	if !strings.Contains(pe.Message, "one lorebook") || f.requests != 0 {
		t.Fatalf("%q after %d requests", pe.Message, f.requests)
	}
}

// A PNG dropped into SillyTavern's folder may hold a V1 card, with no data.
func TestGetAV1Character(t *testing.T) {
	f := newFake(t)
	f.characters["Old.png"] = map[string]any{"name": "Old", "description": "An old card.", "first_mes": "Hi.",
		"creatorcomment": "Notes."}
	res, err := newPlugin(f).get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: "Old.png"}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Spec string         `json:"spec"`
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(res.(protocol.TargetGetResult).Card, &got)
	if got.Spec != "chara_card_v3" || got.Data["name"] != "Old" || got.Data["description"] != "An old card." ||
		got.Data["first_mes"] != "Hi." || got.Data["creator_notes"] != "Notes." {
		t.Fatalf("card = %+v", got)
	}
	if ext := object(got.Data["extensions"]); ext["world"] != nil || ext["fav"] != nil {
		t.Fatalf("extensions = %v", ext)
	}
	if links := res.(protocol.TargetGetResult).LorebookRemoteIDs; links == nil || len(links) != 0 {
		t.Fatalf("lorebooks = %#v, want []", links)
	}
}

func TestGetAvatar(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	p.blobTmp = t.TempDir()
	id := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k", Avatar: avatarFile(t, "abc")})
	res, err := p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id, Avatar: true}))
	if err != nil {
		t.Fatal(err)
	}
	path := res.(protocol.TargetGetResult).Avatar
	img, err := os.ReadFile(path)
	if filepath.Dir(path) != p.blobTmp || err != nil || !bytes.Equal(img, f.images[id]) {
		t.Fatalf("avatar %q: %v", path, err)
	}
}

func TestGetWithoutAnAvatar(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	p.blobTmp = t.TempDir()
	id := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k"})
	res, err := p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id, Avatar: true}))
	if err != nil {
		t.Fatal(err)
	}
	if a := res.(protocol.TargetGetResult).Avatar; a != "" {
		t.Fatalf("avatar = %q", a)
	}
	// A character SillyTavern made itself keeps a gray image.
	_, _, ext := stored(f, id)
	delete(ext, innkeeperKey)
	res, err = p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id, Avatar: true}))
	if err != nil {
		t.Fatal(err)
	}
	if res.(protocol.TargetGetResult).Avatar == "" {
		t.Fatal("a native character's gray avatar was dropped")
	}
}

func TestGetCharacter(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k", LorebookRemoteIDs: []string{"Dwarves"}})
	_, data, ext := stored(f, id)
	ext["fav"] = true
	data["character_book"] = map[string]any{"entries": []any{}}
	res, err := p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: id}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Spec        string         `json:"spec"`
		SpecVersion string         `json:"spec_version"`
		Data        map[string]any `json:"data"`
	}
	_ = json.Unmarshal(res.(protocol.TargetGetResult).Card, &got)
	if got.Spec != "chara_card_v3" || got.SpecVersion != "3.0" || got.Data["first_mes"] != "Hail." || got.Data["character_book"] != nil {
		t.Fatalf("card = %+v", got)
	}
	if fmt.Sprint(got.Data["extensions"]) != "map[depth_prompt:map[depth:4 prompt:Grumble.]]" {
		t.Fatalf("extensions = %v", got.Data["extensions"])
	}
	if r := res.(protocol.TargetGetResult); !slices.Equal(r.LorebookRemoteIDs, []string{"Dwarves"}) || r.Avatar != "" {
		t.Fatalf("lorebooks = %v, avatar = %q", r.LorebookRemoteIDs, r.Avatar)
	}
	if _, err := p.get(context.Background(), mustJSON(t, protocol.TargetGetParams{RemoteID: "Gone.png"})); !httpkit.NotFound(err) {
		t.Fatalf("get of a missing character: %v", err)
	}
}

func TestListCharacters(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	for _, name := range []string{"Zed", "Brakka"} {
		put(t, p, protocol.TargetPutParams{Card: testCard(map[string]any{"name": name}), Key: name})
	}
	for _, lazy := range []bool{false, true} {
		f.lazy = lazy
		res, err := p.list(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(res.Items) != "[{Brakka.png Brakka} {Zed.png Zed}]" {
			t.Fatalf("lazy=%v: items = %v", lazy, res.Items)
		}
	}
}

func TestDeleteCharacterAndItsChats(t *testing.T) {
	f := newFake(t)
	p := newPlugin(f)
	id := put(t, p, protocol.TargetPutParams{Card: testCard(nil), Key: "k1"})
	del := mustJSON(t, protocol.TargetDeleteParams{RemoteID: id})
	if _, err := p.remove(context.Background(), del); err != nil {
		t.Fatal(err)
	}
	if len(f.characters) != 0 || !f.chatsDeleted {
		t.Fatalf("characters = %v, chats deleted = %v", f.characters, f.chatsDeleted)
	}
	if _, err := p.remove(context.Background(), del); !httpkit.NotFound(err) {
		t.Fatalf("deleting it again: %v, want not found", err)
	}
}
