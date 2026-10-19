// Command chub-archive is an import plugin for the chub.ai archive's SQL
// dump.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
)

// env is what initialize set up.
type env struct {
	images  []string // config.images
	blobTmp string
}

func main() {
	protocol.Serve(protocol.Plugin[env]{
		Init: func(ctx context.Context, p protocol.InitializeParams) (*env, error) {
			images, err := configImages(p.Config)
			if err != nil {
				return nil, protocol.InvalidParams(err)
			}
			if p.DatabaseURL != "" {
				if err := dropOldTables(ctx, p.DatabaseURL); err != nil {
					return nil, err
				}
			}
			return &env{images: images, blobTmp: p.BlobTmp}, nil
		},
		Methods: map[string]func(*env, context.Context, json.RawMessage) (any, error){
			protocol.MethodImportRun: func(e *env, ctx context.Context, params json.RawMessage) (any, error) {
				return run(ctx, e, protocol.Peer(ctx).Notify, params)
			},
		},
	})
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
