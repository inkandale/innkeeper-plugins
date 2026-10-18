// Command card-lint is an enrich plugin. It scores cards with fast checks
// that need no model, so an archive can be filtered before a model-based
// evaluation.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.1.0"

func main() {
	// Set by initialize; requests run on other goroutines.
	var cur atomic.Pointer[settings]
	handler := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			s, err := parseSettings(p.Config)
			if err != nil {
				return nil, invalid(err)
			}
			cur.Store(&s)
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "card-lint", Version: version,
				Capabilities: []string{"enrich"}}, nil
		case protocol.MethodShutdown:
			return nil, nil
		case protocol.MethodEnrichRun:
			s := cur.Load()
			if s == nil {
				return nil, fmt.Errorf("%s before initialize", method)
			}
			return enrich(*s, params)
		}
		return nil, protocol.MethodNotFound(method)
	}
	conn := protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// enrich lints each revision in the batch. A revision whose data won't
// decode gets its own error; the rest of the batch goes on.
func enrich(s settings, params json.RawMessage) (*protocol.EnrichRunResult, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid(err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, invalid(fmt.Errorf("revisions must hold 1 to %d", protocol.MaxEnrichBatch))
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

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}
