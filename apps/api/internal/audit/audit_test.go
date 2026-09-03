package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	pool, err := pgxpool.New(context.Background(), url)
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

// seedTeam creates a throwaway team owned by an existing auth user.
func seedTeam(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (teamID, userID uuid.UUID) {
	t.Helper()

	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('audit fixture') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	return teamID, userID
}

func TestLogWritesTheEntry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
			Metadata:    map[string]any{"from": "Alte Verein", "to": "Neue Verein"},
		})
	}))

	var action, entityType string
	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`select action, entity_type, metadata from audit_log where team_id = $1`,
		teamID).Scan(&action, &entityType, &raw))

	require.Equal(t, "team.renamed", action)
	require.Equal(t, "team", entityType)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(raw, &metadata))
	require.Equal(t, "Neue Verein", metadata["to"])
}

func TestLogWritesAnEmptyObjectForNilMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamCreated,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		})
	}))

	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`select metadata from audit_log where team_id = $1`, teamID).Scan(&raw))
	require.JSONEq(t, `{}`, string(raw), "metadata must be an object, never SQL null")
}

func TestLogRefusesPasswordishMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	for _, key := range []string{"password", "Password", "password_hash", "ip", "ip_address"} {
		err := db.InTx(ctx, pool, func(q *db.Queries) error {
			return audit.Log(ctx, q, audit.Entry{
				TeamID:      teamID,
				ActorUserID: userID,
				Action:      audit.ActionTeamRenamed,
				EntityType:  audit.EntityTeam,
				EntityID:    teamID,
				Metadata:    map[string]any{key: "secret"},
			})
		})

		require.ErrorIs(t, err, audit.ErrForbiddenMetadata, "key %q", key)
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from audit_log where team_id = $1`, teamID).Scan(&count))
	require.Zero(t, count, "a refused entry must leave no row behind")
}

func TestLogRefusesAnActionOutsideTheTaxonomy(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.Action("team.frobnicated"),
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		})
	})

	require.ErrorIs(t, err, audit.ErrUnknownAction,
		"the taxonomy is the contract; a typo must fail loudly rather than land in the log")
}

func TestLogRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)
	boom := errors.New("mutation failed after the audit insert")

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		if err := audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		}); err != nil {
			return err
		}
		return boom
	})

	require.ErrorIs(t, err, boom)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from audit_log where team_id = $1`, teamID).Scan(&count))
	require.Zero(t, count,
		"an audited action either happened and is recorded, or neither")
}
