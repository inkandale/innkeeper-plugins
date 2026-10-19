// Command url-import is an import plugin. It imports one card or lorebook
// from its page on a public site, such as chub.ai, and is a sites plugin for
// the same sites, which follows their pages for the author's changes.
package main

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper/protocol"
)

func main() {
	f := newFetcher()
	protocol.Serve(protocol.Plugin[string]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*string, error) { return &p.BlobTmp, nil },
		Methods: map[string]func(*string, context.Context, json.RawMessage) (any, error){
			protocol.MethodImportRun: func(blobTmp *string, ctx context.Context, params json.RawMessage) (any, error) {
				return run(ctx, f, *blobTmp, protocol.Peer(ctx).Notify, params)
			},
			protocol.MethodSitesDetect: func(_ *string, _ context.Context, params json.RawMessage) (any, error) {
				return detectSources(params)
			},
			protocol.MethodSitesParse: func(_ *string, _ context.Context, params json.RawMessage) (any, error) {
				return parseSource(params)
			},
			protocol.MethodTargetDescribe: func(*string, context.Context, json.RawMessage) (any, error) {
				return targetInfo, nil
			},
			protocol.MethodTargetGet: func(blobTmp *string, ctx context.Context, params json.RawMessage) (any, error) {
				return getCard(ctx, f, *blobTmp, params)
			},
			protocol.MethodTargetGetLorebook: func(_ *string, ctx context.Context, params json.RawMessage) (any, error) {
				return getLorebook(ctx, f, params)
			},
		},
	})
}
