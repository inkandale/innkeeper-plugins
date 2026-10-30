package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

func TestFetcherSpacesRequestsToOneHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/x", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET avatars.charhub.io/x", func(http.ResponseWriter, *http.Request) {})
	f := sites(t, mux)
	f.gap = 200 * time.Millisecond
	start := time.Now()
	// The second host doesn't wait; the third request waits for the first's gap.
	for _, u := range []string{"https://api.chub.ai/x", "https://avatars.charhub.io/x", "https://api.chub.ai/x"} {
		if _, err := f.get(context.Background(), "test", u); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < f.gap {
		t.Errorf("three requests took %v, want at least one gap of %v", d, f.gap)
	}
}

func TestFetcherWaitsOutA429(t *testing.T) {
	var asked int
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/x", func(w http.ResponseWriter, _ *http.Request) {
		if asked++; asked == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	f := sites(t, mux)
	f.pause = 200 * time.Millisecond
	start := time.Now()
	if _, err := f.get(context.Background(), "test", "https://api.chub.ai/x"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); asked != 2 || d < f.pause {
		t.Errorf("asked %d times in %v, want 2 after a pause of %v", asked, d, f.pause)
	}
}

func TestFetcherGivesUpOnAHostThatKeepsAnswering429(t *testing.T) {
	var asked int
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/x", func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	f := sites(t, mux)
	_, err := f.get(context.Background(), "test", "https://api.chub.ai/x")
	var perr *protocol.Error
	if !errors.As(err, &perr) || !perr.Retryable() || asked != rateTries {
		t.Fatalf("asked %d times, err %v; want %d asks and a retryable error", asked, err, rateTries)
	}
}
