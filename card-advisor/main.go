// Command card-advisor is a ui plugin. Its panel is a chat that recommends
// cards from the library, using an ai instance's ai.chat with library tools.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/inkandale/innkeeper/protocol"
)

// instance is the setup initialize configured. err is a settings problem,
// reported by the first turn. conn is the core.
type instance struct {
	connection int64
	err        error
	store      *store
	conn       *protocol.Conn
}

type server struct {
	// deltas maps an ai.chat's streaming token to its text callback.
	deltas sync.Map
}

func main() {
	protocol.Serve((&server{}).plugin())
}

func (srv *server) plugin() protocol.Plugin[instance] {
	return protocol.Plugin[instance]{
		Init: func(ctx context.Context, p protocol.InitializeParams) (*instance, error) {
			in := &instance{conn: protocol.Peer(ctx)}
			if p.DatabaseURL != "" {
				var err error
				if in.store, err = openStore(ctx, p.DatabaseURL, p.InstanceID); err != nil {
					return nil, err
				}
			}
			connection, ok := p.Config["connection"].(float64)
			if !ok {
				in.err = errors.New("pick an AI connection in this plugin's settings")
			}
			in.connection = int64(connection)
			return in, nil
		},
		Methods: map[string]func(*instance, context.Context, json.RawMessage) (any, error){
			protocol.NotifyAIDelta: func(_ *instance, _ context.Context, params json.RawMessage) (any, error) {
				var d protocol.AIDelta
				if json.Unmarshal(params, &d) == nil {
					if onText, ok := srv.deltas.Load(d.Run); ok {
						onText.(func(string))(d.Text)
					}
				}
				return nil, nil
			},
			protocol.MethodUICall: func(in *instance, ctx context.Context, params json.RawMessage) (any, error) {
				var p protocol.UICallParams
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, protocol.InvalidParams(err)
				}
				return srv.dispatch(ctx, in, p)
			},
			protocol.MethodShutdown: func(in *instance, _ context.Context, _ json.RawMessage) (any, error) {
				if in.store != nil {
					in.store.close()
				}
				return nil, nil
			},
		},
	}
}

func (srv *server) dispatch(ctx context.Context, in *instance, p protocol.UICallParams) (any, error) {
	switch p.Method {
	case "send", "list_conversations", "get_conversation", "delete_conversation":
	default:
		return nil, protocol.MethodNotFound(p.Method)
	}
	if in.err != nil {
		return nil, protocol.Fail(in.err.Error(), false)
	}
	if in.store == nil {
		return nil, protocol.Fail("card-advisor needs its database", false)
	}
	switch p.Method {
	case "send":
		return srv.send(ctx, in, p)
	case "list_conversations":
		return in.store.conversations(ctx)
	}
	var target struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(p.Params, &target); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if p.Method == "delete_conversation" {
		return nil, wrapStore(in.store.deleteConversation(ctx, target.ID))
	}
	messages, err := in.store.messages(ctx, target.ID)
	return messages, wrapStore(err)
}

// wrapStore turns a missing conversation into a message the panel can show.
func wrapStore(err error) error {
	if errors.Is(err, errNoConversation) {
		return protocol.Fail("that conversation is gone", false)
	}
	if err != nil {
		return fmt.Errorf("card-advisor store: %w", err)
	}
	return nil
}
