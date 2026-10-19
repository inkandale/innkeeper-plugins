// Command card-lint is an enrich plugin. It scores cards with fast checks
// that need no model, so an archive can be filtered before a model-based
// evaluation.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/inkandale/innkeeper/protocol"
)

func main() {
	protocol.Serve(protocol.Plugin[settings]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*settings, error) {
			s, err := parseSettings(p.Config)
			if err != nil {
				return nil, protocol.InvalidParams(err)
			}
			return &s, nil
		},
		Methods: map[string]func(*settings, context.Context, json.RawMessage) (any, error){
			protocol.MethodEnrichRun: func(s *settings, _ context.Context, params json.RawMessage) (any, error) {
				return enrich(*s, params)
			},
		},
	})
}

// enrich lints each revision in the batch. A revision whose data won't
// decode gets its own error; the rest of the batch goes on.
func enrich(s settings, params json.RawMessage) (*protocol.EnrichRunResult, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, protocol.InvalidParams(fmt.Errorf("revisions must hold 1 to %d", protocol.MaxEnrichBatch))
	}
	res := &protocol.EnrichRunResult{Results: make([]protocol.EnrichResult, 0, len(p.Revisions))}
	for _, rev := range p.Revisions {
		var c card
		if err := json.Unmarshal(rev.Data, &c); err != nil {
			res.Results = append(res.Results, protocol.EnrichResult{RevisionID: rev.RevisionID, Error: fmt.Sprintf("card data: %v", err)})
			continue
		}
		values, err := lint(s, c).values()
		if err != nil {
			return nil, err
		}
		res.Results = append(res.Results, protocol.EnrichResult{RevisionID: rev.RevisionID, Values: values})
	}
	return res, nil
}
