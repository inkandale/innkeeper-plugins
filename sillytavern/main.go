// Command sillytavern is a target plugin for SillyTavern
// (github.com/SillyTavern/SillyTavern). It pushes cards as characters and
// lorebooks as world info files, and reads both back.
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

// describe says what SillyTavern stores. It needs no server.
func describe() protocol.TargetInfo {
	return protocol.TargetInfo{
		Fields: cardFields,
		Pull:   true, Update: true, Delete: true, Lorebooks: protocol.LorebooksStandalone,
		LorebookFields: []string{"name", "description", "scan_depth", "token_budget", "recursive_scanning",
			"extensions", "entries"},
	}
}

func main() {
	protocol.Serve(protocol.Plugin[plugin]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*plugin, error) {
			url, _ := p.Config["url"].(string)
			auth, _ := p.Config["auth"].(string)
			username, _ := p.Config["username"].(string)
			return &plugin{newClient(url, auth, username, p.Secrets["password"]), p.BlobTmp}, nil
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
				return p.list(ctx)
			},
			protocol.MethodTargetListLorebooks: func(p *plugin, ctx context.Context, _ json.RawMessage) (any, error) {
				return p.listLorebooks(ctx)
			},
			protocol.MethodTargetDelete:         (*plugin).remove,
			protocol.MethodTargetDeleteLorebook: (*plugin).removeLorebook,
		},
	})
}
