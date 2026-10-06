package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// The fixture link's destination, from newFixture's defaults.
const fixtureDestination = "https://example.org/hello"

func flagged(threats ...string) scanning.Result {
	return scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute}
}

func clean() scanning.Result {
	return scanning.Result{ValidFor: 5 * time.Minute}
}

func linkState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var state string
	require.NoError(t, pool.QueryRow(context.Background(),
		`select state from link where id = $1`, id).Scan(&state))
	return state
}

func scanCheckedAt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) *time.Time {
	t.Helper()
	var checkedAt *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`select scan_checked_at from link where id = $1`, id).Scan(&checkedAt))
	return checkedAt
}

func scanResultCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`select count(*) from link_scan_result where link_id = $1`, id).Scan(&count))
	return count
}

// eventuallyState waits for a background check a handler started to land.
func eventuallyState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var state string
		err := pool.QueryRow(context.Background(), `select state from link where id = $1`, id).Scan(&state)
		return err == nil && state == want
	}, 5*time.Second, 20*time.Millisecond, "link %s never became %q", id, want)
}

// eventuallyChecked waits until a check of url has been recorded for the link.
func eventuallyChecked(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var checked *string
		err := pool.QueryRow(context.Background(),
			`select scan_destination from link where id = $1`, id).Scan(&checked)
		return err == nil && checked != nil && *checked == url
	}, 5*time.Second, 20*time.Millisecond, "link %s was never checked against %s", id, url)
}

// confirmationTTLLeft is negative when there is no confirmation key at all.
func confirmationTTLLeft(t *testing.T, client *cache.Client, id uuid.UUID) time.Duration {
	t.Helper()
	ttl, err := client.Raw().TTL(context.Background(), client.Key("sb:confirmed:"+id.String())).Result()
	require.NoError(t, err)
	return ttl
}

type auditRow struct {
	actor    *uuid.UUID
	metadata string
}

func auditRows(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, action string) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`select actor_user_id, metadata::text from audit_log where entity_id = $1 and action = $2`,
		id, action)
	require.NoError(t, err)
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var row auditRow
		require.NoError(t, rows.Scan(&row.actor, &row.metadata))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestApplyVerdictFlagsAnActiveLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := captureLogs(f)
	teamID := fixtureTeamID(t, f)

	// Cache the active link first, so the flag has something stale to clear.
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, teamID, fixtureDestination,
		flagged("SOCIAL_ENGINEERING"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("flagged"), outcome)

	var (
		state, checkedURL string
		checkedAt         time.Time
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select state, scan_checked_at, scan_destination from link where id = $1`, f.linkID,
	).Scan(&state, &checkedAt, &checkedURL))
	require.Equal(t, "flagged", state)
	require.True(t, f.deps.Now().Equal(checkedAt))
	require.Equal(t, fixtureDestination, checkedURL)

	var (
		verdict, judged string
		threats         []string
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select verdict, threat_types, destination_url from link_scan_result where link_id = $1`, f.linkID,
	).Scan(&verdict, &threats, &judged))
	require.Equal(t, "flagged", verdict)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, threats)
	require.Equal(t, fixtureDestination, judged)

	entries := auditRows(t, f.pool, f.linkID, "link.flagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor, "the scanner is the system, not a member")
	require.JSONEq(t,
		`{"threat_types":["SOCIAL_ENGINEERING"],"destination_url":"https://example.org/hello"}`,
		entries[0].metadata)

	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 3*time.Minute)
	require.LessOrEqual(t, ttl, 4*time.Minute)

	_, err = f.deps.Cache.Raw().Get(ctx, f.deps.Cache.Key(link.CacheKey(f.hostname, "hello"))).Result()
	require.ErrorIs(t, err, redis.Nil,
		"the cached active link must be gone, or the next redirect forwards it for another hour")

	require.Contains(t, logs.String(), `level=ERROR msg="link flagged by Safe Browsing"`,
		"an Error log is how the maintainer hears of a flag")
}

func TestApplyVerdictUnflagsAFlaggedLinkThatChecksClean(t *testing.T) {
	f := newFixture(t, withState("flagged"))
	ctx := context.Background()
	logs := captureLogs(f)
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, f.linkID.String(), []string{"MALWARE"}, 10*time.Minute))
	cacheKey := link.CacheKey(f.hostname, "hello")
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: f.linkID, State: "flagged", DestinationURL: fixtureDestination,
	}, time.Hour))

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination, clean())
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("unflagged"), outcome)

	require.Equal(t, "active", linkState(t, f.pool, f.linkID))

	var (
		verdict string
		threats []string
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select verdict, threat_types from link_scan_result where link_id = $1`, f.linkID,
	).Scan(&verdict, &threats))
	require.Equal(t, "clean", verdict)
	require.Empty(t, threats)

	entries := auditRows(t, f.pool, f.linkID, "link.unflagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor)

	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), "the confirmation must be gone")
	_, err = f.deps.Cache.Raw().Get(ctx, f.deps.Cache.Key(cacheKey)).Result()
	require.ErrorIs(t, err, redis.Nil)
	require.Contains(t, logs.String(), `level=INFO msg="link unflagged by Safe Browsing"`)
}

// Still flagged: the check is recorded and the confirmation renewed, and that
// is all. The cached link already says "flagged", so it is left alone.
func TestApplyVerdictRefreshesTheConfirmationOfAStillFlaggedLink(t *testing.T) {
	f := newFixture(t, withState("flagged"))
	ctx := context.Background()
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, f.linkID.String(), []string{"MALWARE"}, time.Minute))
	cacheKey := link.CacheKey(f.hostname, "hello")
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: f.linkID, State: "flagged", DestinationURL: fixtureDestination,
	}, time.Hour))

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("confirmed"), outcome)

	require.Zero(t, scanResultCount(t, f.pool, f.linkID), "a row is written only when the verdict changes")
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
	checkedAt := scanCheckedAt(t, f.pool, f.linkID)
	require.NotNil(t, checkedAt)
	require.True(t, f.deps.Now().Equal(*checkedAt))
	require.Greater(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), 3*time.Minute,
		"the one-minute confirmation must have been replaced by the new answer's")
	_, err = f.deps.Cache.Raw().Get(ctx, f.deps.Cache.Key(cacheKey)).Result()
	require.NoError(t, err, "a refresh changes nothing the cached link says, so it must not evict it")
}

func TestApplyVerdictOnACleanActiveLinkOnlyRecordsTheCheck(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		fixtureDestination, clean())
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("unchanged"), outcome)

	checkedAt := scanCheckedAt(t, f.pool, f.linkID)
	require.NotNil(t, checkedAt)
	require.True(t, f.deps.Now().Equal(*checkedAt))
	require.Zero(t, scanResultCount(t, f.pool, f.linkID), "the first clean check writes no row")
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))
}

// Review focus: the verdict is for a URL the link no longer points at. It must
// not touch the link at all, so the link stays due for its new destination.
func TestApplyVerdictDiscardsAVerdictForAReplacedDestination(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		"https://example.org/the-old-destination", flagged("SOCIAL_ENGINEERING"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("stale"), outcome)

	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
	require.Zero(t, scanResultCount(t, f.pool, f.linkID))
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
}

// Review focus: the link was deleted while its check was in flight.
func TestApplyVerdictForADeletedLinkIsANoOp(t *testing.T) {
	f := newFixture(t)
	teamID := fixtureTeamID(t, f)
	_, err := f.pool.Exec(context.Background(), `delete from link where id = $1`, f.linkID)
	require.NoError(t, err)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, teamID,
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("gone"), outcome)
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
}

// The read filters by team even for the scanner, so a verdict addressed to
// the wrong team finds nothing to write.
func TestApplyVerdictWithAnotherTeamsIDWritesNothing(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, uuid.New(),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("gone"), outcome)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
}

// A link that does not redirect cannot harm anyone, and it must become due
// again when it is re-enabled — so not even the timestamps change.
func TestApplyVerdictLeavesADisabledLinkAlone(t *testing.T) {
	f := newFixture(t, withState("disabled"))

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("skipped"), outcome)
	require.Equal(t, "disabled", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// A verdict Google says may not be relied on still flags the link, but no
// confirmation is written, and an older one is dropped: a key must never
// vouch for longer than the newest answer allows.
func TestApplyVerdictWithoutAUsableValidityFlagsButConfirmsNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, f.linkID.String(), []string{"MALWARE"}, 10*time.Minute))

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination,
		scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 0})
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("flagged"), outcome)
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))
}

// cancelOnCommit is a pgx tracer that cancels a context the moment a COMMIT
// on its pool succeeds. That is the caller's time running out in the gap
// between applyVerdict's transaction and the Redis work that must follow it,
// which a deadline could only hit by chance.
type cancelOnCommit struct{ cancel context.CancelFunc }

type commitQueryKey struct{}

func (c cancelOnCommit) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	return context.WithValue(ctx, commitQueryKey{}, strings.EqualFold(strings.TrimSpace(data.SQL), "commit"))
}

func (c cancelOnCommit) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(commitQueryKey{}).(bool); commit && data.Err == nil {
		c.cancel()
	}
}

// poolCancellingOnCommit is a second pool on the fixture's database whose
// every successful COMMIT calls cancel.
func poolCancellingOnCommit(t *testing.T, base *pgxpool.Pool, cancel context.CancelFunc) *pgxpool.Pool {
	t.Helper()
	config := base.Config()
	config.ConnConfig.Tracer = cancelOnCommit{cancel: cancel}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// The flag has committed, and then the caller's time runs out — the sweep's
// budget, or the ten seconds a background check shares with Google. The
// confirmation and the cache invalidation must still happen: without them the
// cached active entry of a link Google just flagged keeps forwarding visitors
// for up to an hour.
func TestApplyVerdictFinishesAFlagAfterTheCallersTimeRanOutAtTheCommit(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code, "cache the active link first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.deps.Pool = poolCancellingOnCommit(t, f.pool, cancel)

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination,
		flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("flagged"), outcome)
	require.ErrorIs(t, ctx.Err(), context.Canceled, "the commit must have ended the caller's context")

	_, err = f.deps.Cache.Raw().Get(context.Background(),
		f.deps.Cache.Key(link.CacheKey(f.hostname, "hello"))).Result()
	require.ErrorIs(t, err, redis.Nil, "the cached active link must be gone")
	require.Positive(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), "the confirmation must be set")
}

// The same gap when Google clears a link: the confirmation must go, or the
// link keeps its block page for up to 29 minutes after Google cleared it.
func TestApplyVerdictFinishesAnUnflagAfterTheCallersTimeRanOutAtTheCommit(t *testing.T) {
	f := newFixture(t, withState("flagged"))
	background := context.Background()
	require.NoError(t, f.deps.Cache.ConfirmThreats(background, f.linkID.String(), []string{"MALWARE"}, 10*time.Minute))
	cacheKey := link.CacheKey(f.hostname, "hello")
	require.NoError(t, f.deps.Cache.PutLink(background, cacheKey, link.Cached{
		ID: f.linkID, State: "flagged", DestinationURL: fixtureDestination,
	}, time.Hour))
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	f.deps.Pool = poolCancellingOnCommit(t, f.pool, cancel)

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination, clean())
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("unflagged"), outcome)
	require.ErrorIs(t, ctx.Err(), context.Canceled, "the commit must have ended the caller's context")

	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), "the confirmation must be gone")
	_, err = f.deps.Cache.Raw().Get(background, f.deps.Cache.Key(cacheKey)).Result()
	require.ErrorIs(t, err, redis.Nil, "the cached flagged link must be gone")
}

// An instance Vercel froze mid-verdict keeps the link's row lock until its
// connection dies. The next verdict for that link has to give up within
// seconds rather than wait, because the sweep applies one link at a time and
// would otherwise stall behind it until its budget ran out, every run.
func TestApplyVerdictGivesUpOnALinkAnotherTransactionHoldsLocked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	teamID := fixtureTeamID(t, f)
	holder, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	_, err = holder.Exec(ctx, `select id from link where id = $1 for update`, f.linkID)
	require.NoError(t, err)

	// A deadline well past the lock timeout, so a missing timeout fails the
	// test instead of hanging it.
	bounded, cancel := context.WithTimeout(ctx, api.ScanLockTimeout+6*time.Second)
	defer cancel()
	started := time.Now()
	_, err = f.deps.ApplyVerdictForTest(bounded, f.linkID, teamID, fixtureDestination, flagged("MALWARE"))
	elapsed := time.Since(started)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr, "got %v after %s", err, elapsed)
	require.Equal(t, "55P03", pgErr.Code, "lock_not_available")
	require.Less(t, elapsed, api.ScanLockTimeout+2*time.Second)
	require.NoError(t, holder.Rollback(ctx))
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID), "the link stays due")
}

// scanningFixture is a tenancy fixture whose /v1 surface has a fake checker.
func scanningFixture(t *testing.T) (*tenancyFixture, *fakeChecker) {
	t.Helper()
	f := newTenancyFixture(t)
	checker := newFakeChecker()
	f.deps.Scanner = checker
	f.rebuildRouter()
	return f, checker
}

func uniqueDestination(kind string) string {
	return "https://" + kind + "-" + uuid.NewString()[:8] + ".test/login"
}

func TestCreatingALinkChecksItsDestinationRightAway(t *testing.T) {
	f, checker := scanningFixture(t)
	destination := uniqueDestination("phish")
	checker.flag(destination, "SOCIAL_ENGINEERING")

	created := f.createLink(t, "sofort", destination)
	require.Equal(t, "active", created.State,
		"the link is live from the moment it is created; the check comes after")

	eventuallyState(t, f.pool, created.ID, "flagged")
}

// The checker answered, but not about this URL. That is no verdict, and
// recording it as a completed check would take the link off the sweep's
// never-checked list without anyone having judged it.
func TestAnImmediateCheckWithoutAVerdictLeavesTheLinkDue(t *testing.T) {
	f, checker := scanningFixture(t)

	created := f.createLink(t, "stumm", uniqueDestination("unknown"))

	require.Eventually(t, func() bool { return checker.callCount() == 1 },
		5*time.Second, 20*time.Millisecond)
	// No require inside: the condition runs off the test's goroutine.
	require.Never(t, func() bool {
		var checkedAt *time.Time
		err := f.pool.QueryRow(context.Background(),
			`select scan_checked_at from link where id = $1`, created.ID).Scan(&checkedAt)
		return err != nil || checkedAt != nil
	}, 300*time.Millisecond, 20*time.Millisecond)
	require.Equal(t, "active", linkState(t, f.pool, created.ID))
}

// A destination Safe Browsing reads no host in would never get a verdict and
// would starve the sweep, so it is refused with the 422 every other invalid
// destination gets — on create and on PATCH alike, since PATCH has no rate
// limit of its own.
func TestADestinationNoCheckCanReadIsRefused(t *testing.T) {
	f := newTenancyFixture(t)
	const unreadable = "https://\u3002/"

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost, "/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": unreadable})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())

	created := f.createLink(t, "lesbar", "https://example.org/lesbar")
	rec = f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": unreadable})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}

func TestChangingTheDestinationChecksTheNewOneRightAway(t *testing.T) {
	f, checker := scanningFixture(t)
	first, second := uniqueDestination("ok"), uniqueDestination("phish")
	checker.pass(first)
	checker.flag(second, "MALWARE")

	created := f.createLink(t, "umzug", first)
	eventuallyChecked(t, f.pool, created.ID, first)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": second})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	eventuallyState(t, f.pool, created.ID, "flagged")
}

func TestAnEditThatKeepsTheDestinationStartsNoCheck(t *testing.T) {
	f, checker := scanningFixture(t)
	destination := uniqueDestination("ok")
	checker.pass(destination)

	created := f.createLink(t, "ruhig", destination)
	eventuallyChecked(t, f.pool, created.ID, destination)
	require.Equal(t, 1, checker.callCount())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"redirect_type": 301})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	require.Never(t, func() bool { return checker.callCount() > 1 },
		300*time.Millisecond, 20*time.Millisecond)
}

// A disabled link does not redirect, so checking its new destination would
// spend a Google request on a verdict applyVerdict skips anyway. It becomes
// due again when it is re-enabled.
func TestADestinationChangeOnADisabledLinkStartsNoCheck(t *testing.T) {
	f, checker := scanningFixture(t)
	first, second := uniqueDestination("ok"), uniqueDestination("ok")
	checker.pass(first)
	checker.pass(second)

	created := f.createLink(t, "ruhend", first)
	eventuallyChecked(t, f.pool, created.ID, first)
	require.Equal(t, 1, checker.callCount())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": second, "state": "disabled"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	require.Never(t, func() bool { return checker.callCount() > 1 },
		300*time.Millisecond, 20*time.Millisecond)
}

// Disabling a link, pointing it somewhere else and enabling it again must not
// be a way to forward visitors to a destination nobody has checked: the
// destination change starts no check while the link is disabled, so the
// re-enable has to.
func TestReEnablingALinkChecksADestinationChangedWhileItWasDisabled(t *testing.T) {
	f, checker := scanningFixture(t)
	first, second := uniqueDestination("ok"), uniqueDestination("phish")
	checker.pass(first)
	checker.flag(second, "SOCIAL_ENGINEERING")
	editor := f.members[authz.RoleEditor]

	created := f.createLink(t, "wieder", first)
	eventuallyChecked(t, f.pool, created.ID, first)
	path := "/v1/links/" + created.ID.String()
	for _, body := range []map[string]any{
		{"state": "disabled"},
		{"destination_url": second},
		{"state": "active"},
	} {
		rec := f.do(t, editor, http.MethodPatch, path, body)
		require.Equal(t, http.StatusOK, rec.Code, "%v, body: %s", body, rec.Body.String())
	}

	eventuallyChecked(t, f.pool, created.ID, second)
	eventuallyState(t, f.pool, created.ID, "flagged")
	require.Equal(t, 2, checker.callCount(), "one check on create, one on re-enable, none while disabled")
}

// Re-enabling a link whose destination was already checked asks Google
// nothing: the check on record still judges the URL the link points at.
func TestReEnablingALinkWithACheckedDestinationStartsNoCheck(t *testing.T) {
	f, checker := scanningFixture(t)
	destination := uniqueDestination("ok")
	checker.pass(destination)
	editor := f.members[authz.RoleEditor]

	created := f.createLink(t, "pause", destination)
	eventuallyChecked(t, f.pool, created.ID, destination)
	path := "/v1/links/" + created.ID.String()
	for _, state := range []string{"disabled", "active"} {
		rec := f.do(t, editor, http.MethodPatch, path, map[string]any{"state": state})
		require.Equal(t, http.StatusOK, rec.Code, "state %q, body: %s", state, rec.Body.String())
	}

	require.Never(t, func() bool { return checker.callCount() > 1 },
		300*time.Millisecond, 20*time.Millisecond)
}

// The flag belonged to the old destination; the new one starts active and is
// checked at once, not left for the next sweep.
func TestANewDestinationForAFlaggedLinkIsCheckedRightAway(t *testing.T) {
	f, checker := scanningFixture(t)
	first, second := uniqueDestination("phish"), uniqueDestination("phish")
	checker.flag(first, "MALWARE")
	checker.flag(second, "SOCIAL_ENGINEERING")

	created := f.createLink(t, "weiter", first)
	eventuallyState(t, f.pool, created.ID, "flagged")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": second})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "active", decode[linkBody](t, rec).State)

	eventuallyChecked(t, f.pool, created.ID, second)
	eventuallyState(t, f.pool, created.ID, "flagged")
}

func flagLink(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `update link set state = 'flagged' where id = $1`, id)
	require.NoError(t, err)
}

// Only Google lifts a flag. Disabling is refused too: disable-then-enable would
// otherwise be a way around the block.
func TestTheStateOfAFlaggedLinkIsNotTheCallersToChange(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "gesperrt", "https://example.org/gesperrt")
	flagLink(t, f.pool, created.ID)

	for _, state := range []string{"active", "disabled"} {
		rec := f.do(t, f.members[authz.RoleOwner], http.MethodPatch, "/v1/links/"+created.ID.String(),
			map[string]any{"state": state})
		require.Equal(t, http.StatusConflict, rec.Code, "state %q, body: %s", state, rec.Body.String())

		problem := decode[problemBody](t, rec)
		require.Len(t, problem.Errors, 1)
		require.Equal(t, "body.state", problem.Errors[0].Location)
		require.Equal(t, "flagged", problem.Errors[0].Value)
	}
	require.Equal(t, "flagged", linkState(t, f.pool, created.ID))

	// A new destination is the one way out, but not with a state beside it:
	// the refusal comes first, so neither field moves.
	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/anderswo", "state": "active"})
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	var destination, state string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select destination_url, state from link where id = $1`, created.ID).Scan(&destination, &state))
	require.Equal(t, "https://example.org/gesperrt", destination)
	require.Equal(t, "flagged", state)
}

// The flag belonged to the old URL, so the new one starts active and is
// checked on its own; the confirmation for the old URL must not survive it.
func TestANewDestinationLiftsTheFlagItsOldOneEarned(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "neuesziel", "https://example.org/alt")
	flagLink(t, f.pool, created.ID)
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, created.ID.String(), []string{"MALWARE"}, 10*time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "active", decode[linkBody](t, rec).State)

	entries := auditRows(t, f.pool, created.ID, "link.updated")
	require.Len(t, entries, 1)
	var metadata struct {
		Changed []string          `json:"changed"`
		State   map[string]string `json:"state"`
	}
	require.NoError(t, json.Unmarshal([]byte(entries[0].metadata), &metadata))
	require.ElementsMatch(t, []string{"destination_url", "state"}, metadata.Changed)
	require.Equal(t, map[string]string{"from": "flagged", "to": "active"}, metadata.State)

	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, created.ID))
}

// isDue reports whether the sweep would pick the link up now. The due list is
// instance-wide, so it is read the way internal/db's tests read it: one
// REPEATABLE READ snapshot, a limit that is the count of everything due in
// that snapshot, and only this link's id looked for.
func isDue(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	q, now := db.New(tx), time.Now()
	count, err := q.CountDueLinksForScan(ctx, now)
	require.NoError(t, err)
	due, err := q.ListDueLinksForScan(ctx, db.ListDueLinksForScanParams{Now: now, BatchLimit: int32(count)})
	require.NoError(t, err)
	for _, row := range due {
		if row.ID == id {
			return true
		}
	}
	return false
}

// A flag lifted by a new destination must not let the flagged URL back in
// unchecked. Scanner is nil, so every immediate check is lost: changing the
// destination back would otherwise leave the link active on the URL Google
// flagged, with a recent check on record for exactly that URL, and the sweep
// would not look at it for a day.
func TestChangingBackToAFlaggedDestinationLeavesTheLinkDue(t *testing.T) {
	f := newTenancyFixture(t)
	flaggedURL := "https://example.org/zurueck"
	created := f.createLink(t, "zurueck", flaggedURL)
	_, err := f.pool.Exec(context.Background(),
		`update link set state = 'flagged', scan_checked_at = now(), scan_destination = destination_url
		 where id = $1`, created.ID)
	require.NoError(t, err)

	for _, destination := range []string{"https://example.org/woanders", flaggedURL} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
			map[string]any{"destination_url": destination})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}

	require.Equal(t, "active", linkState(t, f.pool, created.ID))
	require.True(t, isDue(t, f.pool, created.ID),
		"the flagged URL is back, and nothing has checked it since the flag was lifted")
}

// No more lost flags. A transaction stands in for applyVerdict: it locks the
// row and flags it, and the PATCH starts while that is uncommitted. With the
// locked read, the PATCH waits and then reads the flag; with an unlocked one
// it reads "active" first and writes it back over the flag.
func TestUpdateLinkDoesNotOverwriteAFlagSetWhileItWaited(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "wettlauf", "https://example.org/wettlauf")

	scanner, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = scanner.Rollback(context.Background()) })
	var scannerPID int32
	require.NoError(t, scanner.QueryRow(ctx, `select pg_backend_pid()`).Scan(&scannerPID))
	_, err = scanner.Exec(ctx, `select id from link where id = $1 for update`, created.ID)
	require.NoError(t, err)
	_, err = scanner.Exec(ctx, `update link set state = 'flagged' where id = $1`, created.ID)
	require.NoError(t, err)

	// Built here, served in the goroutine: require must not run off the test's
	// own goroutine.
	editor := f.members[authz.RoleEditor]
	req := httptest.NewRequest(http.MethodPatch, "/v1/links/"+created.ID.String(),
		strings.NewReader(`{"redirect_type": 301}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signMeToken(t, f.key, editor.id.String(), editor.email))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		done <- rec
	}()

	// Wait until the PATCH is blocked behind this transaction — on its own
	// locked read (correct) or on its write (the bug). Either way it has
	// started. Only a backend this one blocks counts, so no other test's lock
	// wait can stand in for it.
	require.Eventually(t, func() bool {
		var waiting int
		err := f.pool.QueryRow(ctx,
			`select count(*) from pg_stat_activity where pg_blocking_pids(pid) @> array[$1::int]`,
			scannerPID,
		).Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, scanner.Commit(ctx))
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the PATCH never finished after the scanner committed")
	}
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "flagged", linkState(t, f.pool, created.ID),
		"the PATCH wrote back the state it had read before the flag landed")
}

// A side effect of the 409 above: updateLink now passes a shaped error from
// inside its transaction through. Before, a foreign folder answered 500.
func TestUpdateLinkAnswers422ForAnotherTeamsFolder(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createFolder(t, "Fremd")
	created := f.createLink(t, "ordner", "https://example.org/ordner")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"folder_id": foreign.ID.String()})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}
