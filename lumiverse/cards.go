package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// stringFields are the character's text fields, as Lumiverse names them.
var stringFields = []string{"name", "description", "personality", "scenario", "first_mes", "mes_example",
	"creator", "creator_notes", "system_prompt", "post_history_instructions"}

// localKeys are extensions Lumiverse keeps for itself. A push keeps the
// character's own, and a read leaves them out. Keys starting with
// _lumiverse_ count too.
var localKeys = []string{"expressions", "expression_groups", "alternate_fields", "alternate_avatars",
	"world_book_id", "world_book_ids", "avatar_crop_image_id", "original_image_id", "risu_asset_map",
	"gallery_reference_sequence", "gallery_reference_names", "landing_perspective_layers", "ttsVoice"}

func isLocal(key string) bool {
	return slices.Contains(localKeys, key) || strings.HasPrefix(key, "_lumiverse_")
}

// innkeeperKey is the extension where the plugin keeps what it needs.
const innkeeperKey = "innkeeper"

// character is a Lumiverse character, as its API returns it.
type character struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Extensions map[string]any `json:"extensions"`
}

func (c character) stash() map[string]any {
	m, _ := c.Extensions[innkeeperKey].(map[string]any)
	return m
}

// worldBookIDs is never nil, so an empty result means the character has none.
func (c character) worldBookIDs() []string {
	links := []string{}
	if ids, ok := c.Extensions["world_book_ids"].([]any); ok {
		for _, id := range ids {
			if s, ok := id.(string); ok && s != "" {
				links = append(links, s)
			}
		}
	} else if id, _ := c.Extensions["world_book_id"].(string); id != "" {
		links = append(links, id)
	}
	return links
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (p *plugin) put(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetPutParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var card struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(in.Card, &card); err != nil || card.Data == nil {
		return nil, protocol.InvalidParams(errors.New("card has no data object"))
	}
	id := in.RemoteID
	if id == "" && in.Key != "" {
		found, err := p.findCharacter(ctx, fmt.Sprint(card.Data["name"]), in.Key)
		if err != nil {
			return nil, err
		}
		id = found
	}
	if id == "" {
		var err error
		if id, err = p.createCharacter(ctx, card.Data, in.Key); err != nil {
			return nil, err
		}
	}
	// A new character gets its avatar and links here too, so a create that
	// stopped partway is finished by the re-run that finds it.
	if err := p.updateCharacter(ctx, id, card.Data, in); err != nil {
		return nil, err
	}
	return protocol.TargetPutResult{RemoteID: id}, nil
}

// findCharacter returns the character a put with key created, or "". It
// searches by name, since Lumiverse can't search extensions.
func (p *plugin) findCharacter(ctx context.Context, name, key string) (string, error) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.c.call(ctx, "GET", "/characters/summary?limit=1000&search="+url.QueryEscape(name), nil, &list); err != nil {
		return "", err
	}
	for _, s := range list.Data {
		var c character
		err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(s.ID), nil, &c)
		if httpkit.NotFound(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if c.stash()["key"] == key {
			return c.ID, nil
		}
	}
	return "", nil
}

func (p *plugin) createCharacter(ctx context.Context, data map[string]any, key string) (string, error) {
	data = cardForLumiverse(data)
	ext := object(data["extensions"])
	ext[innkeeperKey] = map[string]any{"key": key}
	data["extensions"] = ext
	var reply struct {
		Character character `json:"character"`
	}
	err := p.c.call(ctx, "POST", "/characters/import", map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}, &reply)
	if err != nil {
		return "", err
	}
	if reply.Character.ID == "" {
		return "", protocol.Fail("character import returned no id", false)
	}
	return reply.Character.ID, nil
}

// cardForLumiverse copies data without its embedded lorebook, which this
// target keeps as world books.
func cardForLumiverse(data map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range data {
		if k != "character_book" {
			out[k] = v
		}
	}
	ext := map[string]any{}
	for k, v := range object(data["extensions"]) {
		if k != "character_book" && k != innkeeperKey {
			ext[k] = v
		}
	}
	out["extensions"] = ext
	return out
}

// updateCharacter makes character id match data: every field, the avatar if
// it changed, and the world book links. It keeps Lumiverse's own extensions.
func (p *plugin) updateCharacter(ctx context.Context, id string, data map[string]any, in protocol.TargetPutParams) error {
	path := "/characters/" + url.PathEscape(id)
	var cur character
	if err := p.c.call(ctx, "GET", path, nil, &cur); err != nil {
		return err
	}
	stash := map[string]any{"key": keyOr(cur.stash()["key"], in.Key)}
	if v, ok := cur.stash()["avatar"]; ok {
		stash["avatar"] = v
	}
	if sha := filepath.Base(in.Avatar); in.Avatar != "" && stash["avatar"] != sha {
		if err := p.uploadAvatar(ctx, id, in.Avatar); err != nil {
			return err
		}
		stash["avatar"] = sha
		// The upload changes Lumiverse's own avatar keys.
		cur = character{}
		if err := p.c.call(ctx, "GET", path, nil, &cur); err != nil {
			return err
		}
	}
	data = cardForLumiverse(data)
	ext := object(data["extensions"])
	for k, v := range cur.Extensions {
		if isLocal(k) {
			ext[k] = v
		}
	}
	ext["world_book_ids"] = orEmpty(in.LorebookRemoteIDs)
	delete(ext, "world_book_id")
	if v, ok := data["character_version"]; ok {
		ext["character_version"] = v
	}
	ext[innkeeperKey] = stash
	body := map[string]any{"extensions": ext,
		"tags": listOr(data["tags"]), "alternate_greetings": listOr(data["alternate_greetings"])}
	for _, f := range stringFields {
		s, _ := data[f].(string)
		body[f] = s
	}
	return p.c.call(ctx, "PUT", path, body, nil)
}

// orEmpty sends no links as an empty list, which unlinks them all.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// keyOr keeps the key a character already has, or uses key.
func keyOr(have any, key string) any {
	if s, ok := have.(string); ok && s != "" {
		return s
	}
	return key
}

func listOr(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func (p *plugin) uploadAvatar(ctx context.Context, id, path string) error {
	raw, err := pluginio.ReadFile(path)
	if err != nil {
		return protocol.Fail("read avatar: "+err.Error(), false)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("avatar", "avatar"+avatarExt(raw))
	if err != nil {
		return err
	}
	if _, err := part.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return p.c.do(ctx, request{method: "POST", path: "/characters/" + url.PathEscape(id) + "/avatar",
		body: body.Bytes(), contentType: w.FormDataContentType()}, nil)
}

// avatarExt names the image's type, since Lumiverse takes it from the file name.
func avatarExt(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG")):
		return ".png"
	case bytes.HasPrefix(raw, []byte("\xff\xd8")):
		return ".jpg"
	case len(raw) > 12 && string(raw[8:12]) == "WEBP":
		return ".webp"
	case bytes.HasPrefix(raw, []byte("GIF8")):
		return ".gif"
	}
	return ".png"
}

func (p *plugin) get(ctx context.Context, params json.RawMessage) (any, error) {
	var in protocol.TargetGetParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	// The export leaves world_book_ids out, so read the links here.
	var c character
	if err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(in.RemoteID), nil, &c); err != nil {
		return nil, err
	}
	var card map[string]any
	if err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(in.RemoteID)+"/export?format=json", nil, &card); err != nil {
		return nil, err
	}
	data := object(card["data"])
	// Built from the linked world books, which sync on their own.
	delete(data, "character_book")
	ext := map[string]any{}
	for k, v := range object(data["extensions"]) {
		// fav and world are SillyTavern's own keys, carried in by an import.
		switch {
		case k == "character_version":
			data[k] = v
		case isLocal(k) || k == innkeeperKey || k == "character_book" || k == "fav" || k == "world":
		default:
			ext[k] = v
		}
	}
	data["extensions"] = ext
	card["data"] = data
	raw, err := json.Marshal(card)
	if err != nil {
		return nil, err
	}
	res := protocol.TargetGetResult{Card: raw, LorebookRemoteIDs: c.worldBookIDs()}
	if in.Avatar {
		if res.Avatar, err = p.avatar(ctx, in.RemoteID); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// avatar writes character id's image to blob_tmp and returns its path, or "" when it has none.
func (p *plugin) avatar(ctx context.Context, id string) (string, error) {
	var img []byte
	if err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(id)+"/avatar", nil, &img); err != nil {
		if httpkit.NotFound(err) {
			return "", nil
		}
		return "", err
	}
	path, err := pluginio.WriteTmp(p.blobTmp, "avatar-*"+avatarExt(img), img)
	if err != nil {
		return "", protocol.Fail("write the avatar: "+err.Error(), false)
	}
	return path, nil
}
