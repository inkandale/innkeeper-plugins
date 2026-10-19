package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// targetInfo answers target.describe. url-import follows a source's page
// and never pushes. fields leave out character_book, which the core doesn't
// compare, and source, which lists the pages Innkeeper linked rather than
// anything the author wrote.
var targetInfo = protocol.TargetInfo{
	Fields: []string{"name", "nickname", "description", "personality", "scenario", "first_mes", "mes_example",
		"alternate_greetings", "group_only_greetings", "system_prompt", "post_history_instructions",
		"creator_notes", "creator_notes_multilingual", "tags", "creator", "character_version", "extensions", "assets"},
	Pull:           true,
	Lorebooks:      protocol.LorebooksStandalone,
	LorebookFields: []string{"name", "description", "scan_depth", "token_budget", "recursive_scanning", "extensions", "entries"},
}

// noItems answers target.list and target.list_lorebooks: there is nothing to
// bind except the sources the core already knows.
var noItems = protocol.TargetListResult{Items: []protocol.TargetItem{}}

// follow fetches the page that remoteID, kind:ref, names. A remote_id that
// url-import can't read is gone, so the core stops expecting it.
func follow(ctx context.Context, f *fetcher, remoteID string, lorebook bool) (found, error) {
	kind, ref, _ := strings.Cut(remoteID, ":")
	toSource, ok := sourceKinds[kind]
	var s protocol.Source
	if ok {
		s, ok = toSource(ref, lorebook)
	}
	if !ok {
		return found{}, gone(fmt.Errorf("url-import can't follow %q", remoteID))
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return found{}, permanent(err)
	}
	return hosts[u.Hostname()].resolve(ctx, f, u)
}

// getCard answers target.get.
func getCard(ctx context.Context, f *fetcher, blobTmp string, raw json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	got, err := follow(ctx, f, p.RemoteID, false)
	if err != nil {
		return nil, err
	}
	c, img, err := decodeCard(got)
	if err != nil {
		return nil, err
	}
	res := protocol.TargetGetResult{Card: c}
	if p.Avatar && img != nil {
		if res.Avatar, err = pluginio.WriteTmp(blobTmp, "avatar-*.png", img); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// getLorebook answers target.get_lorebook.
func getLorebook(ctx context.Context, f *fetcher, raw json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	got, err := follow(ctx, f, p.RemoteID, true)
	if err != nil {
		return nil, err
	}
	book, err := card.DecodeLorebook(got.lorebook, got.name)
	if err != nil {
		return nil, permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	return protocol.TargetGetLorebookResult{Lorebook: book}, nil
}
