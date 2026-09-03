package db_test

import (
	"context"
	"errors"
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
