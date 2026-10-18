// Command chub-archive is an import plugin for the chub.ai archive's SQL
// dump.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
)

const version = "0.3.1"

// env is what initialize set up.
type env struct {
	images  []string // config.images
	blobTmp string
}

func main() {
	// Set by initialize; requests run on other goroutines.
	var cur atomic.Pointer[env]
	var conn *protocol.Conn
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			images, err := configImages(p.Config)
			if err != nil {
				return nil, invalid(err)
			}
			if p.DatabaseURL != "" {
				if err := dropOldTables(ctx, p.DatabaseURL); err != nil {
					return nil, err
				}
			}
			cur.Store(&env{images: images, blobTmp: p.BlobTmp})
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "chub-archive", Version: version,
				Capabilities: []string{"import"}}, nil
		case protocol.MethodShutdown:
			return nil, nil
		case protocol.MethodImportRun:
			e := cur.Load()
			if e == nil {
				return nil, errors.New("import.run before initialize")
			}
			return run(ctx, e, conn.Notify, params)
		}
		return nil, protocol.MethodNotFound(method)
	}
	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// dropOldTables drops the catalog index that chub-archive 0.2 kept. Dropping
// its migration record too lets a rollback to 0.2 recreate the tables empty.
// source_row marks a 0.2 schema, so a schema shared with another catalogkit
// plugin, as in make check, keeps its tables. The lock serializes the API's
// and the worker's instances, which start together.
func dropOldTables(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('catalogkit:' || current_schema()))`); err != nil {
			return err
		}
		var old bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('source_row') IS NOT NULL`).Scan(&old); err != nil || !old {
			return err
		}
		_, err := tx.Exec(ctx, `DROP TABLE source_row, parent, item_version, item, migration;
DROP FUNCTION IF EXISTS tags_text(text[]);`)
		if err != nil {
			return fmt.Errorf("drop chub-archive 0.2 tables: %w", err)
		}
		return nil
	})
}

// configImages reads config.images, a list of folders. Empty entries are skipped.
func configImages(cfg map[string]any) ([]string, error) {
	v, ok := cfg["images"]
	if !ok || v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("config images must be a list of folder paths")
	}
	var dirs []string
	for _, dir := range list {
		s, ok := dir.(string)
		if !ok {
			return nil, errors.New("config images must be a list of folder paths")
		}
		if s != "" {
			dirs = append(dirs, s)
		}
	}
	return dirs, nil
}

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}
