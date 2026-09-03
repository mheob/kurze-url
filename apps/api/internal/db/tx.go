// This file is hand-written. sqlc rewrites only its own outputs — db.go,
// models.go, batch.go and *.sql.go — so a hand-written file is safe in this
// package. Do not add queries here; they belong in queries/*.sql.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn inside one transaction. Every mutating handler uses it so the
// mutation and its audit_log insert commit together: an audited action either
// happened and is recorded, or neither.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this is safe
	// unconditionally and covers every early return inside fn.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit transaction: %w", err)
	}
	return nil
}
