// Command crucible is an enrich plugin. It sends each card to a crucible
// service's POST /score and stores the values it answers with.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

// settings is an instance's config.
type settings struct {
	URL string
	// Timeout bounds one card's request.
	Timeout time.Duration
}

// defaultTimeout gives a large card time to score; the service scores serially.
const defaultTimeout = 5 * time.Minute

// parseSettings does not require the URL: core's config check skips required
// settings, so the plugin must start without one and fail when used.
func parseSettings(config map[string]any) settings {
	u, _ := config["url"].(string)
	s := settings{URL: strings.TrimRight(strings.TrimSpace(u), "/"), Timeout: defaultTimeout}
	if secs, ok := config["timeout"].(float64); ok && secs > 0 {
		s.Timeout = time.Duration(secs * float64(time.Second))
	}
	return s
}

// client has no timeout of its own: score bounds each request with the instance's Timeout.
var client = &http.Client{Transport: httpkit.HintLoopback(http.DefaultTransport)}

func main() {
	protocol.Serve(protocol.Plugin[settings]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*settings, error) {
			s := parseSettings(p.Config)
			return &s, nil
		},
		Methods: map[string]func(*settings, context.Context, json.RawMessage) (any, error){
			protocol.MethodEnrichRun: func(s *settings, ctx context.Context, params json.RawMessage) (any, error) {
				return enrich(ctx, s, params)
			},
		},
	})
}

// enrich scores a batch's revisions one at a time: the service scores serially,
// so parallel requests would wait out the client timeout in its queue.
func enrich(ctx context.Context, s *settings, params json.RawMessage) (*protocol.EnrichRunResult, error) {
	if s.URL == "" {
		return nil, errors.New("set the service URL")
	}
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if len(p.Revisions) == 0 || len(p.Revisions) > protocol.MaxEnrichBatch {
		return nil, protocol.InvalidParams(fmt.Errorf("revisions must hold 1 to %d", protocol.MaxEnrichBatch))
	}
	results := make([]protocol.EnrichResult, len(p.Revisions))
	for i, rev := range p.Revisions {
		results[i] = score(ctx, s, rev)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &protocol.EnrichRunResult{Results: results}, nil
}

// score sends one revision to the service.
func score(ctx context.Context, s *settings, rev protocol.EnrichRevision) protocol.EnrichResult {
	fail := func(err error) protocol.EnrichResult {
		return protocol.EnrichResult{RevisionID: rev.RevisionID, Error: err.Error()}
	}
	body, err := json.Marshal(map[string]any{"card_id": rev.CardID, "revision_id": rev.RevisionID, "data": rev.Data})
	if err != nil {
		return fail(err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.URL+"/score", bytes.NewReader(body))
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return fail(fmt.Errorf("the crucible service took longer than %s; raise Timeout in Crucible's settings", s.Timeout))
		}
		return fail(err)
	}
	defer resp.Body.Close()
	var out struct {
		Values map[string]json.RawMessage `json:"values"`
		Meta   json.RawMessage            `json:"meta"`
		Error  string                     `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, protocol.MaxEnrichValue*16)).Decode(&out); err != nil {
		return fail(fmt.Errorf("crucible service answered HTTP %d without JSON", resp.StatusCode))
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = fmt.Sprintf("crucible service answered HTTP %d", resp.StatusCode)
		}
		return fail(errors.New(out.Error))
	}
	return protocol.EnrichResult{RevisionID: rev.RevisionID, Values: out.Values, Meta: out.Meta}
}
