package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	// maxRounds caps a turn's requests to the model. The last offers only recommend.
	maxRounds = 8
	maxPicks  = 6
	// maxHistory is how many past messages a turn sends the model.
	maxHistory = 20
	// noAnswer stands in for a reply with no text and no picks.
	noAnswer = "I couldn't come up with an answer. Try asking another way."
)

type turnInput struct {
	Conversation int64  `json:"conversation"`
	Text         string `json:"text"`
	Library      int64  `json:"library"`
	AllLibraries bool   `json:"all_libraries"`
}

type turnResult struct {
	Conversation int64  `json:"conversation"`
	Text         string `json:"text"`
	Picks        []pick `json:"picks"`
}

// turnDeps are what a turn calls out to, so tests can script the model.
type turnDeps struct {
	chat    func(ctx context.Context, p protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error)
	library func(name, arguments string) (string, error)
	show    func(uiEvent)
}

// turn is the state of one turn's tool calls.
type turn struct {
	deps  turnDeps
	input turnInput
	// seen maps each card the tools returned to its name. Only these can be picks.
	seen map[int64]string
	// fatal is a tool failure that ends the turn.
	fatal error
}

func runTurn(ctx context.Context, deps turnDeps, input turnInput, history []message) (turnResult, error) {
	t := &turn{deps: deps, input: input, seen: map[int64]string{}}
	res, err := t.run(ctx, history)
	if err != nil {
		return turnResult{}, err
	}
	if res.Picks == nil {
		res.Picks = []pick{}
	}
	if res.Text == "" && len(res.Picks) == 0 {
		res.Text = noAnswer
	}
	return res, nil
}

func (t *turn) run(ctx context.Context, history []message) (turnResult, error) {
	messages := []protocol.ChatMessage{{Role: "system", Content: systemPrompt}}
	for _, m := range history[max(len(history)-maxHistory, 0):] {
		messages = append(messages, protocol.ChatMessage{Role: m.Role, Content: withPicks(m)})
	}
	messages = append(messages, protocol.ChatMessage{Role: "user", Content: t.input.Text})
	var lastText string
	for round := 1; round <= maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return turnResult{}, err
		}
		tools := searchTools
		if round == maxRounds {
			tools = []protocol.ChatTool{recommendTool}
		}
		// Text beside a tool call is narration, which the final reply replaces.
		if round > 1 {
			t.deps.show(uiEvent{Reset: true})
		}
		live := &liveText{show: t.deps.show}
		res, err := t.deps.chat(ctx, protocol.AIChatParams{Messages: messages, Tools: tools}, live.add)
		if err != nil {
			return turnResult{}, err
		}
		text := strings.TrimSpace(visible(res.Text))
		if len(res.ToolCalls) == 0 {
			return turnResult{Text: text}, nil
		}
		lastText = cmp.Or(text, lastText)
		messages = append(messages, protocol.ChatMessage{Role: "assistant", Content: text, ToolCalls: res.ToolCalls})
		// Other tools go first, so recommend can pick from a search in the same round.
		for _, c := range res.ToolCalls {
			if c.Name != "recommend" {
				messages = append(messages, protocol.ChatMessage{Role: "tool", ToolCallID: c.ID, Content: t.answer(c)})
			}
		}
		if t.fatal != nil {
			return turnResult{}, t.fatal
		}
		for _, c := range res.ToolCalls {
			if c.Name != "recommend" {
				continue
			}
			done, complaint := t.finish(c.Arguments, text)
			if complaint == "" {
				return done, nil
			}
			messages = append(messages, protocol.ChatMessage{Role: "tool", ToolCallID: c.ID, Content: complaint})
		}
	}
	return turnResult{Text: lastText}, nil
}

// withPicks is a saved message as the model reads it. A reply can be picks
// alone, and providers can refuse an empty message.
func withPicks(m message) string {
	var b strings.Builder
	b.WriteString(m.Content)
	for _, p := range m.Picks {
		fmt.Fprintf(&b, "\nPicked %s (card %d): %s", p.Name, p.CardID, p.Reason)
	}
	return strings.TrimSpace(b.String())
}

// finish keeps the picks a tool really returned, without repeats. text is
// what the model wrote beside the call. A nonempty complaint is the tool
// result that lets the model try again.
func (t *turn) finish(arguments, text string) (turnResult, string) {
	var args struct {
		Message string `json:"message"`
		Picks   []struct {
			CardID int64  `json:"card_id"`
			Reason string `json:"reason"`
		} `json:"picks"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return turnResult{}, "error: recommend's arguments aren't valid: " + err.Error()
	}
	reply := cmp.Or(strings.TrimSpace(args.Message), text)
	if reply == "" && len(args.Picks) == 0 {
		return turnResult{}, "error: recommend needs a message or picks"
	}
	picks := []pick{}
	for _, p := range args.Picks {
		name, ok := t.seen[p.CardID]
		repeat := slices.ContainsFunc(picks, func(q pick) bool { return q.CardID == p.CardID })
		if ok && !repeat && len(picks) < maxPicks {
			picks = append(picks, pick{CardID: p.CardID, Name: name, Reason: p.Reason})
		}
	}
	return turnResult{Text: reply, Picks: picks}, ""
}

// send runs one turn from the panel.
func (srv *server) send(ctx context.Context, in *instance, p protocol.UICallParams) (any, error) {
	var input turnInput
	if err := json.Unmarshal(p.Params, &input); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if strings.TrimSpace(input.Text) == "" {
		return nil, protocol.InvalidParams(errors.New("text is empty"))
	}
	return converse(ctx, in.store, srv.deps(ctx, in, p.Run), input)
}

// converse saves the owner's message before the turn, so a dropped stream can
// retry. A retry resends that message, which is then already the last one.
func converse(ctx context.Context, st *store, deps turnDeps, input turnInput) (turnResult, error) {
	if input.Conversation == 0 {
		id, err := st.createConversation(ctx, title(input.Text))
		if err != nil {
			return turnResult{}, wrapStore(err)
		}
		input.Conversation = id
		deps.show(uiEvent{Conversation: id})
	}
	history, err := st.messages(ctx, input.Conversation)
	if err != nil {
		return turnResult{}, wrapStore(err)
	}
	if n := len(history); n > 0 && history[n-1].Role == "user" && history[n-1].Content == input.Text {
		history = history[:n-1]
	} else if err := st.addMessage(ctx, input.Conversation, "user", input.Text, nil); err != nil {
		return turnResult{}, wrapStore(err)
	}
	res, err := runTurn(ctx, deps, input, history)
	if err != nil {
		return turnResult{}, turnFailure(err)
	}
	if err := st.addMessage(ctx, input.Conversation, "assistant", res.Text, res.Picks); err != nil {
		return turnResult{}, wrapStore(err)
	}
	res.Conversation = input.Conversation
	return res, nil
}

func title(text string) string {
	runes := []rune(strings.TrimSpace(text))
	return string(runes[:min(len(runes), 60)])
}

// turnFailure turns a failure the owner can fix into a message they can read.
func turnFailure(err error) error {
	if errors.Is(err, errNoAccess) {
		return protocol.Fail(err.Error(), false)
	}
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeAIRejected {
		return protocol.Fail("This AI connection needs a model with tool support.", false)
	}
	return err
}

func (srv *server) deps(ctx context.Context, in *instance, run string) turnDeps {
	show := func(e uiEvent) {
		data, _ := json.Marshal(e)
		_ = in.conn.Notify(protocol.NotifyUIEvent, protocol.UIEvent{Run: run, Data: data})
	}
	return turnDeps{
		show: show,
		chat: func(ctx context.Context, p protocol.AIChatParams, onText func(string)) (protocol.AIChatResult, error) {
			token := fmt.Sprintf("%s-%d", run, srv.chats.Add(1))
			srv.deltas.Store(token, onText)
			defer srv.deltas.Delete(token)
			p.Instance, p.Run = in.connection, token
			var res protocol.AIChatResult
			err := in.conn.Call(ctx, protocol.MethodAIChat, p, &res)
			return res, err
		},
		library: func(name, arguments string) (string, error) {
			var res protocol.MCPCallResult
			err := in.conn.Call(ctx, protocol.MethodLibraryCall,
				protocol.LibraryCallParams{Run: run, Name: name, Arguments: json.RawMessage(arguments)}, &res)
			var rpcErr *protocol.Error
			if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeForbidden {
				return "", errNoAccess
			}
			if err != nil {
				return "", err
			}
			var text []string
			for _, c := range res.Content {
				text = append(text, c.Text)
			}
			out := strings.Join(text, "\n")
			if res.IsError {
				return "", errToolFailed(out)
			}
			return out, nil
		},
	}
}
