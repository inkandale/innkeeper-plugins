// Command lumiverse is a target plugin for Lumiverse
// (github.com/prolix-oc/Lumiverse). It pushes cards as characters and
// lorebooks as world books, and reads both back.
package main

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper/protocol"
)

type plugin struct {
	c       *client
	blobTmp string
}

// describe says what Lumiverse stores. It needs no server.
func describe() protocol.TargetInfo {
	return protocol.TargetInfo{
		Fields: []string{"name", "description", "personality", "scenario", "first_mes", "mes_example", "creator",
			"creator_notes", "system_prompt", "post_history_instructions", "alternate_greetings", "tags",
			"character_version", "extensions"},
		Pull: true, Update: true, Delete: true, Lorebooks: protocol.LorebooksStandalone,
		LorebookFields: []string{"name", "description", "scan_depth", "token_budget", "recursive_scanning",
			"extensions", "entries"},
	}
}

func main() {
	protocol.Serve(protocol.Plugin[plugin]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*plugin, error) {
			url, _ := p.Config["url"].(string)
			username, _ := p.Config["username"].(string)
			return &plugin{newClient(url, username, p.Secrets["password"]), p.BlobTmp}, nil
		},
		Methods: map[string]func(*plugin, context.Context, json.RawMessage) (any, error){
			protocol.MethodTargetDescribe: func(*plugin, context.Context, json.RawMessage) (any, error) {
				return describe(), nil
			},
			protocol.MethodTargetPut:         (*plugin).put,
			protocol.MethodTargetGet:         (*plugin).get,
			protocol.MethodTargetPutLorebook: (*plugin).putLorebook,
			protocol.MethodTargetGetLorebook: (*plugin).getLorebook,
			protocol.MethodTargetList: func(p *plugin, ctx context.Context, _ json.RawMessage) (any, error) {
				// By name: the default order moves a character up when it's chatted with.
				return p.list(ctx, "/characters/summary?sort=name&direction=asc")
			},
			protocol.MethodTargetListLorebooks: func(p *plugin, ctx context.Context, _ json.RawMessage) (any, error) {
				// ponytail: Lumiverse orders these by updated_at only, so a book
				// edited mid-listing can be skipped or repeated.
				return p.list(ctx, "/world-books?")
			},
			protocol.MethodTargetDelete: func(p *plugin, ctx context.Context, params json.RawMessage) (any, error) {
				// Lumiverse deletes the character's one-on-one chats with it.
				return p.remove(ctx, "/characters/", params)
			},
			protocol.MethodTargetDeleteLorebook: func(p *plugin, ctx context.Context, params json.RawMessage) (any, error) {
				return p.remove(ctx, "/world-books/", params)
			},
		},
	})
}
