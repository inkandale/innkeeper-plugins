// Command card-rules is an enrich plugin that scores cards for obvious
// structural problems without a model, as a cheap first pass over a library.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.1.0"

func main() {
	conn := protocol.NewConn(os.Stdin, os.Stdout, handle)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodInitialize:
		return protocol.InitializeResult{Protocol: protocol.Version, Name: "card-rules", Version: version,
			Capabilities: []string{"enrich"}}, nil
	case protocol.MethodShutdown:
		return nil, nil
	case protocol.MethodEnrichRun:
		return enrich(params)
	}
	return nil, protocol.MethodNotFound(method)
}

func enrich(params json.RawMessage) (*protocol.EnrichRunResult, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	meta, _ := json.Marshal(map[string]string{"rules": rulesVersion})
	res := &protocol.EnrichRunResult{Results: []protocol.EnrichResult{}}
	for _, rev := range p.Revisions {
		var card map[string]any
		if err := json.Unmarshal(rev.Data, &card); err != nil {
			res.Results = append(res.Results, protocol.EnrichResult{RevisionID: rev.RevisionID, Error: "the card data isn't a JSON object"})
			continue
		}
		r := check(rev.Data)
		values := map[string]json.RawMessage{}
		values["quality"], _ = json.Marshal(r.quality)
		values["issues"], _ = json.Marshal(r.issues)
		values["tokens"], _ = json.Marshal(r.tokens)
		res.Results = append(res.Results, protocol.EnrichResult{RevisionID: rev.RevisionID, Values: values, Meta: meta})
	}
	return res, nil
}
