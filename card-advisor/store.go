package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoConversation = errors.New("no such conversation")

// pick is one recommended card.
type pick struct {
	CardID int64  `json:"card_id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Picks   []pick `json:"picks,omitempty"`
}

// store keeps one instance's conversations. Instances share the plugin's
// schema, so every conversation names its instance.
type store struct {
	pool     *pgxpool.Pool
	instance string
}

func openStore(ctx context.Context, dsn, instance string) (*store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// The lock keeps the API and the worker, which start instances together,
	// from racing to create the tables.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, stmt := range []string{
			`SELECT pg_advisory_xact_lock(hashtext('card-advisor:' || current_schema()))`,
			`CREATE TABLE IF NOT EXISTS conversation (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	instance text NOT NULL,
	title text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now())`,
			`CREATE TABLE IF NOT EXISTS message (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	conversation_id bigint NOT NULL REFERENCES conversation ON DELETE CASCADE,
	role text NOT NULL CHECK (role IN ('user', 'assistant')),
	content text NOT NULL,
	picks jsonb,
	created_at timestamptz NOT NULL DEFAULT now())`,
			`CREATE INDEX IF NOT EXISTS message_conversation ON message (conversation_id)`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return &store{pool: pool, instance: instance}, nil
}

func (s *store) close() { s.pool.Close() }

func (s *store) createConversation(ctx context.Context, title string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO conversation (instance, title) VALUES ($1, $2) RETURNING id`, s.instance, title).Scan(&id)
	return id, err
}

func (s *store) owns(ctx context.Context, id int64) error {
	var found bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM conversation WHERE id = $1 AND instance = $2)`, id, s.instance).Scan(&found)
	if err == nil && !found {
		return errNoConversation
	}
	return err
}

func (s *store) addMessage(ctx context.Context, id int64, role, content string, picks []pick) error {
	if err := s.owns(ctx, id); err != nil {
		return err
	}
	var raw []byte
	if picks != nil {
		var err error
		if raw, err = json.Marshal(picks); err != nil {
			return err
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO message (conversation_id, role, content, picks) VALUES ($1, $2, $3, $4)`, id, role, content, raw); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversation SET updated_at = now() WHERE id = $1`, id)
		return err
	})
}

func (s *store) messages(ctx context.Context, id int64) ([]message, error) {
	if err := s.owns(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT role, content, picks FROM message WHERE conversation_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (message, error) {
		var m message
		var raw []byte
		if err := r.Scan(&m.Role, &m.Content, &raw); err != nil {
			return m, err
		}
		if raw != nil {
			if err := json.Unmarshal(raw, &m.Picks); err != nil {
				return m, err
			}
		}
		return m, nil
	})
}

func (s *store) conversations(ctx context.Context) ([]conversation, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, title, updated_at FROM conversation WHERE instance = $1 ORDER BY updated_at DESC`, s.instance)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[conversation])
}

func (s *store) deleteConversation(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM conversation WHERE id = $1 AND instance = $2`, id, s.instance)
	if err == nil && tag.RowsAffected() == 0 {
		return errNoConversation
	}
	return err
}
