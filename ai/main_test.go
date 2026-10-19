package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// fakeLLM serves the OpenAI-compatible endpoints the plugin calls. chat is
// the SSE delta objects every chat request streams; status, when set, fails
// every request with it and the error code code.
type fakeLLM struct {
	mu     sync.Mutex
	status int
	code   string
	chat   []string
	// decide is the /systemone reply body; a default answers every question.
	decide   string
	paths    []string
	requests []map[string]any
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.requests = append(f.requests, body)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.status != 0 {
		// The SDKs would otherwise retry a 5xx themselves.
		w.Header().Set("X-Should-Retry", "false")
		w.WriteHeader(f.status)
		// The message echoes the key, as some providers' do, to check the plugin hides it.
		msg, _ := json.Marshal("refused by the fake for " + r.Header.Get("Authorization"))
		fmt.Fprintf(w, `{"error": {"message": %s, "type": "invalid_request_error", "code": %q}}`, msg, f.code)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/models"):
		fmt.Fprint(w, `{"object": "list", "data": [{"id": "m2", "object": "model"}, {"id": "m1", "object": "model"}]}`)
	case strings.HasSuffix(r.URL.Path, "/systemone"):
		if f.decide != "" {
			fmt.Fprint(w, f.decide)
			return
		}
		answers := map[string]any{}
		qs, _ := body["questions"].(map[string]any)
		for id := range qs {
			answers[id] = map[string]any{"type": "noul", "noul": 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "d3", "answers": answers})
	case strings.HasSuffix(r.URL.Path, "/embeddings"):
		// Vector i is [i], sent in reverse order to check the plugin orders by index.
		in, _ := body["input"].([]any)
		var data []map[string]any
		for i := len(in) - 1; i >= 0; i-- {
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": []float64{float64(i)}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": "m", "data": data})
	default:
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range f.chat {
			fmt.Fprintf(w, "data: {\"id\": \"c\", \"object\": \"chat.completion.chunk\", \"model\": \"m\", \"choices\": [{\"index\": 0, \"delta\": %s}]}\n\n", d)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func (f *fakeLLM) lastRequest() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func content(text string) string {
	b, _ := json.Marshal(map[string]string{"content": text})
	return string(b)
}

// withLLM starts f and returns a config that points openai-compatible at it.
func withLLM(t *testing.T, f *fakeLLM, kinds ...any) map[string]any {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	cfg := map[string]any{"provider": "openai-compatible", "base_url": ts.URL, "model": "m"}
	if len(kinds) > 0 {
		cfg["kinds"] = kinds
	}
	return cfg
}

// host is a fake Innkeeper core: it talks to the plugin over pipes and
// records the ai.delta notifications it gets.
type host struct {
	conn   *protocol.Conn
	mu     sync.Mutex
	deltas []protocol.AIDelta
}

// start runs the plugin and initializes it with config.
func start(t *testing.T, config map[string]any) (*host, protocol.InitializeResult, error) {
	t.Helper()
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	m, err := protocol.LoadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	pluginConn := protocol.NewConn(toPlugin, fromPlugin, plugin().Handler(m))
	h := &host{}
	h.conn = protocol.NewConn(toHost, fromHost, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != protocol.NotifyAIDelta {
			return nil, protocol.MethodNotFound(method)
		}
		var d protocol.AIDelta
		_ = json.Unmarshal(params, &d)
		h.mu.Lock()
		h.deltas = append(h.deltas, d)
		h.mu.Unlock()
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go pluginConn.Run(ctx)
	go h.conn.Run(ctx)
	t.Cleanup(func() {
		cancel()
		fromHost.Close()
		fromPlugin.Close()
	})
	var res protocol.InitializeResult
	err = h.conn.Call(ctx, protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1, Config: config}, &res)
	return h, res, err
}

// started is start for a config that must initialize.
func started(t *testing.T, config map[string]any) *host {
	t.Helper()
	h, _, err := start(t, config)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *host) call(method string, params, result any) error {
	return h.conn.Call(context.Background(), method, params, result)
}

// rpcCode is err's JSON-RPC code, or 0.
func rpcCode(err error) int {
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

func TestInitializeReportsTickedKinds(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		want   []string
	}{
		{"default", map[string]any{"provider": "ollama", "model": "m"}, []string{protocol.MethodAIChat, protocol.MethodAIDecide}},
		{"both", map[string]any{"provider": "ollama", "model": "m", "kinds": []any{"embed", "chat"}},
			[]string{protocol.MethodAIChat, protocol.MethodAIDecide, protocol.MethodAIEmbed}},
		{"settings process", map[string]any{}, nil},
	}
	for _, c := range cases {
		_, res, err := start(t, c.config)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !slices.Equal(res.Services, c.want) || res.Name != "ai" || !slices.Equal(res.Capabilities, []string{"ai", "settings"}) {
			t.Errorf("%s: initialize = %+v, want services %v", c.name, res, c.want)
		}
	}
}

func TestInitializeRefusesABadConfig(t *testing.T) {
	if _, _, err := start(t, map[string]any{"provider": "ollama"}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v, want one about the model", err)
	}
}
