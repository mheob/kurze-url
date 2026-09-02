package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

// redirectRouter mounts the handler the way the real router does, so chi's
// {slug} URL parameter resolves.
func redirectRouter(f *fixture) http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}", f.deps.HandleRedirect)
	return r
}

func get(t *testing.T, f *fixture, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = f.hostname
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/141.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	redirectRouter(f).ServeHTTP(rec, req)
	return rec
}

func TestRedirectResolvesFromPostgresOnACacheMissAndPopulatesTheCache(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a 302 must not be cached, or destination changes stop taking effect")

	cached, err := f.deps.Cache.LookupForRedirect(context.Background(),
		link.CacheKey(f.hostname, "hello"), "someone-else", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, cached.Found, "the miss must have populated the cache")
	require.Equal(t, "https://example.org/hello", cached.Link.DestinationURL)
}

func TestRedirectServesFromTheCacheOnTheSecondRequest(t *testing.T) {
	f := newFixture(t)

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	// Change the row behind the cache's back. A cache hit must not see it.
	_, err := f.pool.Exec(context.Background(),
		`update link set destination_url = 'https://example.org/changed' where id = $1`, f.linkID)
	require.NoError(t, err)

	rec := get(t, f, "/hello", nil)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"),
		"the second request must be served from cache")
}

func TestRedirectHonoursThePerLinkRedirectType(t *testing.T) {
	f := newFixture(t, withRedirectType(301))

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusMovedPermanently, rec.Code)
}

func TestRedirectReturns404AndNegativelyCachesAnUnknownSlug(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/nope", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)

	cached, err := f.deps.Cache.LookupForRedirect(context.Background(),
		link.CacheKey(f.hostname, "nope"), "v", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, cached.NegativelyCached,
		"an unknown slug must be negatively cached so a scanner cannot hammer Postgres")
}

func TestRedirectRefusesAnUnverifiedDomain(t *testing.T) {
	f := newFixture(t, unverifiedDomain())

	require.Equal(t, http.StatusNotFound, get(t, f, "/hello", nil).Code,
		"an unverified domain must not serve links — the team may not own the hostname")
}

func TestRedirectRefusesLinksThatAreNotActive(t *testing.T) {
	for _, tc := range []struct {
		state  string
		status int
	}{
		{"disabled", http.StatusGone},
		{"expired", http.StatusGone},
		{"flagged", http.StatusForbidden},
	} {
		t.Run(tc.state, func(t *testing.T) {
			f := newFixture(t, withState(tc.state))
			require.Equal(t, tc.status, get(t, f, "/hello", nil).Code)
		})
	}
}

func TestRedirectRefusesALinkPastItsExpiry(t *testing.T) {
	f := newFixture(t, withExpiry(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))

	require.Equal(t, http.StatusGone, get(t, f, "/hello", nil).Code)
}

func TestRedirectAllowsALinkBeforeItsExpiry(t *testing.T) {
	f := newFixture(t, withExpiry(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)))

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
}

func TestRedirectShowsThePasswordPromptInsteadOfRedirecting(t *testing.T) {
	f := newFixture(t, withPasswordHash("$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"))

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), `action="/hello/verify"`)

	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.Empty(t, *f.rows, "opening the interstitial is not a click")
}

func TestRedirectRecordsTheClickWithTheUniqueVisitorFlag(t *testing.T) {
	f := newFixture(t)

	get(t, f, "/hello", nil)
	get(t, f, "/hello", nil)

	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	var total *struct {
		clicks int64
		unique int64
	}
	for _, row := range *f.rows {
		if row.DimType == "total" {
			total = &struct {
				clicks int64
				unique int64
			}{row.Clicks, row.Unique}
		}
	}

	require.NotNil(t, total)
	require.EqualValues(t, 2, total.clicks)
	require.EqualValues(t, 1, total.unique, "the same visitor twice in a day counts once")
}

func TestRedirectRecordsNothingWhenAnalyticsAreDisabledForTheLink(t *testing.T) {
	f := newFixture(t, analyticsDisabled())

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	require.Empty(t, *f.rows, "a link with analytics disabled must record no click at all")
}

func TestRedirectRateLimitsPerClientIP(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RedirectRateLimitPerMin = 2

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	rec := get(t, f, "/hello", nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	other := httptest.NewRequest(http.MethodGet, "/hello", nil)
	other.Host = f.hostname
	other.Header.Set("X-Forwarded-For", "198.51.100.4")
	otherRec := httptest.NewRecorder()
	redirectRouter(f).ServeHTTP(otherRec, other)
	require.Equal(t, http.StatusFound, otherRec.Code, "a different IP has its own budget")
}

func TestRedirectRespondsInGermanWhenPreferred(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/nope", map[string]string{"Accept-Language": "de-DE,de;q=0.9"})

	require.Contains(t, rec.Body.String(), "nicht gefunden")
}
