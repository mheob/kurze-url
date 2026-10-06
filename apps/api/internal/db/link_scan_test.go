package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// scanNow is the clock every test in this file runs the due queries at.
//
// The due list and its count are instance-wide on purpose, and `go test ./...`
// runs other packages' tests in parallel processes against this database,
// committing links of their own, flagged and never-checked ones among them.
// So no test here assumes its links sort first, or that the rows it gets back
// are only its own. Two things keep the tests exact instead:
//
//   - Everything a test seeds goes through its own REPEATABLE READ transaction,
//     rolled back when the test ends. No other process can see, scan or lock
//     it, and both reads of a count delta see one snapshot.
//   - Every assertion on a list is on the test's own ids: the rows are
//     filtered to them (ownIDs) before anything is compared, and a limit that
//     has to see all of them is the count of everything due, never a guess.
//
// The links are created in the year 2000 only so their dates stay clear of
// anything a fixture elsewhere seeds; nothing relies on where they sort.
var scanNow = time.Date(2000, 6, 1, 12, 0, 0, 0, time.UTC)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}

// scanFixture is one team with one verified domain, seeded inside a REPEATABLE
// READ transaction that the test's cleanup rolls back.
type scanFixture struct {
	tx       pgx.Tx
	queries  *db.Queries
	teamID   uuid.UUID
	userID   uuid.UUID
	domainID uuid.UUID
	hostname string
}

func newScanFixture(t *testing.T) *scanFixture {
	t.Helper()
	ctx := context.Background()

	tx, err := testPool(t).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	f := &scanFixture{tx: tx, queries: db.New(tx), hostname: "s" + uuid.NewString()[:8] + ".test"}
	f.teamID, f.userID = seedTeamWithOwner(ctx, t, tx)
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.teamID, f.hostname).Scan(&f.domainID))
	return f
}

// scanSeed describes one link by the columns the due predicate reads.
type scanSeed struct {
	State       string
	CreatedAt   string // RFC 3339, in the year 2000; see scanNow
	Destination string
	CheckedAt   *time.Time
	CheckedURL  *string
	ExpiresAt   *time.Time
}

func (f *scanFixture) link(t *testing.T, seed scanSeed) uuid.UUID {
	t.Helper()
	if seed.State == "" {
		seed.State = "active"
	}
	if seed.Destination == "" {
		seed.Destination = "https://example.org/" + uuid.NewString()
	}

	var id uuid.UUID
	require.NoError(t, f.tx.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, state, expires_at,
		                   created_by, created_at, scan_checked_at, scan_destination)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
		f.domainID, f.teamID, "s-"+uuid.NewString()[:8], seed.Destination, seed.State,
		seed.ExpiresAt, f.userID, at(t, seed.CreatedAt), seed.CheckedAt, seed.CheckedURL,
	).Scan(&id))
	return id
}

// dueMix is one link per branch of the due predicate, five that are due and
// three that are not, so a predicate that is too loose shows up as an extra id
// and one that is too tight as a missing one.
type dueMix struct {
	never, expiresLater, moved, flagged, stale uuid.UUID // due
	fresh, disabled, expired                   uuid.UUID // not due
}

func (f *scanFixture) seedDueMix(t *testing.T) dueMix {
	t.Helper()
	return dueMix{
		never: f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"}),
		expiresLater: f.link(t, scanSeed{
			CreatedAt: "2000-01-02T00:00:00Z", ExpiresAt: ptr(at(t, "2001-01-01T00:00:00Z")),
		}),
		moved: f.link(t, scanSeed{
			CreatedAt: "2000-01-03T00:00:00Z", Destination: "https://example.org/new",
			CheckedAt: ptr(at(t, "2000-06-01T11:45:00Z")), CheckedURL: ptr("https://example.org/old"),
		}),
		flagged: f.link(t, scanSeed{
			State: "flagged", CreatedAt: "2000-01-04T00:00:00Z", Destination: "https://example.org/flagged",
			CheckedAt: ptr(at(t, "2000-06-01T11:30:00Z")), CheckedURL: ptr("https://example.org/flagged"),
		}),
		stale: f.link(t, scanSeed{
			CreatedAt: "2000-01-05T00:00:00Z", Destination: "https://example.org/stale",
			CheckedAt: ptr(at(t, "2000-05-30T00:00:00Z")), CheckedURL: ptr("https://example.org/stale"),
		}),
		fresh: f.link(t, scanSeed{
			CreatedAt: "2000-01-06T00:00:00Z", Destination: "https://example.org/fresh",
			CheckedAt: ptr(at(t, "2000-06-01T11:00:00Z")), CheckedURL: ptr("https://example.org/fresh"),
		}),
		disabled: f.link(t, scanSeed{State: "disabled", CreatedAt: "2000-01-07T00:00:00Z"}),
		expired: f.link(t, scanSeed{
			CreatedAt: "2000-01-08T00:00:00Z", ExpiresAt: ptr(at(t, "2000-05-01T00:00:00Z")),
		}),
	}
}

// all is every seeded id, due or not.
func (m dueMix) all() []uuid.UUID {
	return []uuid.UUID{m.never, m.expiresLater, m.moved, m.flagged, m.stale, m.fresh, m.disabled, m.expired}
}

// due is the ids that are due, in the order ListDueLinksForScan must return them.
func (m dueMix) due() []uuid.UUID {
	return []uuid.UUID{m.never, m.expiresLater, m.moved, m.flagged, m.stale}
}

// ownIDs keeps the rows whose id is one of ids, in the order the query
// returned them. Every other row was committed by somebody else.
func ownIDs(rows []db.ListDueLinksForScanRow, ids ...uuid.UUID) []uuid.UUID {
	mine := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		mine[id] = true
	}
	var out []uuid.UUID
	for _, row := range rows {
		if mine[row.ID] {
			out = append(out, row.ID)
		}
	}
	return out
}

// listAllDue lists everything due at scanNow. The limit is the count taken in
// the same snapshot, so rows other tests committed cannot push a seeded one
// out of the page.
func (f *scanFixture) listAllDue(t *testing.T) (rows []db.ListDueLinksForScanRow, total int64) {
	t.Helper()
	ctx := context.Background()

	total, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)
	rows, err = f.queries.ListDueLinksForScan(ctx, db.ListDueLinksForScanParams{
		Now: scanNow, BatchLimit: int32(total),
	})
	require.NoError(t, err)
	return rows, total
}

// The order is the spec's: links whose current destination was never
// checked, then flagged links, then the longest-unchecked. Three links that
// are not due are seeded beside them.
func TestListDueLinksForScanPutsTheOldestDebtFirst(t *testing.T) {
	f := newScanFixture(t)
	mix := f.seedDueMix(t)

	rows, _ := f.listAllDue(t)

	require.Equal(t, mix.due(), ownIDs(rows, mix.all()...))
}

// A limit of two returns two rows, and they are the first two of the same
// ordered list. Which rows those are is not asserted: other tests commit due
// links too, and they may sort ahead of these.
func TestListDueLinksForScanStopsAtTheLimit(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()

	first := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	second := f.link(t, scanSeed{CreatedAt: "2000-01-02T00:00:00Z"})
	third := f.link(t, scanSeed{CreatedAt: "2000-01-03T00:00:00Z"})

	all, total := f.listAllDue(t)
	require.GreaterOrEqual(t, total, int64(3))

	limited, err := f.queries.ListDueLinksForScan(ctx, db.ListDueLinksForScanParams{
		Now: scanNow, BatchLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, limited, 2)
	require.Equal(t, all[:2], limited, "the limit cuts the same ordered list, it does not reorder it")

	// The team travels with the row: the sweep needs it to filter its writes.
	var seeded *db.ListDueLinksForScanRow
	for i := range all {
		if all[i].ID == first {
			seeded = &all[i]
		}
	}
	require.NotNil(t, seeded, "a link that was never checked is due")
	require.Equal(t, f.teamID, seeded.TeamID)
	require.Equal(t, []uuid.UUID{first, second, third}, ownIDs(all, first, second, third))
}

// Both counts run in one REPEATABLE READ snapshot, so what other processes
// commit in between cannot move the difference.
func TestCountDueLinksForScanCountsExactlyWhatIsDue(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()

	before, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)

	f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	f.link(t, scanSeed{
		State: "flagged", CreatedAt: "2000-01-02T00:00:00Z", Destination: "https://example.org/f",
		CheckedAt: ptr(at(t, "2000-06-01T11:59:00Z")), CheckedURL: ptr("https://example.org/f"),
	})
	f.link(t, scanSeed{
		CreatedAt: "2000-01-03T00:00:00Z", Destination: "https://example.org/s",
		CheckedAt: ptr(at(t, "2000-05-01T00:00:00Z")), CheckedURL: ptr("https://example.org/s"),
	})
	f.link(t, scanSeed{
		CreatedAt: "2000-01-04T00:00:00Z", Destination: "https://example.org/fresh",
		CheckedAt: ptr(at(t, "2000-06-01T11:00:00Z")), CheckedURL: ptr("https://example.org/fresh"),
	})
	f.link(t, scanSeed{State: "disabled", CreatedAt: "2000-01-05T00:00:00Z"})
	f.link(t, scanSeed{CreatedAt: "2000-01-06T00:00:00Z", ExpiresAt: ptr(at(t, "2000-05-01T00:00:00Z"))})

	after, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)
	require.EqualValues(t, 3, after-before)
}

// ListDueLinksForScan and CountDueLinksForScan carry the same predicate twice,
// because sqlc has no shared fragments. This is what makes the copies stay
// one: over the same data, the links the count adds are exactly the links the
// list adds.
func TestCountDueLinksForScanAgreesWithTheListOverTheSameLinks(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()

	before, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)
	mix := f.seedDueMix(t)
	after, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)

	rows, _ := f.listAllDue(t)

	listed := ownIDs(rows, mix.all()...)
	require.Len(t, listed, len(mix.due()))
	require.EqualValues(t, len(listed), after-before,
		"the count's predicate has drifted from the list's")
}

func TestGetLinkForScanReadsTheLinkAndItsHostnameWithinItsTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z", Destination: "https://example.org/read"})

	row, err := f.queries.GetLinkForScan(ctx, db.GetLinkForScanParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, "https://example.org/read", row.DestinationURL)
	require.Equal(t, "active", row.State)
	require.Equal(t, f.hostname, row.Hostname)
	require.NotEmpty(t, row.Slug)

	_, err = f.queries.GetLinkForScan(ctx, db.GetLinkForScanParams{ID: id, TeamID: uuid.New()})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

// updated_at is the dashboard's "last changed by somebody". A daily check of
// every link must not turn it into "last scanned".
func TestRecordLinkScanWritesTheCheckButNotUpdatedAt(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	_, err := f.tx.Exec(ctx, `update link set updated_at = '2000-01-01T00:00:00Z' where id = $1`, id)
	require.NoError(t, err)

	require.NoError(t, f.queries.RecordLinkScan(ctx, db.RecordLinkScanParams{
		ID: id, TeamID: f.teamID, State: "flagged",
		CheckedAt: scanNow, CheckedDestination: "https://example.org/checked",
	}))
	// Another team's id writes nothing.
	require.NoError(t, f.queries.RecordLinkScan(ctx, db.RecordLinkScanParams{
		ID: id, TeamID: uuid.New(), State: "active",
		CheckedAt: scanNow, CheckedDestination: "https://example.org/other",
	}))

	var (
		state, checkedURL    string
		checkedAt, updatedAt time.Time
	)
	require.NoError(t, f.tx.QueryRow(ctx,
		`select state, scan_checked_at, scan_destination, updated_at from link where id = $1`, id,
	).Scan(&state, &checkedAt, &checkedURL, &updatedAt))
	require.Equal(t, "flagged", state)
	require.True(t, scanNow.Equal(checkedAt))
	require.Equal(t, "https://example.org/checked", checkedURL)
	require.True(t, at(t, "2000-01-01T00:00:00Z").Equal(updatedAt))
}

// A PATCH that lifts a flag forgets which destination the last check judged,
// and only for its own team's link. scan_checked_at stays: it still says when
// that check ran.
func TestClearLinkScanDestinationForgetsTheCheckedURLWithinTheTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{
		CreatedAt: "2000-01-01T00:00:00Z", Destination: "https://example.org/flagged",
		CheckedAt: ptr(scanNow), CheckedURL: ptr("https://example.org/flagged"),
	})
	checkedURL := func() (url *string) {
		require.NoError(t, f.tx.QueryRow(ctx,
			`select scan_destination from link where id = $1`, id).Scan(&url))
		return url
	}

	require.NoError(t, f.queries.ClearLinkScanDestination(ctx, db.ClearLinkScanDestinationParams{
		ID: id, TeamID: uuid.New(),
	}))
	require.Equal(t, ptr("https://example.org/flagged"), checkedURL(), "another team must not touch it")

	require.NoError(t, f.queries.ClearLinkScanDestination(ctx, db.ClearLinkScanDestinationParams{
		ID: id, TeamID: f.teamID,
	}))
	require.Nil(t, checkedURL())

	var checkedAt *time.Time
	require.NoError(t, f.tx.QueryRow(ctx,
		`select scan_checked_at from link where id = $1`, id).Scan(&checkedAt))
	require.NotNil(t, checkedAt)
	require.True(t, scanNow.Equal(*checkedAt))
}

func TestGetLatestLinkScanResultReturnsTheNewestRowWithinTheTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})

	for _, params := range []db.InsertLinkScanResultParams{
		{
			LinkID: id, TeamID: f.teamID, Verdict: "flagged", DestinationURL: "https://example.org/x",
			ThreatTypes: []string{"MALWARE"}, ScannedAt: at(t, "2000-02-01T00:00:00Z"),
		},
		{
			LinkID: id, TeamID: f.teamID, Verdict: "clean", DestinationURL: "https://example.org/x",
			ThreatTypes: []string{}, ScannedAt: at(t, "2000-03-01T00:00:00Z"),
		},
	} {
		written, err := f.queries.InsertLinkScanResult(ctx, params)
		require.NoError(t, err)
		require.EqualValues(t, 1, written)
	}

	latest, err := f.queries.GetLatestLinkScanResult(ctx, db.GetLatestLinkScanResultParams{
		LinkID: id, TeamID: f.teamID,
	})
	require.NoError(t, err)
	require.Equal(t, "clean", latest.Verdict)
	require.Empty(t, latest.ThreatTypes)
	require.Equal(t, "https://example.org/x", latest.DestinationURL)
	require.True(t, at(t, "2000-03-01T00:00:00Z").Equal(latest.ScannedAt))

	_, err = f.queries.GetLatestLinkScanResult(ctx, db.GetLatestLinkScanResultParams{
		LinkID: id, TeamID: uuid.New(),
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "the result table has no team_id, so the link's must filter it")
}

// link_scan_result has no team_id of its own, so the insert takes it from the
// link it selects from: a link that is not the caller's team's gets no row,
// like every other verdict write. The miss is not an error, so the query
// reports how many rows it wrote, and that count is what lets applyVerdict
// refuse a verdict change that left no record behind.
func TestInsertLinkScanResultWritesNothingForAnotherTeamsLink(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})

	count := func() (n int) {
		require.NoError(t, f.tx.QueryRow(ctx,
			`select count(*) from link_scan_result where link_id = $1`, id).Scan(&n))
		return n
	}

	written, err := f.queries.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
		LinkID: id, TeamID: uuid.New(), Verdict: "flagged", DestinationURL: "https://example.org/x",
		ThreatTypes: []string{"MALWARE"}, ScannedAt: scanNow,
	})
	require.NoError(t, err)
	require.Zero(t, written, "the miss must be visible to the caller, not only in the table")
	require.Zero(t, count(), "a team that does not own the link must not write its verdict")

	// A clean verdict has no threat types, and a nil slice is how Go says so.
	// It must land as an empty array, not as the NULL the column refuses.
	written, err = f.queries.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
		LinkID: id, TeamID: f.teamID, Verdict: "clean", DestinationURL: "https://example.org/x",
		ThreatTypes: nil, ScannedAt: scanNow,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, written)
	require.Equal(t, 1, count())
}

func TestGetLinkForAPIReportsTheLastCheckAndItsForUpdateTwinFiltersByTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{
		CreatedAt: "2000-01-01T00:00:00Z", Destination: "https://example.org/api",
		CheckedAt: ptr(scanNow), CheckedURL: ptr("https://example.org/api"),
	})

	row, err := f.queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.NotNil(t, row.ScanCheckedAt)
	require.True(t, scanNow.Equal(*row.ScanCheckedAt))
	require.NotNil(t, row.ScanDestination)
	require.Equal(t, "https://example.org/api", *row.ScanDestination)

	twin, err := f.queries.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, id, twin.ID)
	require.Equal(t, row.ScanCheckedAt, twin.ScanCheckedAt)
	require.Equal(t, row.ScanDestination, twin.ScanDestination)

	_, err = f.queries.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{ID: id, TeamID: uuid.New()})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

// The two locking reads exist for the lock, so something has to show they
// take it. The fixture's transaction is uncommitted, which keeps any other
// connection from contending for the row, so the test reads the evidence off
// the row instead: a row locked by this transaction carries its id in xmax,
// and a row that was only read, or only inserted, carries 0. The plain read
// is the control, showing the check can tell the two apart.
func TestTheLockingReadsLockTheLinkRowAndThePlainReadDoesNot(t *testing.T) {
	cases := map[string]struct {
		read   func(ctx context.Context, q *db.Queries, id, teamID uuid.UUID) error
		locked bool
	}{
		"GetLinkForAPI": {
			read: func(ctx context.Context, q *db.Queries, id, teamID uuid.UUID) error {
				_, err := q.GetLinkForAPI(ctx, db.GetLinkForAPIParams{ID: id, TeamID: teamID})
				return err
			},
			locked: false,
		},
		"GetLinkForAPIForUpdate": {
			read: func(ctx context.Context, q *db.Queries, id, teamID uuid.UUID) error {
				_, err := q.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{ID: id, TeamID: teamID})
				return err
			},
			locked: true,
		},
		"GetLinkForScan": {
			read: func(ctx context.Context, q *db.Queries, id, teamID uuid.UUID) error {
				_, err := q.GetLinkForScan(ctx, db.GetLinkForScanParams{ID: id, TeamID: teamID})
				return err
			},
			locked: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newScanFixture(t)
			ctx := context.Background()
			id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})

			// xmax is an xid, 32 bits wide; txid_current() carries an epoch above it.
			heldByThisTx := func() (held bool) {
				require.NoError(t, f.tx.QueryRow(ctx,
					`select xmax::text::bigint = txid_current() % 4294967296 from link where id = $1`, id,
				).Scan(&held))
				return held
			}

			require.False(t, heldByThisTx(), "a freshly inserted row is not locked")
			require.NoError(t, tc.read(ctx, f.queries, id, f.teamID))
			require.Equal(t, tc.locked, heldByThisTx())
		})
	}
}
