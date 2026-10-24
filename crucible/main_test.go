package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

func TestEnrichForwardsEachRevision(t *testing.T) {
	var mu sync.Mutex // guards got against the handler goroutines
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		if body["card_id"].(float64) == 2 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"this card has changed since"}`))
			return
		}
		_, _ = w.Write([]byte(`{"values":{"quality":72.5,"flags":["minor risk"]},"meta":{"source":"test"}}`))
	}))
	defer srv.Close()

	s := &settings{URL: srv.URL}
	params, _ := json.Marshal(protocol.EnrichRunParams{Run: "r", Revisions: []protocol.EnrichRevision{
		{CardID: 1, RevisionID: 10, Data: json.RawMessage(`{"name":"Mira"}`)},
		{CardID: 2, RevisionID: 20, Data: json.RawMessage(`{"name":"Bazaar"}`)},
	}})
	res, err := enrich(context.Background(), s, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || res.Results[0].RevisionID != 10 || string(res.Results[0].Values["quality"]) != "72.5" {
		t.Fatalf("first result: %+v", res.Results[0])
	}
	if res.Results[1].Error != "this card has changed since" {
		t.Fatalf("second result: %+v", res.Results[1])
	}
	if len(got) != 2 {
		t.Fatalf("requests: %d", len(got))
	}
}

func TestEnrichFailsWithoutAURL(t *testing.T) {
	s := parseSettings(map[string]any{"url": " "})
	params, _ := json.Marshal(protocol.EnrichRunParams{Run: "r", Revisions: []protocol.EnrichRevision{
		{CardID: 1, RevisionID: 10, Data: json.RawMessage(`{}`)},
	}})
	if _, err := enrich(context.Background(), &s, params); err == nil || err.Error() != "set the service URL" {
		t.Fatalf("err = %v", err)
	}
}

// The service scores one card at a time, so a request sent alongside others
// waits its turn inside the client timeout.
func TestEnrichSendsOneRevisionAtATime(t *testing.T) {
	var inFlight, most atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		if n > most.Load() {
			most.Store(n)
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"values":{"quality":1}}`))
	}))
	defer srv.Close()

	var revs []protocol.EnrichRevision
	for i := range 5 {
		revs = append(revs, protocol.EnrichRevision{CardID: int64(i), RevisionID: int64(i), Data: json.RawMessage(`{}`)})
	}
	params, _ := json.Marshal(protocol.EnrichRunParams{Run: "r", Revisions: revs})
	if _, err := enrich(context.Background(), &settings{URL: srv.URL}, params); err != nil {
		t.Fatal(err)
	}
	if got := most.Load(); got != 1 {
		t.Fatalf("%d requests in flight at once, want 1", got)
	}
}
