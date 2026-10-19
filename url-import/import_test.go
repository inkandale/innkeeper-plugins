package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// sites sends every request to mux. Requests keep their Host, so mux
// patterns name the real host, such as "GET api.chub.ai/api/...".
func sites(t *testing.T, mux *http.ServeMux) *fetcher {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	return &fetcher{http: &http.Client{CheckRedirect: httpsOnly, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = r.URL.Host // a followed redirect leaves Host empty
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}}
}

// importURL runs import.run for page and returns what it sent.
func importURL(t *testing.T, f *fetcher, page string) ([]any, error) {
	t.Helper()
	ps, _ := json.Marshal(params{URL: page})
	raw, _ := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: ps})
	var sent []any
	_, err := run(context.Background(), f, t.TempDir(), func(_ string, p any) error {
		sent = append(sent, p)
		return nil
	}, raw)
	return sent, err
}

// emitted returns the one card import sent, failing if it sent anything else
// but progress.
func emitted(t *testing.T, sent []any) protocol.EmitCard {
	t.Helper()
	if len(sent) != 2 {
		t.Fatalf("sent %d notifications, want a card and progress: %+v", len(sent), sent)
	}
	ev, ok := sent[0].(protocol.EmitCard)
	if !ok {
		t.Fatalf("sent %T, want EmitCard", sent[0])
	}
	return ev
}

// sources returns the card's data.source.
func sources(t *testing.T, c json.RawMessage) []string {
	t.Helper()
	var v struct {
		Data struct {
			Source []string `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(c, &v); err != nil {
		t.Fatal(err)
	}
	return v.Data.Source
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "card", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rpcErr returns err as a protocol error, failing if it isn't one.
func rpcErr(t *testing.T, err error) *protocol.Error {
	t.Helper()
	var e *protocol.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want a protocol error", err)
	}
	return e
}

func TestParams(t *testing.T) {
	f := sites(t, http.NewServeMux())
	for _, page := range []string{"", "not a url", "ftp://chub.ai/characters/a/b", "https://example.com/x"} {
		if _, err := importURL(t, f, page); rpcErr(t, err).Code != protocol.CodeInvalidParams {
			t.Errorf("%q: err = %v, want invalid params", page, err)
		}
	}
	raw, _ := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: json.RawMessage(`{"url": "x", "path": "y"}`)})
	if _, err := run(context.Background(), f, t.TempDir(), nil, raw); rpcErr(t, err).Code != protocol.CodeInvalidParams {
		t.Errorf("unknown param: err = %v", err)
	}
}

func TestJanitor(t *testing.T) {
	_, err := importURL(t, sites(t, http.NewServeMux()), "https://janitorai.com/characters/abc_character-x")
	if e := rpcErr(t, err); e.Retryable() || !strings.Contains(e.Message, "JanitorAI needs a login") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET example.test/404", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	mux.HandleFunc("GET example.test/410", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) })
	mux.HandleFunc("GET example.test/403", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	mux.HandleFunc("GET example.test/429", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	mux.HandleFunc("GET example.test/503", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	mux.HandleFunc("GET example.test/big", func(w http.ResponseWriter, _ *http.Request) { w.Write(make([]byte, maxBody+1)) })
	mux.HandleFunc("GET example.test/drop", func(w http.ResponseWriter, _ *http.Request) {
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	})
	mux.HandleFunc("GET example.test/redir", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.test/ua", http.StatusFound)
	})
	mux.HandleFunc("GET example.test/multi", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ua", http.StatusMultipleChoices)
	})
	mux.HandleFunc("GET example.test/hop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.test/ua", http.StatusFound)
	})
	mux.HandleFunc("GET example.test/ua", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.UserAgent())) })
	f := sites(t, mux)
	for path, retry := range map[string]bool{"404": false, "410": false, "403": false, "429": true, "503": true, "big": false, "drop": true, "redir": false} {
		_, err := f.get(context.Background(), "Example", "https://example.test/"+path)
		e := rpcErr(t, err)
		if e.Retryable() != retry {
			t.Errorf("%s: retryable = %v, want %v (%v)", path, e.Retryable(), retry, err)
		}
		// A Cloudflare block (403) must never mark a card gone.
		if wantGone := path == "404" || path == "410"; (e.Code == protocol.CodeRemoteNotFound) != wantGone {
			t.Errorf("%s: code = %d, want -32004 only for 404 and 410", path, e.Code)
		}
	}
	if _, err := f.get(context.Background(), "Example", "https://example.test/404"); !strings.Contains(err.Error(), "not found on Example") {
		t.Errorf("404: err = %v", err)
	}
	if _, err := f.get(context.Background(), "Example", "http://example.test/ua"); err == nil || rpcErr(t, err).Retryable() {
		t.Errorf("http URL: err = %v, want permanent refusal", err)
	}
	if _, err := f.get(context.Background(), "Example", "https://example.test/redir"); err == nil || !strings.Contains(err.Error(), "Example redirected off https to http://example.test/ua") {
		t.Errorf("redirect off https: err = %v", err)
	}
	if _, err := f.get(context.Background(), "Example", "https://example.test/multi"); err == nil || strings.Contains(err.Error(), "off https") || !strings.Contains(err.Error(), "Example answered 300") {
		t.Errorf("300 with https Location: err = %v", err)
	}
	if ua, err := f.get(context.Background(), "Example", "https://example.test/hop"); err != nil || len(ua) == 0 {
		t.Errorf("https redirect: %q, %v", ua, err)
	}
	if ua, err := f.get(context.Background(), "Example", "https://example.test/ua"); err != nil || string(ua) != "innkeeper-url-import" {
		t.Errorf("User-Agent = %q, %v", ua, err)
	}
}

func TestWithSource(t *testing.T) {
	const page = "https://chub.ai/characters/a/b"
	for _, in := range []string{
		`{"spec":"chara_card_v3","data":{"name":"X"}}`,
		`{"spec":"chara_card_v3","data":{"name":"X","source":["risurealm:1"]}}`,
		`{"spec":"chara_card_v3","data":{"name":"X","source":["` + page + `"]}}`,
	} {
		out, err := withSource(json.RawMessage(in), page)
		if err != nil {
			t.Fatal(err)
		}
		if src := sources(t, out); !slices.Contains(src, page) || slices.Index(src, page) != len(src)-1 {
			t.Errorf("%s: source = %q", in, src)
		}
	}
	if _, err := withSource(json.RawMessage(`{"data":{"source":"x"}}`), page); err == nil {
		t.Error("a string source was accepted")
	}
}

func TestBadAvatarKeepsDecoded(t *testing.T) {
	var sent []any
	err := emit(found{item: "https://chub.ai/characters/u/s", card: testdata(t, "v2-seraphina.png"), avatar: []byte("not an image")},
		"1.1", t.TempDir(), func(_ string, p any) error { sent = append(sent, p); return nil })
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := sent[0].(protocol.EmitCard)
	if !ok || ev.Avatar == "" {
		t.Errorf("sent[0] = %+v, want an EmitCard with the decoded avatar", sent[0])
	}
}

func TestRefusePrivate(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:443": true, "10.0.0.1:443": true, "169.254.169.254:80": true,
		"[::1]:443": true, "[::ffff:127.0.0.1]:443": true, "0.0.0.0:443": true,
		"93.184.216.34:443": false,
	} {
		if err := refusePrivate("tcp", addr, nil); (err != nil) != want {
			t.Errorf("%s: err = %v, want refused = %v", addr, err, want)
		}
	}
}

func TestGetRefusesPrivateHost(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	_, err := newFetcher().get(context.Background(), "Example", srv.URL)
	if e := rpcErr(t, err); e.Retryable() || !strings.Contains(e.Message, "refused to connect to a private address") {
		t.Errorf("err = %v, want a permanent refusal", err)
	}
}

func TestHTTPOwnerURLUsesHTTPSItem(t *testing.T) {
	sent, err := importURL(t, chubSite(t), "http://chub.ai/characters/u/seraphina")
	if err != nil {
		t.Fatal(err)
	}
	const item = "https://chub.ai/characters/u/seraphina"
	if ev := emitted(t, sent); ev.Item != item || !slices.Contains(sources(t, ev.Card), item) {
		t.Errorf("item %q, source %q", ev.Item, sources(t, ev.Card))
	}
}
