package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, url string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 15
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	var pool *pgxpool.Pool
	var last error
	for i := 0; i < 8; i++ {
		pool, last = pgxpool.NewWithConfig(ctx, cfg)
		if last == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			last = pool.Ping(pingCtx)
			cancel()
			if last == nil {
				break
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return nil, last
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS links (
  code TEXT PRIMARY KEY CHECK (char_length(code) BETWEEN 4 AND 40),
  payload TEXT NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 100000),
  once BOOLEAN NOT NULL DEFAULT FALSE,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`)
	return err
}

func (p *Postgres) Put(ctx context.Context, code string, rec Record) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO links (code, payload, once, expires_at)
VALUES ($1, $2, $3, $4)`, code, rec.Payload, rec.Once, rec.ExpiresAt)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrExists
	}
	return err
}

func (p *Postgres) Take(ctx context.Context, code string) (string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var payload string
	var once bool
	var exp *time.Time
	err = tx.QueryRow(ctx, `
SELECT payload, once, expires_at
FROM links
WHERE code = $1
FOR UPDATE`, code).Scan(&payload, &once, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if exp != nil && !exp.After(time.Now()) {
		if _, err = tx.Exec(ctx, `DELETE FROM links WHERE code = $1`, code); err != nil {
			return "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrNotFound
	}
	if once {
		if _, err = tx.Exec(ctx, `DELETE FROM links WHERE code = $1`, code); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return payload, nil
}

func (p *Postgres) Sweep(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM links WHERE expires_at IS NOT NULL AND expires_at <= now()`)
	return err
}
