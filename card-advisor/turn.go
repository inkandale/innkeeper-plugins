package main

import (
	"context"
	"errors"

	"github.com/inkandale/innkeeper/protocol"
)

func (srv *server) send(context.Context, *instance, protocol.UICallParams) (any, error) {
	return nil, errors.New("replaced in the next task")
}
