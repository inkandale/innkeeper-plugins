package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/inkandale/innkeeper-plugins/internal/pgtest"
)

func openTestStore(t *testing.T) *store {
	t.Helper()
	st, err := openStore(t.Context(), pgtest.URL(t), "1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.close)
	return st
}

func TestStoreKeepsAConversationInOrder(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	id, err := st.createConversation(ctx, "A slow-burn rival")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.addMessage(ctx, id, "user", "A slow-burn rival", nil); err != nil {
		t.Fatal(err)
	}
	blocks := []block{{Type: "text", Markdown: "Try Brakka."}, {Type: "picks", Picks: []pick{{CardID: 41, Name: "Brakka", Reason: "Rival."}}}}
	if err := st.addMessage(ctx, id, "assistant", "", blocks); err != nil {
		t.Fatal(err)
	}
	got, err := st.messages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Role != "user" || got[0].Blocks != nil || got[1].Blocks[1].Picks[0].CardID != 41 {
		raw, _ := json.Marshal(got)
		t.Fatalf("messages: %s", raw)
	}
}

func TestStoreSeparatesInstances(t *testing.T) {
	dsn := pgtest.URL(t)
	ctx := t.Context()
	a, err := openStore(ctx, dsn, "1")
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	b, err := openStore(ctx, dsn, "2")
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	id, err := a.createConversation(ctx, "Mine")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.messages(ctx, id); !errors.Is(err, errNoConversation) {
		t.Fatalf("another instance read it: %v", err)
	}
	if err := b.deleteConversation(ctx, id); !errors.Is(err, errNoConversation) {
		t.Fatalf("another instance deleted it: %v", err)
	}
}

func TestStoreDeletesMessagesWithConversation(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	id, _ := st.createConversation(ctx, "Gone")
	_ = st.addMessage(ctx, id, "user", "Gone", nil)
	if err := st.deleteConversation(ctx, id); err != nil {
		t.Fatal(err)
	}
	list, err := st.conversations(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("list %v, err %v", list, err)
	}
}
