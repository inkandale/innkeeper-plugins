package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkandale/innkeeper-plugins/internal/pgtest"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	workA = "2ef14b67-7d1e-4c74-89f9-f0a859779a59"
	workB = "58f80e70-d3af-4898-87d4-ef0ad333cb3c"
)

var (
	cardJSON = `{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"Vyr","description":"d","first_mes":"hi"}}`
	bookJSON = `{"name":"Atlas","entries":{"0":{"uid":0,"key":["moon"],"content":"The moon is near."}}}`
)

// fakeIllarin is the connected-app side of an Illarin server.
type fakeIllarin struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	grant       []string
	polls       int      // polls answered pending before approval
	refreshes   []string // refresh tokens presented, in order
	refuse      bool     // refuse every refresh
	issued      int
	queue       []map[string]any
	acked       []string
	connectReqs int
}

func newFake(t *testing.T) *fakeIllarin {
	f := &fakeIllarin{t: t, grant: []string{receivePermission}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/connect/requests", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.connectReqs++
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"deviceCode": "dev", "userCode": "ABCD-EFGH",
			"verificationUrl": f.srv.URL + "/link", "expiresAt": time.Now().Add(10 * time.Minute), "interval": 0})
	})
	mux.HandleFunc("POST /api/v1/connect/poll", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.polls > 0 {
			f.polls--
			writeJSON(w, 200, map[string]any{"status": "pending"})
			return
		}
		writeJSON(w, 200, f.credentials("connected"))
	})
	mux.HandleFunc("POST /api/v1/connect/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ RefreshToken string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.refreshes = append(f.refreshes, body.RefreshToken)
		if f.refuse {
			writeJSON(w, 401, map[string]any{"error": "invalid refresh token"})
			return
		}
		writeJSON(w, 200, f.credentials(""))
	})
	mux.HandleFunc("POST /api/v1/works/{id}/receive", func(w http.ResponseWriter, r *http.Request) {
		if !f.authorized(w, r) {
			return
		}
		id := "send-" + r.PathValue("id")
		f.mu.Lock()
		f.queue = append(f.queue, f.send(id, r.PathValue("id"), "character"))
		f.mu.Unlock()
		writeJSON(w, 202, map[string]any{"id": id, "state": "queued"})
	})
	mux.HandleFunc("POST /api/v1/sends/collect", func(w http.ResponseWriter, r *http.Request) {
		if !f.authorized(w, r) {
			return
		}
		var body struct{ Acknowledge []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.acked = append(f.acked, body.Acknowledge...)
		if len(f.queue) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, 200, map[string]any{"sends": f.queue, "takedowns": []any{}})
		f.queue = nil
	})
	mux.HandleFunc("GET /send/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.PathValue("id"), "book") {
			fmt.Fprint(w, bookJSON)
			return
		}
		fmt.Fprint(w, cardJSON)
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// credentials issues a new token pair; called with mu held.
func (f *fakeIllarin) credentials(status string) map[string]any {
	f.issued++
	c := map[string]any{"accessToken": fmt.Sprintf("ia1.%d", f.issued), "accessTokenExpiresAt": time.Now().Add(15 * time.Minute),
		"refreshToken": fmt.Sprintf("ir1.%d", f.issued), "connectedApp": map[string]any{"id": "app", "permissions": f.grant}}
	if status != "" {
		c["status"] = status
	}
	return c
}

func (f *fakeIllarin) authorized(w http.ResponseWriter, r *http.Request) bool {
	f.mu.Lock()
	want := fmt.Sprintf("Bearer ia1.%d", f.issued)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != want {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return false
	}
	return true
}

func (f *fakeIllarin) send(id, work, typ string) map[string]any {
	return map[string]any{"id": id, "workId": work, "versionNumber": 1, "type": typ, "name": "N", "format": "chara_card_v3",
		"files": []any{map[string]any{"type": "export", "url": f.srv.URL + "/send/" + id + "/export?signature=s"}}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// started is an instance of the plugin pointed at f, with its own database schema.
func started(t *testing.T, f *fakeIllarin) *instance {
	t.Helper()
	in, err := newInstance(context.Background(), protocol.InitializeParams{InstanceID: "1", BlobTmp: t.TempDir(),
		DatabaseURL: pgtest.URL(t), Config: map[string]any{"base_url": f.srv.URL}}, f.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(in.client.store.close)
	return in
}

// importPage runs import.run with page, or with sent when page is empty, and returns what it sent.
func importPage(t *testing.T, in *instance, page string) ([]any, error) {
	t.Helper()
	ps, _ := json.Marshal(params{URL: page, Sent: page == ""})
	raw, _ := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: ps})
	var sent []any
	_, err := in.run(context.Background(), func(_ string, p any) error {
		sent = append(sent, p)
		return nil
	}, raw)
	return sent, err
}

func emits[T any](sent []any) []T {
	var out []T
	for _, s := range sent {
		if e, ok := s.(T); ok {
			out = append(out, e)
		}
	}
	return out
}

func TestFirstImportShowsTheCodeThenImportsTheWork(t *testing.T) {
	f := newFake(t)
	f.polls = 1
	in := started(t, f)
	sent, err := importPage(t, in, f.srv.URL+"/a/"+strings.ToUpper(workA)+"/vyr")
	if err != nil {
		t.Fatal(err)
	}
	logs := emits[protocol.Log](sent)
	if len(logs) == 0 || !strings.Contains(logs[0].Message, "ABCD-EFGH") || !strings.Contains(logs[0].Message, f.srv.URL+"/link") {
		t.Fatalf("logs %+v, want the code and where to enter it", logs)
	}
	cards := emits[protocol.EmitCard](sent)
	if len(cards) != 1 || cards[0].Item != f.srv.URL+"/a/"+workA || !strings.Contains(string(cards[0].Card), "Vyr") {
		t.Fatalf("cards %+v", cards)
	}
	if !slices.Equal(f.acked, []string{"send-" + workA}) {
		t.Fatalf("acked %v", f.acked)
	}
	// Connected now: the next import goes straight to the work.
	if _, err := importPage(t, in, f.srv.URL+"/a/"+workB); err != nil {
		t.Fatal(err)
	}
	if f.connectReqs != 1 {
		t.Fatalf("connected %d times, want once", f.connectReqs)
	}
}

func TestImportWithoutURLTakesEverySend(t *testing.T) {
	f := newFake(t)
	in := started(t, f)
	f.queue = []map[string]any{f.send("card1", workA, "character"), f.send("book1", workB, "lorebook")}
	sent, err := importPage(t, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(emits[protocol.EmitCard](sent)); n != 1 {
		t.Fatalf("%d cards, want 1", n)
	}
	books := emits[protocol.EmitLorebook](sent)
	if len(books) != 1 || books[0].Item != f.srv.URL+"/a/"+workB || !strings.Contains(string(books[0].Lorebook), "The moon is near.") {
		t.Fatalf("lorebooks %+v", books)
	}
	if !slices.Equal(f.acked, []string{"card1", "book1"}) {
		t.Fatalf("acked %v", f.acked)
	}
}

func TestBrokenSendIsAcknowledgedAndSkipped(t *testing.T) {
	f := newFake(t)
	in := started(t, f)
	broken := f.send("card1", workA, "character")
	broken["files"] = []any{}
	f.queue = []map[string]any{broken}
	sent, err := importPage(t, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if logs := emits[protocol.Log](sent); len(logs) < 2 || logs[len(logs)-1].Level != "warn" {
		t.Fatalf("logs %+v, want a warning", logs)
	}
	if !slices.Equal(f.acked, []string{"card1"}) {
		t.Fatalf("acked %v", f.acked)
	}
}

func TestExpiredAccessRefreshesWithTheRotatedToken(t *testing.T) {
	f := newFake(t)
	in := started(t, f)
	if _, err := importPage(t, in, ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := in.client.store.pool.Exec(context.Background(), `UPDATE connection SET access_expires = now()`); err != nil {
			t.Fatal(err)
		}
		if _, err := importPage(t, in, ""); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(f.refreshes, []string{"ir1.1", "ir1.2"}) {
		t.Fatalf("refreshed with %v, want each rotated token once", f.refreshes)
	}
}

func TestRefusedRefreshConnectsAgain(t *testing.T) {
	f := newFake(t)
	in := started(t, f)
	if _, err := importPage(t, in, ""); err != nil {
		t.Fatal(err)
	}
	f.refuse = true
	if _, err := in.client.store.pool.Exec(context.Background(), `UPDATE connection SET access_expires = now()`); err != nil {
		t.Fatal(err)
	}
	sent, err := importPage(t, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.connectReqs != 2 || len(f.refreshes) != 1 {
		t.Fatalf("connected %d times after %d refreshes, want a new connection after one refused refresh", f.connectReqs, len(f.refreshes))
	}
	if len(emits[protocol.Log](sent)) == 0 {
		t.Fatal("no code shown for the new connection")
	}
}

func TestConnectionWithoutReceiveFails(t *testing.T) {
	f := newFake(t)
	f.grant = []string{"library:sync"}
	in := started(t, f)
	_, err := importPage(t, in, "")
	if err == nil || !strings.Contains(err.Error(), receivePermission) {
		t.Fatalf("err = %v, want the missing permission named", err)
	}
}

func TestParamsNeedURLOrSent(t *testing.T) {
	f := newFake(t)
	in := started(t, f)
	for _, ps := range []string{`{}`, `{"url": "", "sent": false}`, `{"url": "https://illarin.com/a/x", "sent": true}`, `{"name": "x"}`} {
		raw, _ := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: json.RawMessage(ps)})
		_, err := in.run(context.Background(), func(string, any) error { return nil }, raw)
		var perr *protocol.Error
		if !errors.As(err, &perr) || perr.Code != protocol.CodeInvalidParams {
			t.Errorf("%s: err = %v, want invalid params", ps, err)
		}
	}
	if f.connectReqs != 0 {
		t.Fatal("bad params started a connection")
	}
}

func TestWorkURLs(t *testing.T) {
	c := &client{base: "https://illarin.com"}
	for page, want := range map[string]string{
		"https://illarin.com/a/" + workA + "/vyr":   workA,
		"https://ILLARIN.com/a/" + workA:            workA,
		"https://illarin.com/a/" + workA + "/vyr/":  workA,
		"https://example.com/a/" + workA + "/vyr":   "",
		"https://illarin.com/a/not-a-uuid/vyr":      "",
		"https://illarin.com/u/" + workA:            "",
		"https://illarin.com/a/" + workA + "/x/y/z": "",
	} {
		got, err := c.workID(page)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("workID(%q) = %q, %v; want %q", page, got, err, want)
		}
	}
}

func TestBaseURLMustBeHTTPS(t *testing.T) {
	for raw, ok := range map[string]bool{"": true, "https://fork.example/": true, "http://fork.example": false, "https://fork.example/app": false} {
		if _, err := baseURL(map[string]any{"base_url": raw}); (err == nil) != ok {
			t.Errorf("baseURL(%q) err = %v", raw, err)
		}
	}
}
