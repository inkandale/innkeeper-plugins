package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// connection is an instance's Illarin credentials. Illarin rotates the
// refresh token on every refresh, so it lives here rather than in secrets.
type connection struct {
	base          string
	access        string
	accessExpires time.Time
	refresh       string
}

// store keeps each instance's connection. Instances share the plugin's schema.
type store struct {
	pool     *pgxpool.Pool
	instance string
}

func openStore(ctx context.Context, dsn, instance string) (*store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// The API and the worker start instances together; the lock stops them racing to create the table.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('illarin:' || current_schema()))`)
		if err == nil {
			_, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS connection (
	instance text PRIMARY KEY,
	base_url text NOT NULL,
	access_token text NOT NULL,
	access_expires timestamptz NOT NULL,
	refresh_token text NOT NULL)`)
		}
		return err
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create connection table: %w", err)
	}
	return &store{pool: pool, instance: instance}, nil
}

func (s *store) close() { s.pool.Close() }

// lock reads the instance's connection and holds its row until tx ends, so
// one process at a time spends the refresh token. ok is false without one.
func (s *store) lock(ctx context.Context, tx pgx.Tx) (c connection, ok bool, err error) {
	err = tx.QueryRow(ctx, `SELECT base_url, access_token, access_expires, refresh_token FROM connection WHERE instance = $1 FOR UPDATE`,
		s.instance).Scan(&c.base, &c.access, &c.accessExpires, &c.refresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// execer is a pool or a transaction.
type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *store) save(ctx context.Context, q execer, c connection) error {
	_, err := q.Exec(ctx, `INSERT INTO connection (instance, base_url, access_token, access_expires, refresh_token)
	VALUES ($1, $2, $3, $4, $5) ON CONFLICT (instance) DO UPDATE SET base_url = $2, access_token = $3, access_expires = $4, refresh_token = $5`,
		s.instance, c.base, c.access, c.accessExpires, c.refresh)
	return err
}

func (s *store) forget(ctx context.Context, q execer) error {
	_, err := q.Exec(ctx, `DELETE FROM connection WHERE instance = $1`, s.instance)
	return err
}
