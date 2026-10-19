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
			folder, _ := p.Config["folder"].(string)
			return &env{folder: folder, blobTmp: p.BlobTmp, library: p.BlobStores["library"]}, nil
		},
		Methods: map[string]func(*env, context.Context, json.RawMessage) (any, error){
			protocol.MethodImportRun: func(e *env, ctx context.Context, params json.RawMessage) (any, error) {
				return run(ctx, *e, protocol.Peer(ctx).Notify, params)
			},
		},
	})
}
