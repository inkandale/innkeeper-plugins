package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

func newPlugin(t *testing.T, f *fake, username, password string) *plugin {
	return &plugin{c: newClient(f.srv.URL+"/", username, password), blobTmp: t.TempDir()}
}

func TestSignsInOnceAndAgainWhenTheSessionEnds(t *testing.T) {
	f := newFake(t)
	p := newPlugin(t, f, "owner", fakePassword)
	ctx := context.Background()
	for range 2 {
		if err := p.c.call(ctx, "GET", "/world-books", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if f.signIns != 1 {
		t.Fatalf("signed in %d times, want 1", f.signIns)
	}
	f.token = "expired-elsewhere"
	if err := p.c.call(ctx, "GET", "/world-books", nil, nil); err != nil || f.signIns != 2 {
		t.Fatalf("after the session ended: %v, %d sign-ins", err, f.signIns)
	}
}

func TestSignsInByEmail(t *testing.T) {
	f := newFake(t)
	if err := newPlugin(t, f, "owner@example.com", fakePassword).c.call(context.Background(), "GET", "/world-books", nil, nil); err != nil {
		t.Fatal(err)
	}
}

// Five rejected sign-ins lock the server out of Lumiverse, so one is the limit.
func TestStopsSigningInAfterARejection(t *testing.T) {
	f := newFake(t)
	p := newPlugin(t, f, "owner", "wrong")
	for range 3 {
		err := p.c.call(context.Background(), "GET", "/world-books", nil, nil)
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Retryable() {
			t.Fatalf("call: %v, want a final error", err)
		}
	}
	if f.signIns != 1 {
		t.Fatalf("signed in %d times, want 1", f.signIns)
	}
}

func TestErrors(t *testing.T) {
	f := newFake(t)
	p := newPlugin(t, f, "owner", fakePassword)
	err := p.c.call(context.Background(), "GET", "/characters/nope", nil, nil)
	if !httpkit.NotFound(err) {
		t.Fatalf("missing character: %v", err)
	}
	f.srv.Close()
	err = p.c.call(context.Background(), "GET", "/world-books", nil, nil)
	var pe *protocol.Error
	if !errors.As(err, &pe) || !pe.Retryable() {
		t.Fatalf("server down: %v, want retryable", err)
	}
}

func TestDecodeRaw(t *testing.T) {
	p := newPlugin(t, newFake(t), "owner", fakePassword)
	var b []byte
	if err := p.c.call(context.Background(), "GET", "/raw", nil, &b); err != nil {
		t.Fatal(err)
	}
	if want := "\x89PNG\r\n\x1a\nnot json"; string(b) != want {
		t.Fatalf("got %q, want %q", b, want)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
