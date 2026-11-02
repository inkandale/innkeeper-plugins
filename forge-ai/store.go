package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errSkillNotFound = errors.New("no such skill")
	errSkillExists   = errors.New("a skill with that name exists")
)

// store keeps one instance's skills and AI Assistant conversations. The
// plugin's instances share its schema, so every row belongs to an instance.
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
		for _, stmt := range append([]string{
			`SELECT pg_advisory_xact_lock(hashtext('forge-ai:' || current_schema()))`,
			`CREATE TABLE IF NOT EXISTS skill (
	instance text NOT NULL,
	name text NOT NULL,
	description text NOT NULL,
	body text NOT NULL,
	files jsonb NOT NULL DEFAULT '{}',
	enabled boolean NOT NULL DEFAULT true,
	PRIMARY KEY (instance, name))`,
		}, assistantTables...) {
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

// summaries lists skills without bodies or files, by name; with
// enabledOnly, only the enabled ones.
func (s *store) summaries(ctx context.Context, enabledOnly bool) ([]skill, error) {
	rows, _ := s.pool.Query(ctx, `SELECT name, description, enabled FROM skill
		WHERE instance = $1 AND (enabled OR NOT $2) ORDER BY name`, s.instance, enabledOnly)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (skill, error) {
		var sk skill
		return sk, row.Scan(&sk.Name, &sk.Description, &sk.Enabled)
	})
}

func (s *store) get(ctx context.Context, name string) (skill, error) {
	var sk skill
	err := s.pool.QueryRow(ctx, `SELECT name, description, body, files, enabled FROM skill WHERE instance = $1 AND name = $2`,
		s.instance, name).Scan(&sk.Name, &sk.Description, &sk.Body, &sk.Files, &sk.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return sk, errSkillNotFound
	}
	return sk, err
}

// create adds sk, refusing to overwrite a skill of the same name.
func (s *store) create(ctx context.Context, sk skill) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO skill (instance, name, description, body, files, enabled) VALUES ($1, $2, $3, $4, $5, $6)`,
		s.instance, sk.Name, sk.Description, sk.Body, files(sk), sk.Enabled)
	return uniqueAsExists(err)
}

// replace overwrites the skill named name with sk, which may rename it.
func (s *store) replace(ctx context.Context, name string, sk skill) error {
	tag, err := s.pool.Exec(ctx, `UPDATE skill SET name = $3, description = $4, body = $5, files = $6, enabled = $7
		WHERE instance = $1 AND name = $2`, s.instance, name, sk.Name, sk.Description, sk.Body, files(sk), sk.Enabled)
	if err == nil && tag.RowsAffected() == 0 {
		return errSkillNotFound
	}
	return uniqueAsExists(err)
}

// upsert adds sk or replaces the skill of the same name, for imports. A
// replaced skill keeps its enablement.
func (s *store) upsert(ctx context.Context, sk skill) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO skill (instance, name, description, body, files, enabled) VALUES ($1, $2, $3, $4, $5, true)
		ON CONFLICT (instance, name) DO UPDATE SET description = $3, body = $4, files = $5`,
		s.instance, sk.Name, sk.Description, sk.Body, files(sk))
	return err
}

func (s *store) delete(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM skill WHERE instance = $1 AND name = $2`, s.instance, name)
	if err == nil && tag.RowsAffected() == 0 {
		return errSkillNotFound
	}
	return err
}

// files is sk's reference files for the NOT NULL column.
func files(sk skill) map[string]string {
	if sk.Files == nil {
		return map[string]string{}
	}
	return sk.Files
}

func uniqueAsExists(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errSkillExists
	}
	return err
}
