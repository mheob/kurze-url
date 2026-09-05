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

	emptyMetadata := "{}"
	for _, action := range []string{"team.created", "team.renamed", "team.renamed"} {
		require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			TeamID:      &teamID,
			ActorUserID: &userID,
			Action:      action,
			EntityType:  "team",
			EntityID:    &teamID,
			Metadata:    &emptyMetadata,
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
	emptyMetadata := "{}"
	require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &otherTeamID,
		ActorUserID: &userID,
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    &otherTeamID,
		Metadata:    &emptyMetadata,
	}))

	rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{
		TeamID:       teamID,
		ResultLimit:  25,
		ResultOffset: 0,
	})

	require.NoError(t, err)
	require.Empty(t, rows, "an audit entry belonging to another team must never be listed")
}

// TestCountFoldersForTeamOnlyCountsTheCallersFolders and
// TestCountTagsForTeamOnlyCountsTheCallersTags exercise CountFoldersForTeam
// and CountTagsForTeam directly, the same way the tests below exercise
// UpdateFolder and friends: an HTTP-level test cannot tell a per-team count
// from a global one, because CountFoldersForTeam/CountTagsForTeam feed both
// the per-team creation cap (where a global count still refuses new rows,
// just for the wrong reason) and the ListXForTeam pagination fallback (which
// no folder or tag test reaches). Calling the query directly against rows
// seeded in two different teams is the only way to see the team_id filter
// actually doing something.
func TestCountFoldersForTeamOnlyCountsTheCallersFolders(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('other') returning id`).Scan(&otherTeamID))

	_, err = tx.Exec(ctx,
		`insert into folder (team_id, name) values ($1, 'Mine A'), ($1, 'Mine B')`, teamID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`insert into folder (team_id, name) values ($1, 'Theirs A'), ($1, 'Theirs B'), ($1, 'Theirs C')`,
		otherTeamID)
	require.NoError(t, err)

	q := db.New(tx)
	count, err := q.CountFoldersForTeam(ctx, teamID)

	require.NoError(t, err)
	require.EqualValues(t, 2, count,
		"the count must include only this team's own folders, not every team's")
}

func TestCountTagsForTeamOnlyCountsTheCallersTags(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('other') returning id`).Scan(&otherTeamID))

	_, err = tx.Exec(ctx,
		`insert into tag (team_id, name) values ($1, 'Mine A'), ($1, 'Mine B')`, teamID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`insert into tag (team_id, name) values ($1, 'Theirs A'), ($1, 'Theirs B'), ($1, 'Theirs C')`,
		otherTeamID)
	require.NoError(t, err)

	q := db.New(tx)
	count, err := q.CountTagsForTeam(ctx, teamID)

	require.NoError(t, err)
	require.EqualValues(t, 2, count,
		"the count must include only this team's own tags, not every team's")
}

// The four tests below exercise UpdateFolder, DeleteFolder, UpdateTag and
// DeleteTag directly against a team_id that does not own the row, bypassing
// internal/api entirely. That bypass is deliberate: FolderEditorScope and
// TagEditorScope discover a resource's real team from its ID alone (via
// GetFolderScope / GetTagScope) and then require the caller to be a member of
// exactly that team, so by the time either handler calls one of these four
// queries, member.TeamID is already guaranteed to equal the row's own
// team_id — an HTTP request that reached the handler with a mismatched
// team_id is not constructible. That makes the queries' own "and team_id =
// $2" clause pure defense in depth from the API's perspective, and no
// isolation test that goes through the router can ever observe it being
// dropped: TestAnotherTeamsFolderCannotBeRenamed and its three siblings in
// internal/api all still pass with that clause deleted, because the entity
// scope already turned the attempt into a 404 before either query ran. These
// four tests are the equivalent property at the layer where it is actually
// decided, so a regression here is caught even if a future authorization path
// ever reaches one of these queries without having already checked the team.

func TestUpdateFolderRefusesAnotherTeamsID(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	var folderID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into folder (team_id, name) values ($1, 'Fremd') returning id`,
		teamID).Scan(&folderID))

	q := db.New(tx)
	_, err = q.UpdateFolder(ctx, db.UpdateFolderParams{
		ID: folderID, TeamID: uuid.New(), Name: "Gekapert",
	})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"a folder must not be renamed through a team_id that does not own it")

	var name string
	require.NoError(t, tx.QueryRow(ctx,
		`select name from folder where id = $1`, folderID).Scan(&name))
	require.Equal(t, "Fremd", name, "the row must be unchanged")
}

func TestDeleteFolderRefusesAnotherTeamsID(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	var folderID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into folder (team_id, name) values ($1, 'Fremd') returning id`,
		teamID).Scan(&folderID))

	q := db.New(tx)
	_, err = q.DeleteFolder(ctx, db.DeleteFolderParams{ID: folderID, TeamID: uuid.New()})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"a folder must not be deleted through a team_id that does not own it")

	var exists bool
	require.NoError(t, tx.QueryRow(ctx,
		`select exists(select 1 from folder where id = $1)`, folderID).Scan(&exists))
	require.True(t, exists, "the row must still exist")
}

func TestUpdateTagRefusesAnotherTeamsID(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	var tagID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into tag (team_id, name) values ($1, 'Fremd') returning id`,
		teamID).Scan(&tagID))

	q := db.New(tx)
	_, err = q.UpdateTag(ctx, db.UpdateTagParams{ID: tagID, TeamID: uuid.New(), Name: "Gekapert"})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"a tag must not be renamed through a team_id that does not own it")

	var name string
	require.NoError(t, tx.QueryRow(ctx,
		`select name from tag where id = $1`, tagID).Scan(&name))
	require.Equal(t, "Fremd", name, "the row must be unchanged")
}

func TestDeleteTagRefusesAnotherTeamsID(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	var tagID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into tag (team_id, name) values ($1, 'Fremd') returning id`,
		teamID).Scan(&tagID))

	q := db.New(tx)
	_, err = q.DeleteTag(ctx, db.DeleteTagParams{ID: tagID, TeamID: uuid.New()})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"a tag must not be deleted through a team_id that does not own it")

	var exists bool
	require.NoError(t, tx.QueryRow(ctx,
		`select exists(select 1 from tag where id = $1)`, tagID).Scan(&exists))
	require.True(t, exists, "the row must still exist")
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
