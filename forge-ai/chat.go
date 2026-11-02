package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// maxToolRounds caps a run's requests to the model; the last offers only the closing tool, or none.
const maxToolRounds = 8

// libraryTools are the core's read tools a library action may call. The
// schemas are the parts of Innkeeper's MCP tool inputs a model needs.
var libraryTools = []protocol.ChatTool{
	tool("search_cards", "Search the library's cards by text. Returns matching cards with the fields asked for.",
		map[string]any{"query": map[string]any{"type": "object", "properties": map[string]any{
			"text":   map[string]any{"type": "string", "description": "Words to match in name, description, personality, scenario, creator notes, and first message"},
			"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Card data keys to return per hit. Default: name"},
		}}}, "query"),
	tool("get_card", "Get a card by ID: its newest revision and linked lorebooks.",
		map[string]any{"id": map[string]any{"type": "integer"}}, "id"),
	tool("get_lorebook", "Get a lorebook by ID: its newest revision.",
		map[string]any{"id": map[string]any{"type": "integer"}}, "id"),
	tool("get_history", "List a card's or lorebook's revisions, newest first.",
		map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"card", "lorebook"}}, "id": map[string]any{"type": "integer"}}, "kind", "id"),
}

func tool(name, description string, properties map[string]any, required ...string) protocol.ChatTool {
	params, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required})
	return protocol.ChatTool{Name: name, Description: description, Parameters: params}
}

// toolFunc answers one tool call.
type toolFunc func(ctx context.Context, name, arguments string) (string, error)

// coreLibrary sends a run's tool calls to the core. A refusal or a tool
// error goes back to the model as the tool's result; a run that has ended
// stops the loop.
func coreLibrary(conn *protocol.Conn, run string) toolFunc {
	return func(ctx context.Context, name, arguments string) (string, error) {
		if arguments == "" {
			arguments = "{}"
		}
		if !json.Valid([]byte(arguments)) {
			return "error: the arguments aren't valid JSON", nil
		}
		var res protocol.MCPCallResult
		err := conn.Call(ctx, protocol.MethodLibraryCall,
			protocol.LibraryCallParams{Run: run, Name: name, Arguments: json.RawMessage(arguments)}, &res)
		var rpcErr *protocol.Error
		if errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeInvalidParams {
			return "error: " + rpcErr.Message, nil
		}
		if err != nil {
			return "", err
		}
		var text []string
		for _, c := range res.Content {
			text = append(text, c.Text)
		}
		if res.IsError {
			return "error: " + strings.Join(text, "\n"), nil
		}
		return strings.Join(text, "\n"), nil
	}
}

// errToolsRejected is the model refusing tools on a run that can't end without them.
var errToolsRejected = errors.New("the model refused the tools")

// closer is a tool that ends a run. accept checks a call's arguments and
// returns a complaint for the model, or "" to end the run.
type closer struct {
	tool   protocol.ChatTool
	accept func(arguments string) string
}

// toolbox is the tools a run offers the model and what answers their calls.
// With closing set, the run ends on that tool rather than on a reply
// without tool calls.
type toolbox struct {
	tools   []protocol.ChatTool
	call    toolFunc
	closing *closer
}

// offered is what a round offers: every tool before the last round, and in
// the last only the closing tool, or none.
func (b toolbox) offered(round int) []protocol.ChatTool {
	switch {
	case round < maxToolRounds && b.closing != nil:
		return append(slices.Clone(b.tools), b.closing.tool)
	case round < maxToolRounds:
		return b.tools
	case b.closing != nil:
		return []protocol.ChatTool{b.closing.tool}
	}
	return nil
}

func (b toolbox) closes(c protocol.ToolCall) bool {
	return b.closing != nil && c.Name == b.closing.tool.Name
}

// complete runs the conversation to the model's final answer, answering its
// tool calls through box. newProgress gives each request its own callback,
// which gets that request's text so far.
func (srv *server) complete(ctx context.Context, in *instance, run string, params protocol.AIChatParams, box toolbox, newProgress func() func(string)) (protocol.AIChatResult, error) {
	params.Instance = in.settings.Connection
	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return protocol.AIChatResult{}, err
		}
		params.Tools = box.offered(round)
		res, err := srv.chat(ctx, in, run, params, newProgress())
		if err != nil && round == 1 && params.Tools != nil && rejected(err) {
			if box.closing != nil {
				return protocol.AIChatResult{}, fmt.Errorf("%w: %w", errToolsRejected, err)
			}
			// Most likely a model without tool support; the context and forced skills in the prompt still apply.
			fmt.Fprintf(os.Stderr, "retrying without tools: %v\n", err)
			box.tools, params.Tools = nil, nil
			res, err = srv.chat(ctx, in, run, params, newProgress())
		}
		if err != nil || len(res.ToolCalls) == 0 || params.Tools == nil {
			return res, err
		}
		// Reasoning stays out of the history the model reads back.
		params.Messages = append(params.Messages, protocol.ChatMessage{Role: "assistant", Content: strings.TrimSpace(visible(res.Text)), ToolCalls: res.ToolCalls})
		// Other tools go first, so the closing call can use what they found in the same round.
		for _, c := range res.ToolCalls {
			if box.closes(c) {
				continue
			}
			// The last round offers only the closing tool, so a call to any other is unknown.
			if !slices.ContainsFunc(params.Tools, func(t protocol.ChatTool) bool { return t.Name == c.Name }) {
				params.Messages = append(params.Messages, protocol.ChatMessage{Role: "tool", Content: fmt.Sprintf("error: no tool %q", c.Name), ToolCallID: c.ID})
				continue
			}
			result, err := box.call(ctx, c.Name, c.Arguments)
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				return protocol.AIChatResult{}, fmt.Errorf("tool %s: %w", c.Name, err)
			}
			params.Messages = append(params.Messages, protocol.ChatMessage{Role: "tool", Content: result, ToolCallID: c.ID})
		}
		for _, c := range res.ToolCalls {
			if !box.closes(c) {
				continue
			}
			complaint := box.closing.accept(c.Arguments)
			if complaint == "" {
				return res, nil
			}
			params.Messages = append(params.Messages, protocol.ChatMessage{Role: "tool", Content: complaint, ToolCallID: c.ID})
		}
		// Only a run with a closing tool reaches its last round's end here.
		if round >= maxToolRounds {
			return res, nil
		}
	}
}

// chat sends one ai.chat through the core under its own streaming token,
// passing onText the reply's text so far as it streams.
func (srv *server) chat(ctx context.Context, in *instance, run string, params protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error) {
	params.Run = fmt.Sprintf("%s/%d", run, srv.chats.Add(1))
	var text strings.Builder
	srv.deltas.Store(params.Run, func(piece string) {
		text.WriteString(piece)
		onText(text.String())
	})
	defer srv.deltas.Delete(params.Run)
	var res protocol.AIChatResult
	if err := in.conn.Call(ctx, protocol.MethodAIChat, params, &res); err != nil {
		return res, chatError{err}
	}
	return res, nil
}

// chatError is an ai.chat that failed, kept apart from a tool's failure in the same turn.
type chatError struct{ err error }

func (e chatError) Error() string { return e.err.Error() }
func (e chatError) Unwrap() error { return e.err }

// rejected reports a provider refusing the request as invalid.
func rejected(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeAIRejected
}

// imagePart reads an image from the blob store as a data URL.
func imagePart(path string) (protocol.ContentPart, error) {
	b, err := pluginio.ReadFile(path)
	if err != nil {
		return protocol.ContentPart{}, fmt.Errorf("read image: %w", err)
	}
	url := "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b)
	return protocol.ContentPart{Type: "image_url", ImageURL: url}, nil
}
