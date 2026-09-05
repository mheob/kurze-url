package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// deployedExecMode must match cmd/api/main.go's
// poolCfg.ConnConfig.DefaultQueryExecMode. It cannot be imported directly —
// main.go lives in package main — so it is duplicated here deliberately; if
// you change one, change the other, or these tests silently stop proving
// anything about production.
//
// Under Supavisor's transaction pooler, pgx cannot use QueryExecModeCacheStatement
// (the default): it prepares and caches a *named* server-side statement per
// connection, and the pooler may hand that connection a server backend that
// already has — or never had — a statement by that name, producing SQLSTATE
// 42P05 ("prepared statement already exists"). QueryExecModeCacheDescribe
// avoids that: every Prepare/Parse it issues uses an empty ("") statement
// name, i.e. the anonymous statement, and every single execution (cache hit
// or miss) sends its own self-contained Parse+Bind+Execute in one round
// trip — no round trip ever depends on server-side state a *different*
// round trip created, which is the exact failure mode a pooler introduces.
// See cmd/api/main.go for the full reasoning, including why
// QueryExecModeDescribeExec (also unnamed, but split across two round
// trips) is not safe here either.
const deployedExecMode = pgx.QueryExecModeCacheDescribe

// testPoolWithExecMode connects to the local Supabase Postgres with a chosen
// pgx.QueryExecMode, rather than pgx's default. Every other test in this
// package uses testPool (schema_test.go), whose default exec mode
// (QueryExecModeCacheStatement) lets pgx ask Postgres to describe each
// parameter's server-side type before sending it — exactly the situation
// that differs from the deployed pool. A query that only works because pgx
// knows a parameter's type from that description would pass under testPool
// and still break in production; these tests run under deployedExecMode
// specifically so at least one test exercises the same exec mode the
// deployment actually uses, and can also be pointed at
// pgx.QueryExecModeExec (see the class's falsification, described in the
// exec-mode fix report) to reproduce the bugs this mode fixes.
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
// mutating endpoint makes (internal/audit.Log) through a pool configured with
// deployedExecMode: a []byte argument bound to the jsonb metadata column.
//
// Under QueryExecModeExec (the mode this replaced) pgx has no server-described
// parameter type, so it picks a wire encoding from the Go argument's type
// alone: []byte is always encoded as Postgres bytea. Postgres, with the
// parameter itself typed jsonb (inferred from the insert context, since it is
// uncast), then fed that bytea's hex text representation to jsonb's input
// function and rejected it with SQLSTATE 22P02, "invalid input syntax for
// type json" — exactly the error the deployed API logged for
// POST /v1/teams/{team_id}/links. Under deployedExecMode pgx does learn the
// real jsonb OID (via an anonymous Describe, cached client-side), so the same
// []byte argument encodes correctly.
func TestInsertAuditLogSurvivesTheDeploymentsExecMode(t *testing.T) {
	ctx := context.Background()
	pool := testPoolWithExecMode(t, deployedExecMode)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	metadata := []byte(`{"name":"Alte Verein"}`)
	err = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: &userID,
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    &teamID,
		Metadata:    metadata,
	})
	require.NoError(t, err, "insert must succeed under the exec mode the deployed API actually uses")

	var stored string
	require.NoError(t, tx.QueryRow(ctx,
		`select metadata::text from audit_log where team_id = $1 and action = 'team.created'`,
		teamID).Scan(&stored))
	require.JSONEq(t, `{"name":"Alte Verein"}`, stored)
}

// TestTagArrayParamsSurviveTheDeploymentsExecMode covers the second class of
// production failure, all three uuid[] parameters in tag.sql:
// InsertLinkTags.TagIds, ListTagsByIDs.Ids, and ListTagsForLinks.LinkIds (the
// exact query behind the reported GET /v1/teams/{team_id}/links 500).
//
// Under QueryExecModeExec a []uuid.UUID argument has no default wire
// encoding at all — a scalar uuid.UUID is handled by pgx's generic
// [16]byte-array fallback, but a *slice* of them is not — so pgx failed with
// "unable to encode []uuid.UUID{...} into text format for unknown type
// (OID 0): cannot find encode plan". The explicit ::uuid[] cast in each
// query's SQL text does not help: that only tells Postgres how to interpret
// the parameter once pgx has already sent bytes, and pgx never gets far
// enough to send anything. Under deployedExecMode pgx learns the real uuid[]
// array OID for each of these queries (again via an anonymous, client-cached
// Describe), so the same []uuid.UUID argument encodes correctly.
func TestTagArrayParamsSurviveTheDeploymentsExecMode(t *testing.T) {
	ctx := context.Background()
	pool := testPoolWithExecMode(t, deployedExecMode)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	var domainID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		teamID, "t"+uuid.NewString()[:8]+".test").Scan(&domainID))

	link, err := q.CreateLink(ctx, db.CreateLinkParams{
		DomainID:         domainID,
		TeamID:           teamID,
		Slug:             "tagged",
		DestinationURL:   "https://example.org/tagged",
		RedirectType:     302,
		AnalyticsEnabled: true,
		CreatedBy:        userID,
	})
	require.NoError(t, err)

	tagA, err := q.CreateTag(ctx, db.CreateTagParams{TeamID: teamID, Name: "a"})
	require.NoError(t, err)
	tagB, err := q.CreateTag(ctx, db.CreateTagParams{TeamID: teamID, Name: "b"})
	require.NoError(t, err)

	tagIDs := []uuid.UUID{tagA.ID, tagB.ID}

	require.NoError(t, q.InsertLinkTags(ctx, db.InsertLinkTagsParams{
		LinkID: link.ID, TagIds: tagIDs,
	}), "InsertLinkTags must succeed under the exec mode the deployed API actually uses")

	byIDs, err := q.ListTagsByIDs(ctx, db.ListTagsByIDsParams{TeamID: teamID, Ids: tagIDs})
	require.NoError(t, err, "ListTagsByIDs must succeed under the exec mode the deployed API actually uses")
	require.Len(t, byIDs, 2)

	forLinks, err := q.ListTagsForLinks(ctx, db.ListTagsForLinksParams{
		TeamID: teamID, LinkIds: []uuid.UUID{link.ID},
	})
	require.NoError(t, err, "ListTagsForLinks must succeed under the exec mode the deployed API actually uses")
	require.Len(t, forLinks, 2)
}
