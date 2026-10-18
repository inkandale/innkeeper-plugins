// Command forge-ai is an action plugin. It writes, revises, tightens,
// evaluates, and scores character cards, and adds lorebook entries, with a
// language model, through an ai instance's ai.chat.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.7.0"

// instance is the setup initialize configured. err is a settings problem,
// reported by the first run. store is nil without a database_url.
type instance struct {
	settings settings
	err      error
	store    *store
}

type server struct {
	conn *protocol.Conn
	// Set by initialize; requests run on other goroutines.
	cur atomic.Pointer[instance]
	// deltas maps an ai.chat's streaming token to its text callback.
	deltas sync.Map
	chats  atomic.Int64
}

func main() {
	srv := &server{}
	srv.conn = protocol.NewConn(os.Stdin, os.Stdout, srv.handle)
	if err := srv.conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (srv *server) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodInitialize:
		var p protocol.InitializeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, invalid("%v", err)
		}
		in := &instance{}
		if p.DatabaseURL != "" {
			var err error
			if in.store, err = openStore(ctx, p.DatabaseURL, p.InstanceID); err != nil {
				return nil, err
			}
		}
		in.settings, in.err = parseSettings(p.Config)
		srv.cur.Store(in)
		return protocol.InitializeResult{Protocol: protocol.Version, Name: "forge-ai", Version: version,
			Capabilities: []string{"action", "records", "enrich", "settings"}}, nil
	case protocol.NotifyAIDelta:
		var d protocol.AIDelta
		if json.Unmarshal(params, &d) == nil {
			if onText, ok := srv.deltas.Load(d.Run); ok {
				onText.(func(string))(d.Text)
			}
		}
		return nil, nil
	case protocol.MethodSettingsDefaults:
		return settingsDefaults()
	case protocol.MethodShutdown:
		if in := srv.cur.Load(); in != nil && in.store != nil {
			in.store.close()
		}
		return nil, nil
	case protocol.MethodActionList, protocol.MethodActionRun, protocol.MethodRecordsList, protocol.MethodRecordsGet,
		protocol.MethodRecordsPut, protocol.MethodRecordsDelete, protocol.MethodRecordsImport, protocol.MethodEnrichRun:
	default:
		return nil, protocol.MethodNotFound(method)
	}
	in := srv.cur.Load()
	if in == nil {
		return nil, fmt.Errorf("%s before initialize", method)
	}
	switch method {
	case protocol.MethodActionList:
		return actionList(ctx, in)
	case protocol.MethodActionRun:
		return srv.run(ctx, in, params)
	case protocol.MethodEnrichRun:
		return srv.enrich(ctx, in, params)
	}
	return srv.records(ctx, in, method, params)
}

func actionList(ctx context.Context, in *instance) (protocol.ActionListResult, error) {
	list := protocol.ActionListResult{}
	for _, a := range actions {
		list.Actions = append(list.Actions, a.Action)
	}
	enabled, err := in.enabledSkills(ctx)
	if err != nil {
		// The actions work without skills; a run that names one reports the error.
		fmt.Fprintf(os.Stderr, "list skills: %v\n", err)
	}
	for _, sk := range enabled {
		list.Skills = append(list.Skills, protocol.Skill{Name: sk.Name, Description: sk.Description})
	}
	return list, nil
}

func (srv *server) run(ctx context.Context, in *instance, params json.RawMessage) (*protocol.ActionRunResult, error) {
	var p protocol.ActionRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid("%v", err)
	}
	r, err := parseRun(p)
	if err != nil {
		return nil, err
	}
	if in.err != nil {
		return nil, in.err
	}
	if r.WholeCard && r.Field == "" {
		return srv.wholeCard(ctx, in, r)
	}
	return srv.perform(ctx, in, r, true, func() func(string) { return srv.progress(r) })
}

// perform runs a checked action to its result. offerTools gives the model
// the enabled skills and, for a library action, the core's library.
// newProgress gives each request to the model its own progress callback.
func (srv *server) perform(ctx context.Context, in *instance, r *run, offerTools bool, newProgress func() func(string)) (*protocol.ActionRunResult, error) {
	system, user, err := r.prompts(in.settings)
	if err != nil {
		return nil, err
	}
	var box toolbox
	if offerTools {
		enabled, err := in.enabledSkills(ctx)
		if err != nil {
			if len(r.Context.Skills) > 0 {
				return nil, fmt.Errorf("list skills: %w", err)
			}
			// Only skills fail with the database; the run goes on without them.
			fmt.Fprintf(os.Stderr, "list skills: %v\n", err)
		}
		forced, err := in.forcedSkills(ctx, enabled, r.Context.Skills)
		if err != nil {
			return nil, err
		}
		system += skillsPrompt(forced)
		var lib toolFunc
		if r.Library {
			lib = coreLibrary(srv.conn, r.Run)
		}
		box = in.tools(enabled, lib)
	}
	message := protocol.ChatMessage{Role: "user", Content: user}
	if r.Images.Reference != "" || r.Images.Avatar != "" {
		message = protocol.ChatMessage{Role: "user", Parts: []protocol.ContentPart{{Type: "text", Text: user}}}
		for _, path := range []string{r.Images.Reference, r.Images.Avatar} {
			if path == "" {
				continue
			}
			part, err := imagePart(path)
			if err != nil {
				return nil, err
			}
			message.Parts = append(message.Parts, part)
		}
	}
	temperature, maxTokens := r.limits(in.settings)
	req := protocol.AIChatParams{Temperature: &temperature, MaxTokens: &maxTokens,
		Messages: []protocol.ChatMessage{{Role: "system", Content: system}, message}}
	reply, err := srv.complete(ctx, in, r.Run, req, box, newProgress)
	if err != nil {
		return nil, err
	}
	r.model = reply.Model
	res, err := r.result(reply.Text)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// labels name card keys in progress messages.
var labels = map[string]string{"name": "Name", "description": "Description", "personality": "Personality",
	"scenario": "Scenario", "first_mes": "First message", "mes_example": "Example dialogue", "tags": "Tags",
	"system_prompt": "System prompt", "post_history_instructions": "Post-history instructions",
	"creator_notes": "Creator notes", "summary": "Summary", "overall_score": "Score", "card_type": "Card type",
	"dimensions": "Dimensions", "contradictions": "Contradictions", "misplaced_content": "Misplaced content",
	"suggestions": "Suggestions"}

// label names a card key in progress messages.
func label(key string) string {
	if l := labels[key]; l != "" {
		return l
	}
	return key
}

// progress returns a callback for one request that sends one progress
// message per key of the run's as the model starts writing it.
func (srv *server) progress(r *run) func(string) {
	if r.appends != nil {
		return srv.itemProgress(r)
	}
	keys := r.writes()
	var announced []string
	return func(output string) {
		// ponytail: rescans the whole output per chunk, O(n²) in output
		// length; fine for a card's few thousand tokens.
		for _, key := range startedKeys(output) {
			if !slices.Contains(keys, key) || slices.Contains(announced, key) {
				continue
			}
			_ = srv.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(len(announced)),
				Total: int64(len(keys)), Message: fmt.Sprintf("Writing %s (%d of %d)", label(key), len(announced)+1, len(keys))})
			announced = append(announced, key)
		}
	}
}

// itemProgress returns a callback for one request that sends one progress
// message per item, up to the run's count, as the model starts writing it.
func (srv *server) itemProgress(r *run) func(string) {
	announced := 0
	return func(output string) {
		for n := min(startedItems(output, r.appends.key), r.count); announced < n; announced++ {
			_ = srv.conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: r.Run, Done: int64(announced),
				Total: int64(r.count), Message: fmt.Sprintf("Writing %s %d of %d", r.appends.item, announced+1, r.count)})
		}
	}
}
