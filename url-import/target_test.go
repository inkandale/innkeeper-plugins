package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// get runs target.get for remoteID.
func get(t *testing.T, f *fetcher, remoteID string, avatar bool) (protocol.TargetGetResult, error) {
	t.Helper()
	raw, _ := json.Marshal(protocol.TargetGetParams{RemoteID: remoteID, Avatar: avatar})
	res, err := getCard(context.Background(), f, t.TempDir(), raw)
	if err != nil {
		return protocol.TargetGetResult{}, err
	}
	return res.(protocol.TargetGetResult), nil
}

// getBook runs target.get_lorebook for remoteID.
func getBook(t *testing.T, f *fetcher, remoteID string) (protocol.TargetGetLorebookResult, error) {
	t.Helper()
	raw, _ := json.Marshal(protocol.TargetGetParams{RemoteID: remoteID})
	res, err := getLorebook(context.Background(), f, raw)
	if err != nil {
		return protocol.TargetGetLorebookResult{}, err
	}
	return res.(protocol.TargetGetLorebookResult), nil
}

func TestTargetDescribeLeavesOutBooksAndSources(t *testing.T) {
	if err := targetInfo.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"character_book", "source"} {
		if slices.Contains(targetInfo.Fields, field) {
			t.Errorf("fields include %s", field)
		}
	}
	if !targetInfo.Pull || targetInfo.Update {
		t.Errorf("pull %v, update %v; want a target that only pulls", targetInfo.Pull, targetInfo.Update)
	}
}

func TestTargetGetChubCard(t *testing.T) {
	f := chubSite(t)
	res, err := get(t, f, "chub:u/seraphina", true)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Spec string `json:"spec"`
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Card, &c); err != nil || c.Spec != "chara_card_v3" || c.Data.Name == "" {
		t.Fatalf("card %s, err %v", res.Card, err)
	}
	if res.Avatar == "" {
		t.Error("no avatar, though asked for one")
	}
	again, err := get(t, f, "chub:u/seraphina", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Avatar != "" {
		t.Error("an avatar, though not asked for one")
	}
	if !bytes.Equal(again.Card, res.Card) {
		t.Error("two fetches of an unchanged page differ")
	}
}

func TestTargetGetGone(t *testing.T) {
	f := chubSite(t)
	for _, id := range []string{"chub:u/gone", "chub:onlyone", "janitor:a/b", "nokind", "risu:not-a-uuid", "innkeeper-contract:none"} {
		_, err := get(t, f, id, false)
		if e := rpcErr(t, err); e.Code != protocol.CodeRemoteNotFound || e.Retryable() {
			t.Errorf("%s: err = %v, want -32004", id, err)
		}
	}
}

func TestTargetGetOutageIsRetryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/api/characters/u/down", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := get(t, sites(t, mux), "chub:u/down", false); !rpcErr(t, err).Retryable() {
		t.Errorf("err = %v, want retryable", err)
	}
}

func TestTargetGetChubLorebook(t *testing.T) {
	f := chubSite(t)
	got, err := getBook(t, f, "chub:u/eldoria")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lorebook) == 0 {
		t.Fatal("no lorebook")
	}
	// RisuRealm and Character Tavern have no lorebook pages.
	for _, id := range []string{"chub:u/hidden", "risu:09db4bf3-d6c7-467c-a35c-71b38b0299ba", "character-tavern:a/b", "innkeeper-contract:none"} {
		_, err := getBook(t, f, id)
		if e := rpcErr(t, err); e.Code != protocol.CodeRemoteNotFound || e.Retryable() {
			t.Errorf("%s: err = %v, want -32004", id, err)
		}
	}
}
