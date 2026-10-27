package main

import (
	"net/http"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	risuNormal = "11111111-2222-3333-4444-555555555555"
	risuCharX  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

// risuData is a card page's __data.json, trimmed from the 2026-10-13 probe.
func risuData(typ string) string {
	return `{"type":"data","nodes":[null,{"type":"data","data":[{"card":1,"username":-1},` +
		`{"name":2,"img":3,"type":4},"Mirelle","abc123","` + typ + `"]}]}`
}

func risuSite(t *testing.T) *fetcher {
	t.Helper()
	cardJSON := testdata(t, "v3-mirelle.json")
	avatar := testdata(t, "v2-seraphina.png")
	charx := append([]byte("\xff\xd8\xff\xe0 JPEG"), testdata(t, "v3-mirelle.charx")...)
	mux := http.NewServeMux()
	mux.HandleFunc("GET realm.risuai.net/character/{id}/__data.json", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case risuNormal:
			w.Write([]byte(risuData("normal")))
		case risuCharX:
			w.Write([]byte(risuData("charx")))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET realm.risuai.net/api/v1/download/json-v3/"+risuNormal, func(w http.ResponseWriter, _ *http.Request) { w.Write(cardJSON) })
	mux.HandleFunc("GET realm.risuai.net/api/v1/download/charx-v3/"+risuCharX, func(w http.ResponseWriter, _ *http.Request) { w.Write(charx) })
	mux.HandleFunc("GET sv.risuai.xyz/resource/abc123", func(w http.ResponseWriter, _ *http.Request) { w.Write(avatar) })
	return sites(t, mux)
}

func TestRisu(t *testing.T) {
	f := risuSite(t)
	for _, id := range []string{risuNormal, risuCharX} {
		item := "https://realm.risuai.net/character/" + id
		sent, err := importURL(t, f, item+"/")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		ev := emitted(t, sent)
		if ev.Item != item || ev.Avatar == "" || !slices.Contains(sources(t, ev.Card), item) {
			t.Errorf("%s: item %q, avatar %q, source %q", id, ev.Item, ev.Avatar, sources(t, ev.Card))
		}
	}
}

func TestRisuErrors(t *testing.T) {
	f := risuSite(t)
	for _, page := range []string{"https://realm.risuai.net/character/not-a-uuid", "https://realm.risuai.net/"} {
		if _, err := importURL(t, f, page); rpcErr(t, err).Code != protocol.CodeInvalidParams {
			t.Errorf("%s: err = %v, want invalid params", page, err)
		}
	}
	_, err := importURL(t, f, "https://realm.risuai.net/character/99999999-2222-3333-4444-555555555555")
	if e := rpcErr(t, err); e.Code != protocol.CodeRemoteNotFound || e.Retryable() || e.Message != "Nothing at that address on RisuRealm. Check the link" {
		t.Errorf("missing card: err = %v", err)
	}
}

func TestRisuOddAnswers(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	for name, body := range map[string]string{
		"no card node": `{"type":"data","nodes":[null,{"type":"data","data":[{"user":1},"x"]}]}`,
		"unknown type": risuData("hologram"),
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET realm.risuai.net/character/{id}/__data.json", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
		_, err := importURL(t, sites(t, mux), "https://realm.risuai.net/character/"+id)
		if e := rpcErr(t, err); e.Retryable() || e.Code != protocol.CodeInternalError {
			t.Errorf("%s: err = %v, want a permanent error", name, err)
		}
	}
}
