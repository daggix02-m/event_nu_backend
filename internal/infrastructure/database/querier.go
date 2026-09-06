package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the minimal DB surface repositories need. Passing a request
// transaction (instead of the pool) lets RLS context apply to every query.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

// ContextWithTx stores a request transaction in the context.
func ContextWithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext returns the request transaction, if any.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// QuerierFromContext returns the request transaction when present (so RLS
// context is honored), falling back to the pool.
func QuerierFromContext(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return pool
}

// Tx returns the request transaction when one is active; otherwise it begins a
// new one. The second return value reports whether the caller owns the
// transaction (and must commit/rollback it).
func Tx(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, bool, error) {
	if tx, ok := TxFromContext(ctx); ok {
		return tx, false, nil
	}
	tx, err := pool.Begin(ctx)
	return tx, true, err
}

// NewPoolWithRole opens a pool whose connections pre-set a fixed RLS role
// (non-transaction-local). Used by the worker, whose operations are trusted
// internal 'service' work that must survive RLS without per-statement context.
func NewPoolWithRole(ctx context.Context, dsn, role string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SELECT set_config('app.role', $1, false)`, role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
