package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// v2Fields are the fields SillyTavern's own code expects every card to have.
var v2Fields = []string{"name", "description", "personality", "scenario", "first_mes", "mes_example",
	"creator", "creator_notes", "system_prompt", "post_history_instructions", "alternate_greetings", "tags",
	"character_version"}

// cardFields are the CCv3 fields SillyTavern keeps as sent.
var cardFields = append(slices.Clone(v2Fields), "extensions", "nickname", "group_only_greetings",
	"creator_notes_multilingual", "source", "assets")

// v1Fields are copied to the card's top level, which SillyTavern's PNG
// export writes as stored.
var v1Fields = []string{"name", "description", "personality", "scenario", "first_mes", "mes_example", "tags"}

const (
	// innkeeperKey is the extension where the plugin keeps the key and avatar sha.
	innkeeperKey = "innkeeper"
	// unset makes /merge-attributes delete the key it's sent for.
	unset = "__@@UNSET@@__"
)

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func (p *plugin) put(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetPutParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var c struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(in.Card, &c); err != nil || c.Data == nil {
		return nil, protocol.InvalidParams(errors.New("card has no data object"))
	}
	// Pushing without the others would drop their content from SillyTavern.
	if n := len(in.LorebookRemoteIDs); n > 1 {
		return nil, protocol.Fail(fmt.Sprintf("SillyTavern links a character to one lorebook, and this card has %d", n), false)
	}
	world := ""
	if len(in.LorebookRemoteIDs) == 1 {
		world = in.LorebookRemoteIDs[0]
	}
	id := in.RemoteID
	if id == "" && in.Key != "" {
		var err error
		if id, err = p.findCharacter(ctx, in.Key); err != nil {
			return nil, err
		}
	}
	if id == "" {
		id, err := p.createCharacter(ctx, c.Data, world, in)
		if err != nil {
			return nil, err
		}
		return protocol.TargetPutResult{RemoteID: id}, nil
	}
	if err := p.updateCharacter(ctx, id, c.Data, world, in); err != nil {
		return nil, err
	}
	return protocol.TargetPutResult{RemoteID: id}, nil
}

// remove deletes a character and its chats. SillyTavern's delete answers 400
// for a missing character, so read it first to tell that apart.
func (p *plugin) remove(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetDeleteParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if _, err := p.read(ctx, in.RemoteID); err != nil {
		return nil, err
	}
	if err := p.c.call(ctx, "/api/characters/delete", map[string]any{"avatar_url": in.RemoteID, "delete_chats": true}, nil); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// read returns character id as SillyTavern stores it.
func (p *plugin) read(ctx context.Context, id string) (map[string]any, error) {
	var r map[string]any
	if err := p.c.call(ctx, "/api/characters/get", map[string]any{"avatar_url": id}, &r); err != nil {
		return nil, err
	}
	raw, _ := r["json_data"].(string)
	var c map[string]any
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Fail(id+": unreadable card: "+err.Error(), false)
	}
	// A V1 card has no data; the reply around json_data is it converted to V2.
	if _, ok := c["data"].(map[string]any); !ok {
		delete(r, "json_data")
		return r, nil
	}
	return c, nil
}

// findCharacter returns the file of the character a put with key created, or "".
func (p *plugin) findCharacter(ctx context.Context, key string) (string, error) {
	var all []struct {
		Avatar  string `json:"avatar"`
		Shallow bool   `json:"shallow"`
		Data    struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"data"`
	}
	if err := p.c.call(ctx, "/api/characters/all", map[string]any{}, &all); err != nil {
		return "", err
	}
	for _, it := range all {
		ext := it.Data.Extensions
		if it.Shallow {
			// ponytail: one /get per character with lazyLoadCharacters on; fine for hundreds.
			c, err := p.read(ctx, it.Avatar)
			if httpkit.NotFound(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			ext = object(object(c["data"])["extensions"])
		}
		if object(ext[innkeeperKey])["key"] == key {
			return it.Avatar, nil
		}
	}
	return "", nil
}

func (p *plugin) createCharacter(ctx context.Context, data map[string]any, world string, in protocol.TargetPutParams) (string, error) {
	data = forSillyTavern(data, world)
	stash := map[string]any{"key": in.Key}
	if in.Avatar != "" {
		stash["avatar"] = filepath.Base(in.Avatar)
	}
	object(data["extensions"])[innkeeperKey] = stash
	file, err := cardPNG(data, in.Avatar)
	if err != nil {
		return "", err
	}
	var reply struct {
		FileName string `json:"file_name"`
		Error    bool   `json:"error"`
	}
	if err := p.c.upload(ctx, "/api/characters/import", map[string]string{"file_type": "png"}, file, &reply); err != nil {
		return "", err
	}
	// The route answers 200 when it fails.
	if reply.Error || reply.FileName == "" {
		return "", protocol.Fail("SillyTavern couldn't import the card; its server log says why", false)
	}
	id := reply.FileName + ".png"
	// The import strips the characters a file name can't hold from the
	// card's name too, so put the name back.
	if name, ok := data["name"].(string); ok {
		cur, err := p.read(ctx, id)
		if err != nil {
			return "", err
		}
		if object(cur["data"])["name"] != name {
			body := map[string]any{"avatar": id, "name": name, "data": map[string]any{"name": name}}
			if err := p.c.call(ctx, "/api/characters/merge-attributes", body, nil); err != nil {
				return "", err
			}
		}
	}
	return id, nil
}

// updateCharacter makes character id match data, keeping SillyTavern's
// favorite star. It never uses /edit, which resets every field it isn't sent.
func (p *plugin) updateCharacter(ctx context.Context, id string, data map[string]any, world string, in protocol.TargetPutParams) error {
	// /merge-attributes answers 500 for a missing character, so check first.
	cur, err := p.read(ctx, id)
	if err != nil {
		return err
	}
	curData := object(cur["data"])
	have := object(object(curData["extensions"])[innkeeperKey])
	stash := map[string]any{"key": keyOr(have["key"], in.Key)}
	if v, ok := have["avatar"]; ok {
		stash["avatar"] = v
	}
	data = forSillyTavern(data, world)
	ext := object(data["extensions"])
	ext[innkeeperKey] = stash
	// SillyTavern re-encodes the image, so the sha is the only way to tell it changed.
	if sha := filepath.Base(in.Avatar); in.Avatar != "" && stash["avatar"] != sha {
		file, err := cardPNG(data, in.Avatar)
		if err != nil {
			return err
		}
		if err := p.c.upload(ctx, "/api/characters/edit-avatar", map[string]string{"avatar_url": id}, file, nil); err != nil {
			return err
		}
		stash["avatar"] = sha
	}
	// The merge keeps whatever it isn't sent; unsetMissing covers the rest.
	for _, k := range v2Fields {
		if _, ok := data[k]; !ok {
			data[k] = ""
			if k == "alternate_greetings" || k == "tags" {
				data[k] = []any{}
			}
		}
	}
	unsetMissing(data, curData)
	delete(ext, "fav")
	// SillyTavern's merge turns a stored non-object into {} or junk instead of
	// taking an object sent for it, so those go first.
	if fix := clashes(data, curData); len(fix) > 0 {
		if err := p.c.call(ctx, "/api/characters/merge-attributes", map[string]any{"avatar": id, "data": fix}, nil); err != nil {
			return err
		}
	}
	body := map[string]any{"avatar": id, "data": data, "creatorcomment": data["creator_notes"]}
	for _, k := range v1Fields {
		body[k] = data[k]
	}
	return p.c.call(ctx, "/api/characters/merge-attributes", body, nil)
}

// forSillyTavern copies data for SillyTavern: linked to world, without the
// embedded lorebook, which syncs as a world file, or the local favorite star.
func forSillyTavern(data map[string]any, world string) map[string]any {
	out := map[string]any{}
	for k, v := range data {
		if k != "character_book" {
			out[k] = v
		}
	}
	ext := map[string]any{}
	for k, v := range object(data["extensions"]) {
		if k != innkeeperKey && k != "fav" {
			ext[k] = v
		}
	}
	ext["world"] = world
	out["extensions"] = ext
	return out
}

// unsetMissing marks each key of cur that next lacks for deletion, at every
// depth, since the merge combines objects.
func unsetMissing(next, cur map[string]any) {
	for k, v := range cur {
		n, ok := next[k]
		if !ok {
			next[k] = unset
			continue
		}
		nm, nok := n.(map[string]any)
		cm, cok := v.(map[string]any)
		if nok && cok {
			unsetMissing(nm, cm)
		}
	}
}

// clashes returns, as a merge body that unsets them, the paths where next
// has an object and cur a value that isn't one.
func clashes(next, cur map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range next {
		nm, ok := v.(map[string]any)
		cv, in := cur[k]
		if !ok || !in {
			continue
		}
		if cm, ok := cv.(map[string]any); !ok {
			out[k] = unset
		} else if sub := clashes(nm, cm); len(sub) > 0 {
			out[k] = sub
		}
	}
	return out
}

// cardPNG writes data as a card PNG with the avatar at path, or a placeholder.
func cardPNG(data map[string]any, avatar string) ([]byte, error) {
	var img []byte
	if avatar != "" {
		var err error
		if img, err = pluginio.ReadFile(avatar); err != nil {
			return nil, protocol.Fail("read avatar: "+err.Error(), false)
		}
	}
	raw, err := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
	if err != nil {
		return nil, err
	}
	out, _, err := card.Encode(raw, card.FormatPNG, img)
	if err != nil {
		return nil, protocol.Fail("write the card PNG: "+err.Error(), false)
	}
	return out, nil
}

// keyOr keeps the key a character already has, or uses key.
func keyOr(have any, key string) any {
	if s, ok := have.(string); ok && s != "" {
		return s
	}
	return key
}

func (p *plugin) get(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetGetParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	c, err := p.read(ctx, in.RemoteID)
	if err != nil {
		return nil, err
	}
	data := object(c["data"])
	// SillyTavern builds it from the linked world file, which syncs on its own.
	delete(data, "character_book")
	ext := object(data["extensions"])
	links := []string{}
	if world, _ := ext["world"].(string); world != "" {
		links = append(links, world)
	}
	stash, pushed := ext[innkeeperKey].(map[string]any)
	_, hasAvatar := stash["avatar"]
	for _, k := range []string{innkeeperKey, "world", "fav"} {
		delete(ext, k)
	}
	data["extensions"] = ext
	raw, err := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
	if err != nil {
		return nil, err
	}
	res := protocol.TargetGetResult{Card: raw, LorebookRemoteIDs: links}
	if in.Avatar {
		if res.Avatar, err = p.avatar(ctx, in.RemoteID, pushed && !hasAvatar); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// avatar writes character id's image to blob_tmp and returns its path. With
// pushedBare, for a character pushed without an avatar, it returns "" while
// the image is still the codec's placeholder.
func (p *plugin) avatar(ctx context.Context, id string, pushedBare bool) (string, error) {
	// The export is the character's stored PNG, unlike /thumbnail, which shrinks it.
	var img []byte
	if err := p.c.call(ctx, "/api/characters/export", map[string]any{"format": "png", "avatar_url": id}, &img); err != nil {
		return "", err
	}
	if pushedBare && card.IsPlaceholder(img) {
		return "", nil
	}
	path, err := pluginio.WriteTmp(p.blobTmp, "avatar-*.png", img)
	if err != nil {
		return "", protocol.Fail("write the avatar: "+err.Error(), false)
	}
	return path, nil
}

func (p *plugin) list(ctx context.Context) (protocol.TargetListResult, error) {
	var all []struct {
		Avatar string `json:"avatar"`
		Name   string `json:"name"`
	}
	if err := p.c.call(ctx, "/api/characters/all", map[string]any{}, &all); err != nil {
		return protocol.TargetListResult{}, err
	}
	items := make([]protocol.TargetItem, 0, len(all))
	for _, it := range all {
		items = append(items, protocol.TargetItem{RemoteID: it.Avatar, Name: it.Name})
	}
	slices.SortFunc(items, func(a, b protocol.TargetItem) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.RemoteID, b.RemoteID))
	})
	return protocol.TargetListResult{Items: items}, nil
}
