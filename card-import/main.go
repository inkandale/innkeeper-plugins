// Command card-import is an import plugin. It imports character cards and
// lorebooks from a folder, or from one file uploaded to the core.
package main

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper/protocol"
)

func main() {
	protocol.Serve(protocol.Plugin[env]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*env, error) {
			return &env{folder: configFolder(p.Config), blobTmp: p.BlobTmp, library: p.BlobStores["library"]}, nil
		},
		Methods: map[string]func(*env, context.Context, json.RawMessage) (any, error){
			protocol.MethodImportRun: func(e *env, ctx context.Context, params json.RawMessage) (any, error) {
				return run(ctx, *e, protocol.Peer(ctx).Notify, params)
			},
		},
	})
}

// configFolder is the folder imports read: the config's, or /import, its
// default, when the config leaves it out.
func configFolder(config map[string]any) string {
	folder, ok := config["folder"].(string)
	if !ok {
		return "/import"
	}
	return folder
}
