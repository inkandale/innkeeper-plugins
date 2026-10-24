package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
	llmerrors "github.com/mozilla-ai/any-llm-go/errors"
)

func (c *connection) chat(ctx context.Context, params json.RawMessage) (any, error) {
	if err := c.offering(protocol.MethodAIChat); err != nil {
		return nil, err
	}
	var p protocol.AIChatParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Messages) == 0 {
		return nil, protocol.InvalidParams(errors.New("ai.chat needs messages"))
	}
	req, err := completionParams(c.settings.Model, p)
	if err != nil {
		return nil, protocol.InvalidParams(err)
	}
	text, calls, err := stream(ctx, c, req, func(piece string) {
		if p.Run != "" {
			_ = protocol.Peer(ctx).Notify(protocol.NotifyAIDelta, protocol.AIDelta{Run: p.Run, Text: piece})
		}
	})
	if err != nil {
		return nil, modelError(err)
	}
	res := protocol.AIChatResult{Text: text, Model: c.settings.Model}
	for _, tc := range calls {
		call := protocol.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}
		if tc.Extra != nil {
			if call.Extra, err = json.Marshal(tc.Extra); err != nil {
				return nil, fmt.Errorf("tool call %s: extra: %w", tc.ID, err)
			}
		}
		res.ToolCalls = append(res.ToolCalls, call)
	}
	return res, nil
}

// completionParams converts an ai.chat request to any-llm-go's.
func completionParams(model string, p protocol.AIChatParams) (anyllm.CompletionParams, error) {
	req := anyllm.CompletionParams{Model: model, Temperature: p.Temperature, MaxTokens: p.MaxTokens}
	for _, m := range p.Messages {
		msg := anyllm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.Parts) > 0 {
			parts := make([]anyllm.ContentPart, 0, len(m.Parts))
			for _, part := range m.Parts {
				cp := anyllm.ContentPart{Type: part.Type, Text: part.Text}
				if part.ImageURL != "" {
					cp.ImageURL = &anyllm.ImageURL{URL: part.ImageURL}
				}
				parts = append(parts, cp)
			}
			msg.Content = parts
		}
		for _, tc := range m.ToolCalls {
			call := anyllm.ToolCall{ID: tc.ID, Type: "function",
				Function: anyllm.FunctionCall{Name: tc.Name, Arguments: tc.Arguments}}
			if len(tc.Extra) > 0 {
				if err := json.Unmarshal(tc.Extra, &call.Extra); err != nil {
					return req, fmt.Errorf("tool call %s: extra: %w", tc.ID, err)
				}
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		}
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range p.Tools {
		var schema map[string]any
		if err := json.Unmarshal(t.Parameters, &schema); err != nil {
			return req, fmt.Errorf("tool %s: parameters: %w", t.Name, err)
		}
		req.Tools = append(req.Tools, anyllm.Tool{Type: "function",
			Function: anyllm.Function{Name: t.Name, Description: t.Description, Parameters: schema}})
	}
	return req, nil
}

// stream runs one streamed request, passing each piece of text to onText,
// and returns the whole text and any tool calls.
func stream(ctx context.Context, c *connection, params anyllm.CompletionParams, onText func(string)) (string, []anyllm.ToolCall, error) {
	params.Stream = true
	chunks, errs := c.llm.CompletionStream(ctx, params)
	var text strings.Builder
	var calls []anyllm.ToolCall
	for chunk := range chunks {
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				onText(ch.Delta.Content)
			}
			calls = mergeToolCalls(c.settings.Provider, calls, ch.Delta.ToolCalls)
		}
	}
	if err := <-errs; err != nil {
		return "", nil, err
	}
	return text.String(), calls, ctx.Err()
}

// mergeToolCalls adds streamed tool call deltas to calls. any-llm-go passes
// on each provider's own streaming shape, with no index to match on:
//   - anthropic resends the whole call so far under its ID;
//   - ollama sends whole calls, numbered within each chunk, so IDs repeat;
//   - the rest send whole calls with unique IDs, or OpenAI-style fragments
//     whose first carries the ID and later ones carry it or nothing.
func mergeToolCalls(provider string, calls, deltas []anyllm.ToolCall) []anyllm.ToolCall {
	for _, d := range deltas {
		d.Type = "function"
		i := slices.IndexFunc(calls, func(c anyllm.ToolCall) bool { return d.ID != "" && c.ID == d.ID })
		switch {
		case provider == "ollama" || len(calls) == 0 || (d.ID != "" && i < 0):
			calls = append(calls, d)
		case provider == "anthropic" && i >= 0:
			calls[i] = d
		default:
			if i < 0 {
				i = len(calls) - 1
			}
			calls[i].Function.Name += d.Function.Name
			calls[i].Function.Arguments += d.Function.Arguments
		}
	}
	return calls
}

// modelError gives a model failure the code a consumer branches on: a
// refused request, which retrying won't fix, or a retryable one. A refused
// key or a missing model is the instance's fault, not the request's.
func modelError(err error) error {
	var provider *llmerrors.ProviderError
	switch {
	case errors.Is(err, llmerrors.ErrAuthentication), errors.Is(err, llmerrors.ErrMissingAPIKey),
		errors.Is(err, llmerrors.ErrModelNotFound),
		errors.As(err, &provider) && slices.Contains([]int{401, 403, 404}, provider.StatusCode):
		return protocol.NewError(protocol.CodeFailed, err.Error(), false)
	case errors.Is(err, llmerrors.ErrInvalidRequest), errors.Is(err, llmerrors.ErrUnsupportedParam),
		errors.Is(err, llmerrors.ErrContextLength), errors.Is(err, llmerrors.ErrContentFilter):
		return protocol.NewError(protocol.CodeAIRejected, err.Error(), false)
	// A provider error's status says whether a retry can help: a server error or
	// no response may pass, but a client error such as 422 won't.
	case errors.As(err, &provider) && provider.StatusCode >= 400 && provider.StatusCode < 500:
		return protocol.NewError(protocol.CodeAIRejected, err.Error(), false)
	case errors.Is(err, llmerrors.ErrRateLimit), errors.Is(err, llmerrors.ErrProvider),
		errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.CodeInternalError, err.Error(), true)
	}
	return err
}
