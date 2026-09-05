package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// testPoolWithExecMode connects to the local Supabase Postgres with a chosen
// pgx.QueryExecMode, rather than pgx's default. Every other test in this
// package uses testPool (schema_test.go), whose default exec mode lets pgx
// ask Postgres to describe each parameter's server-side type before sending
// it — exactly the information a connection through Supavisor's transaction
// pooler does not get, per the comment on DefaultQueryExecMode in
// cmd/api/main.go. A query that only works because pgx knows a parameter's
// type from that description would pass under testPool and still break in
// production; this helper exists so at least one test runs under the same
// exec mode the deployment actually uses.
func testPoolWithExecMode(t *testing.T, mode pgx.QueryExecMode) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	cfg, err := pgxpool.ParseConfig(url)
	require.NoError(t, err)
	cfg.ConnConfig.DefaultQueryExecMode = mode

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

// TestInsertAuditLogSurvivesTheDeploymentsExecMode runs the same insert every
// mutating endpoint makes (internal/audit.Log) through a pool configured the
// way cmd/api/main.go configures the production one: pgx.QueryExecModeExec,
// set there so Supavisor's transaction pooler works.
//
// Under that mode pgx never asks Postgres to describe the statement's
// parameter types, so it has nothing but the Go argument's own type to decide
// how to encode it. Passing a []byte for the metadata argument (jsonb column)
// used to get encoded as Postgres's bytea text format (a "\x"-prefixed hex
// string); Postgres left the parameter itself typed jsonb (inferred from the
// insert context, since it is uncast), fed that hex string to jsonb's input
// function, and rejected it with SQLSTATE 22P02, "invalid input syntax for
// type json" — exactly the error the deployed API logged. A plain string
// encodes as plain text instead, which jsonb's input function parses
// correctly. This test reproduces the failure because, unlike every other
// test in this package (which use testPool and so get a server-described
// parameter type), it runs under the exec mode that actually hides that
// information.
func TestInsertAuditLogSurvivesTheDeploymentsExecMode(t *testing.T) {
	ctx := context.Background()
	pool := testPoolWithExecMode(t, pgx.QueryExecModeExec)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	metadata := `{"name":"Alte Verein"}`
	err = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: &userID,
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    &teamID,
		Metadata:    &metadata,
	})
	require.NoError(t, err, "insert must succeed under the exec mode the deployed API actually uses")

	var stored string
	require.NoError(t, tx.QueryRow(ctx,
		`select metadata::text from audit_log where team_id = $1 and action = 'team.created'`,
		teamID).Scan(&stored))
	require.JSONEq(t, `{"name":"Alte Verein"}`, stored)
}
