package api_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// flaggedFixture is newFixture with a flagged link and a fake checker.
func flaggedFixture(t *testing.T, opts ...func(*linkOptions)) (*fixture, *fakeChecker) {
	t.Helper()
	f := newFixture(t, append([]func(*linkOptions){withState("flagged")}, opts...)...)
	checker := newFakeChecker()
	f.deps.Scanner = checker
	return f, checker
}

func confirm(t *testing.T, f *fixture, ttl time.Duration, threats ...string) {
	t.Helper()
	require.NoError(t, f.deps.Cache.ConfirmThreats(context.Background(), f.linkID.String(), threats, ttl))
}

func TestAFlaggedLinkWithAFreshConfirmationShowsTheBlockPage(t *testing.T) {
	f, checker := flaggedFixture(t)
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Suspected phishing site")
	require.Contains(t, body, "Advisory provided by Google")
	require.Contains(t, body, "https://developers.google.com/search/docs/monitor-debug/security/social-engineering")
	require.Zero(t, checker.callCount(), "a fresh confirmation needs no second opinion")
}

func TestAFlaggedLinkWithoutAConfirmationAsksGoogleAgain(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Possibly harmful software")
	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 3*time.Minute)
	require.LessOrEqual(t, ttl, 4*time.Minute)
	require.Equal(t, 1, checker.callCount())

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Equal(t, 1, checker.callCount(), "the second redirect is answered from the confirmation")
}

func TestAConfirmationNeverOutlivesThirtyMinutes(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.answer(fixtureDestination, scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 2 * time.Hour})

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)

	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 28*time.Minute)
	require.LessOrEqual(t, ttl, 29*time.Minute)
}

// cacheDuration missing, zero or unreadable all arrive as ValidFor 0: the
// page may be shown on that fresh answer, but nothing may vouch for it later.
func TestAVerdictWithoutAUsableValidityBlocksButConfirmsNothing(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.answer(fixtureDestination, scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 0})

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Equal(t, 2, checker.callCount())
}

func TestAFlaggedLinkGoogleNoLongerReportsIsForwardedAndUnflagged(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.pass(fixtureDestination)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, fixtureDestination, rec.Header().Get("Location"))
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.NotEmpty(t, *f.rows, "a forwarded click is a click, exactly as for an active link")
	eventuallyState(t, f.pool, f.linkID, "active")
}

// A failed check is a Warn, which never reaches Sentry: Google being down
// changes nothing about the link, and a flagged link being visited is not
// news.
func TestAFlaggedLinkAnswers503WhenGoogleFails(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.fail(errors.New("connection reset"))
	logs := captureLogs(f)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "300", rec.Header().Get("Retry-After"))
	body := rec.Body.String()
	require.Contains(t, body, "temporarily unavailable")
	require.NotContains(t, body, "Google", "without a fresh confirmation the page may not call it unsafe")
	require.Contains(t, logs.String(), `level=WARN msg="safe browsing check failed"`)
	require.NotContains(t, logs.String(), "level=ERROR")
}

func TestAFlaggedLinkAnswers503WithoutAScanner(t *testing.T) {
	f, _ := flaggedFixture(t)
	f.deps.Scanner = nil
	logs := captureLogs(f)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "300", rec.Header().Get("Retry-After"))
	require.Contains(t, logs.String(), "safe browsing scanning is off")
	require.NotContains(t, logs.String(), "level=ERROR")
}

// singleflight re-raises a panic from DoChan on a goroutine nothing can
// recover, which would take the whole redirect surface down with it.
func TestAPanickingRecheckAnswers503AndIsReported(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.onCheck(func(context.Context) { panic("checker exploded") })
	logs := captureLogs(f)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, logs.String(), `level=ERROR msg="safe browsing re-check panicked"`)
}

// The order is unchanged: expiry before state, so an expired flagged link is
// simply expired, and nobody asks Google about it.
func TestAnExpiredFlaggedLinkIsExpiredWithoutAskingGoogle(t *testing.T) {
	f, checker := flaggedFixture(t, withExpiry(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))
	checker.flag(fixtureDestination, "MALWARE")

	require.Equal(t, http.StatusGone, get(t, f, "/hello", nil).Code)
	require.Zero(t, checker.callCount())
}

// Golden rule 2's one exception is bounded: two seconds, not the client's
// five, and not however long Google takes.
func TestTheRecheckWaitsTwoSecondsAtMost(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")
	checker.slow(10 * time.Second)

	start := time.Now()
	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Less(t, time.Since(start), 3*time.Second)
}

func TestConcurrentRedirectsShareOneRecheck(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")
	// Long enough that all five requests reach the re-check while the first
	// one's call is still in flight.
	checker.slow(time.Second)

	codes := make([]int, 5)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = get(t, f, "/hello", nil).Code
		}()
	}
	wg.Wait()

	for _, code := range codes {
		require.Equal(t, http.StatusForbidden, code)
	}
	require.Equal(t, 1, checker.callCount(),
		"one instance asks Google once per flagged link, however many visitors arrive at once")
}

// Review focus: Upstash refusing every command. A failed GET reads as "no
// confirmation" and re-checks; a failed SET does not turn a confirmed block
// into an error page.
func TestARedisFailureDuringAFlaggedRedirectFallsBackToARecheck(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "SOCIAL_ENGINEERING")
	require.NoError(t, f.deps.Cache.Close())

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "Suspected phishing site")
	require.Equal(t, 1, checker.callCount())
}

// Golden rule 2: an active link calls nothing new, on either path.
func TestAnActiveLinkNeverAsksGoogle(t *testing.T) {
	checker := newFakeChecker()
	checker.flag(fixtureDestination, "MALWARE")

	f := newFixture(t)
	f.deps.Scanner = checker
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	hash, err := auth.HashPassword("Kartoffelsalat!7")
	require.NoError(t, err)
	protected := newFixture(t, withPasswordHash(hash))
	protected.deps.Scanner = checker
	require.Equal(t, http.StatusFound,
		postPassword(t, protected, "hello", "Kartoffelsalat!7", "203.0.113.5").Code)

	require.Zero(t, checker.callCount())
}

// A confirmed block comes before the password prompt: the visitor is not
// asked for a password to a link that will not be followed.
func TestAConfirmedFlaggedPasswordLinkShowsTheBlockPageNotThePrompt(t *testing.T) {
	f, checker := flaggedFixture(t, withPasswordHash("$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"))
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Suspected phishing site")
	require.Contains(t, body, "Advisory provided by Google")
	require.NotContains(t, body, `type="password"`)
	require.NotContains(t, body, `action="/hello/verify"`)
	require.Zero(t, checker.callCount())
}

// A cached entry can still say flagged after Postgres says active: the flag
// was lifted by one instance while another re-cached the link. The clean
// re-check then finds nothing to change, and without an eviction every burst
// of visitors would ask Google again for as long as the entry lives.
func TestACleanRecheckEvictsAStaleFlaggedCacheEntry(t *testing.T) {
	f := newFixture(t)
	checker := newFakeChecker()
	checker.pass(fixtureDestination)
	f.deps.Scanner = checker
	ctx := context.Background()
	cacheKey := link.CacheKey(f.hostname, "hello")
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: f.linkID, TeamID: fixtureTeamID(t, f), DestinationURL: fixtureDestination,
		RedirectType: http.StatusFound, State: "flagged", AnalyticsEnabled: true,
	}, time.Hour))

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	require.Equal(t, 1, checker.callCount(), "the stale entry must have sent the redirect through the re-check")
	require.Eventually(t, func() bool {
		n, err := f.deps.Cache.Raw().Exists(ctx, f.deps.Cache.Key(cacheKey)).Result()
		return err == nil && n == 0
	}, 5*time.Second, 20*time.Millisecond, "the stale flagged entry was never evicted")
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
}

// The state check still comes before the password, so a blocked link is never
// an oracle for its password.
func TestVerifyShowsTheBlockPageBeforeAskingForThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f, checker := flaggedFixture(t, withPasswordHash(hash))
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := postPassword(t, f, "hello", "hunter2", "203.0.113.1")

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Advisory provided by Google")
	require.Zero(t, checker.callCount())
}

func TestVerifyOnAFlaggedLinkGoogleClearsChecksThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f, checker := flaggedFixture(t, withPasswordHash(hash))
	checker.pass(fixtureDestination)

	require.Equal(t, http.StatusFound, postPassword(t, f, "hello", "hunter2", "203.0.113.1").Code)
	eventuallyState(t, f.pool, f.linkID, "active")
}

// The German attribution is Google's own wording, from the German version of
// its usage page.
func TestTheBlockPageIsGerman(t *testing.T) {
	f, _ := flaggedFixture(t)
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := get(t, f, "/hello", map[string]string{"Accept-Language": "de-DE,de;q=0.9"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Mutmaßliche Phishing-Seite")
	require.Contains(t, rec.Body.String(), "Von Google bereitgestellte Hinweise")
}
