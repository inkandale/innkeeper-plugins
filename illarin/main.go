// Command illarin is an import plugin. It connects to Illarin as a connected
// app with work:receive and imports the works the owner sends or names.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

const defaultBase = "https://illarin.com"

type instance struct {
	client  *client
	blobTmp string
	err     error // a config problem, reported by the first run
}

func main() {
	protocol.Serve(protocol.Plugin[instance]{
		Init: func(ctx context.Context, p protocol.InitializeParams) (*instance, error) {
			return newInstance(ctx, p, httpkit.NewClient(nil))
		},
		Methods: map[string]func(*instance, context.Context, json.RawMessage) (any, error){
			protocol.MethodImportRun: func(in *instance, ctx context.Context, params json.RawMessage) (any, error) {
				return in.run(ctx, protocol.Peer(ctx).Notify, params)
			},
			protocol.MethodShutdown: func(in *instance, _ context.Context, _ json.RawMessage) (any, error) {
				if in.client != nil {
					in.client.store.close()
				}
				return nil, nil
			},
		},
	})
}

func newInstance(ctx context.Context, p protocol.InitializeParams, h *http.Client) (*instance, error) {
	in := &instance{blobTmp: p.BlobTmp}
	base, err := baseURL(p.Config)
	if err != nil {
		in.err = err
		return in, nil
	}
	if p.DatabaseURL == "" {
		// The contract kit starts plugins without a database; runs then fail.
		in.err = fmt.Errorf("illarin needs its database to keep the connection")
		return in, nil
	}
	st, err := openStore(ctx, p.DatabaseURL, p.InstanceID)
	if err != nil {
		return nil, err
	}
	in.client = &client{http: h, base: base, store: st}
	return in, nil
}

// baseURL reads config's base_url: an https origin, without a trailing slash.
func baseURL(cfg map[string]any) (string, error) {
	raw, _ := cfg["base_url"].(string)
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return defaultBase, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" {
		return "", fmt.Errorf("base_url %q must be an https address with no path, such as %s", raw, defaultBase)
	}
	return raw, nil
}
