package db_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

var errTest = errors.New("test failure")

// seedTeamWithOwner inserts a team plus one owner, inside tx, and returns both
// IDs. It borrows an existing auth.users row: the local Supabase stack seeds
// one, and this suite must not invent auth users of its own.
func seedTeamWithOwner(ctx context.Context, t *testing.T, tx pgx.Tx) (teamID, userID uuid.UUID) {
	t.Helper()

	require.NoError(t, tx.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('tenancy fixture') returning id`).Scan(&teamID))
	_, err := tx.Exec(ctx,
		`insert into team_member (team_id, user_id, role) values ($1, $2, 'owner')`, teamID, userID)
	require.NoError(t, err)

	return teamID, userID
}

func TestGetTeamMembershipReturnsTheCallersRole(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	member, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{TeamID: teamID, UserID: userID})

	require.NoError(t, err)
	require.Equal(t, "owner", member.Role)
}

func TestGetTeamMembershipReturnsNoRowsForANonMember(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	_, err = q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: teamID,
		UserID: uuid.New(),
	})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"the caller of this query turns ErrNoRows into 404; it must not return an empty row")
}

func TestListTeamMembersJoinsTheEmailAndCarriesTheTotal(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	rows, err := q.ListTeamMembers(ctx, db.ListTeamMembersParams{
		TeamID: teamID,
		Limit:  25,
		Offset: 0,
	})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, userID, rows[0].UserID)
	require.EqualValues(t, 1, rows[0].TotalCount)
}

func TestListTeamsForUserOnlyReturnsTeamsTheUserBelongsTo(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('someone else') returning id`).Scan(&otherTeamID))

	q := db.New(tx)
	rows, err := q.ListTeamsForUser(ctx, db.ListTeamsForUserParams{
		UserID: userID,
		Limit:  25,
		Offset: 0,
	})

	require.NoError(t, err)
	for _, row := range rows {
		require.NotEqual(t, otherTeamID, row.ID,
			"a team the caller has no membership in must never appear")
	}
	require.Contains(t, teamIDs(rows), teamID)
}

func teamIDs(rows []db.ListTeamsForUserRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestGetUserIDByEmailIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var email *string
	err := pool.QueryRow(ctx,
		`select email from auth.users where email is not null limit 1`).Scan(&email)
	if err != nil {
		t.Skip("the local Supabase stack seeded no user with an email address")
	}

	q := db.New(pool)
	id, err := q.GetUserIDByEmail(ctx, upper(*email))

	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, id)
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - ('a' - 'A')
		}
	}
	return string(out)
}

func TestInTxRollsBackEverythingWhenTheCallbackFails(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var teamID uuid.UUID
	wantErr := errTest

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, "rolled back")
		require.NoError(t, err)
		teamID = team.ID
		return wantErr
	})

	require.ErrorIs(t, err, wantErr)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from team where id = $1`, teamID).Scan(&count))
	require.Zero(t, count, "InTx must roll back every write the callback made")
}

func TestInTxCommitsWhenTheCallbackSucceeds(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var teamID uuid.UUID
	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, "committed")
		if err != nil {
			return err
		}
		teamID = team.ID
		return nil
	}))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	var name string
	require.NoError(t, pool.QueryRow(ctx,
		`select name from team where id = $1`, teamID).Scan(&name))
	require.Equal(t, "committed", name)
}

func TestListAuditLogFiltersByActionAndPaginates(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	for _, action := range []string{"team.created", "team.renamed", "team.renamed"} {
		require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			TeamID:      &teamID,
			ActorUserID: &userID,
			Action:      action,
			EntityType:  "team",
			EntityID:    &teamID,
			Metadata:    []byte(`{}`),
		}))
	}

	renamed := "team.renamed"
	rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{
		TeamID:       teamID,
		Action:       &renamed,
		ResultLimit:  25,
		ResultOffset: 0,
	})

	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.EqualValues(t, 2, rows[0].TotalCount)
	for _, row := range rows {
		require.Equal(t, "team.renamed", row.Action)
	}
}

func TestListAuditLogNeverCrossesTeams(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('other') returning id`).Scan(&otherTeamID))

	q := db.New(tx)
	require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &otherTeamID,
		ActorUserID: &userID,
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    &otherTeamID,
		Metadata:    []byte(`{}`),
	}))

	rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{
		TeamID:       teamID,
		ResultLimit:  25,
		ResultOffset: 0,
	})

	require.NoError(t, err)
	require.Empty(t, rows, "an audit entry belonging to another team must never be listed")
}

// TestLockTeamOwnersSerializesConcurrentDemotions is the reason LockTeamOwners
// exists rather than a plain COUNT(*): two simultaneous demotions must not
// both observe two owners and both proceed, leaving the team ownerless. This
// runs two real, concurrently-committed transactions against the live pool —
// not a single rolled-back tx like the rest of this file — because the
// property under test is about row-lock serialization, which a shared
// transaction cannot exercise.
func TestLockTeamOwnersSerializesConcurrentDemotions(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	// Two distinct owners are required (team_member's PK is (team_id,
	// user_id), so one user cannot hold the role twice). The local stack
	// seeds only one auth.users row, so a second, minimal one is created
	// just for this test — id is the table's only NOT NULL column — and
	// removed again in cleanup.
	var firstOwner uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&firstOwner))

	secondOwner := uuid.New()
	_, err := pool.Exec(ctx, `insert into auth.users (id) values ($1)`, secondOwner)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, secondOwner)
	})

	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('lock owners fixture') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	for _, owner := range []uuid.UUID{firstOwner, secondOwner} {
		_, err := pool.Exec(ctx,
			`insert into team_member (team_id, user_id, role) values ($1, $2, 'owner')`, teamID, owner)
		require.NoError(t, err)
	}

	// barrier holds both goroutines back until both have already opened
	// their transaction, so their LockTeamOwners calls are issued at the
	// same instant instead of one full transaction completing before the
	// other has even started — otherwise there is no real race to serialize.
	var barrier sync.WaitGroup
	barrier.Add(2)

	// demote locks the team's owner rows, then only proceeds if it still
	// sees more than one owner — the same guard a real handler would use
	// before allowing a demotion.
	demote := func(target uuid.UUID) error {
		return db.InTx(ctx, pool, func(q *db.Queries) error {
			barrier.Done()
			barrier.Wait()

			owners, err := q.LockTeamOwners(ctx, teamID)
			if err != nil {
				return err
			}
			if len(owners) <= 1 {
				return nil // last owner: refuse, do not demote
			}
			return q.UpdateTeamMemberRole(ctx, db.UpdateTeamMemberRoleParams{
				TeamID: teamID,
				UserID: target,
				Role:   "admin",
			})
		})
	}

	// Two goroutines race to demote two *different* owners, so they are
	// genuinely competing rather than each idempotently no-opping on the
	// same row.
	targets := []uuid.UUID{firstOwner, secondOwner}
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for i, target := range targets {
		go func(i int, target uuid.UUID) {
			defer wg.Done()
			errs[i] = demote(target)
		}(i, target)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	rows, err := pool.Query(ctx,
		`select user_id, role from team_member where team_id = $1`, teamID)
	require.NoError(t, err)
	roles := map[uuid.UUID]string{}
	for rows.Next() {
		var uid uuid.UUID
		var role string
		require.NoError(t, rows.Scan(&uid, &role))
		roles[uid] = role
	}
	rows.Close()
	require.NoError(t, rows.Err())

	require.Len(t, roles, 2, "both team_member rows must still exist")
	ownerCount, adminCount := 0, 0
	for _, role := range roles {
		switch role {
		case "owner":
			ownerCount++
		case "admin":
			adminCount++
		default:
			t.Fatalf("unexpected role %q", role)
		}
	}

	// The assertion that matters: without the row lock, both transactions
	// would read "two owners" and both demote, leaving zero owners. With the
	// lock, exactly one demotion succeeds and one owner always remains.
	require.Equal(t, 1, ownerCount,
		"LockTeamOwners must serialize concurrent demotions so exactly one owner always remains")
	require.Equal(t, 1, adminCount, "exactly one of the two demotions must have succeeded")
}
