package main

import (
	"fmt"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

func TestEmbedReturnsVectorsInTextOrder(t *testing.T) {
	llm := &fakeLLM{}
	h := started(t, withLLM(t, llm, "embed"))
	var res protocol.AIEmbedResult
	if err := h.call(protocol.MethodAIEmbed, protocol.AIEmbedParams{Texts: []string{"a", "b"}}, &res); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Vectors) != "[[0] [1]]" || res.Model != "m" {
		t.Fatalf("vectors %v model %q", res.Vectors, res.Model)
	}
	if req := llm.lastRequest(); req["model"] != "m" || fmt.Sprint(req["input"]) != "[a b]" {
		t.Fatalf("request %v", req)
	}
}

func TestEmbedNotOfferedIsMethodNotFound(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{}))
	if err := h.call(protocol.MethodAIEmbed, protocol.AIEmbedParams{Texts: []string{"a"}}, nil); rpcCode(err) != protocol.CodeMethodNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbedNeedsTexts(t *testing.T) {
	h := started(t, withLLM(t, &fakeLLM{}, "embed"))
	if err := h.call(protocol.MethodAIEmbed, protocol.AIEmbedParams{}, nil); rpcCode(err) != protocol.CodeInvalidParams {
		t.Fatalf("err = %v", err)
	}
}

// A refused key or a missing model fails every text alike, so it must not
// read as the provider refusing this text.
func TestEmbedAuthAndMissingModelAreNotAIRejected(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		h := started(t, withLLM(t, &fakeLLM{status: status}, "embed"))
		err := h.call(protocol.MethodAIEmbed, protocol.AIEmbedParams{Texts: []string{"a"}}, nil)
		if code := rpcCode(err); code != protocol.CodeFailed {
			t.Errorf("%d: err = %v, want code %d", status, err, protocol.CodeFailed)
		}
	}
}
