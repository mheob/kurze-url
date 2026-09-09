package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

func verifyRouter(f *fixture) http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}", f.deps.HandleRedirect)
	r.Get("/{slug}/verify", f.deps.HandleVerifyForm)
	r.Post("/{slug}/verify", f.deps.HandleVerifySubmit)
	return r
}

func protectedFixture(t *testing.T, password string) *fixture {
	t.Helper()
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	return newFixture(t, withPasswordHash(hash))
}

func postPassword(t *testing.T, f *fixture, slug, password, ip string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/"+slug+"/verify", strings.NewReader(form.Encode()))
	req.Host = f.hostname
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	verifyRouter(f).ServeHTTP(rec, req)
	return rec
}

func TestVerifyFormRendersThePrompt(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	req := httptest.NewRequest(http.MethodGet, "/hello/verify", nil)
	req.Host = f.hostname
	rec := httptest.NewRecorder()
	verifyRouter(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `action="/hello/verify"`)
}

func TestVerifyFormIsRateLimitedPerIP(t *testing.T) {
	f := protectedFixture(t, "hunter2")
	f.deps.Config.RedirectRateLimitPerMin = 2

	get := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/hello/verify", nil)
		req.Host = f.hostname
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		verifyRouter(f).ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, get("203.0.113.1").Code)
	require.Equal(t, http.StatusOK, get("203.0.113.1").Code)

	rec := get("203.0.113.1")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	require.Equal(t, http.StatusOK, get("198.51.100.9").Code,
		"a different IP has its own budget")
}

func TestVerifySubmitRedirectsOnTheCorrectPassword(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "hunter2", "203.0.113.1")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"))
}

func TestVerifySubmitRecordsTheClickOnlyOnSuccess(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	require.Equal(t, http.StatusUnauthorized, postPassword(t, f, "hello", "wrong", "203.0.113.1").Code)
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.Empty(t, *f.rows, "a failed attempt is not a click")

	postPassword(t, f, "hello", "hunter2", "203.0.113.1")
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	var totals int64
	for _, row := range *f.rows {
		if row.DimType == "total" {
			totals += row.Clicks
		}
	}
	require.EqualValues(t, 1, totals)
}

func TestVerifySubmitRerendersWithAnErrorOnTheWrongPassword(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "nope", "203.0.113.1")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, strings.ToLower(rec.Body.String()), "incorrect")
}

func TestVerifySubmitNeverLeaksTheHash(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "nope", "203.0.113.1")

	require.NotContains(t, rec.Body.String(), "$argon2id$")
	require.NotContains(t, rec.Body.String(), "hunter2")
}

func TestVerifySubmitRateLimitsTightlyPerLinkAndIP(t *testing.T) {
	f := protectedFixture(t, "hunter2")
	f.deps.Config.PasswordRateLimitPerMin = 3

	for range 3 {
		require.Equal(t, http.StatusUnauthorized,
			postPassword(t, f, "hello", "wrong", "203.0.113.1").Code)
	}

	require.Equal(t, http.StatusTooManyRequests,
		postPassword(t, f, "hello", "wrong", "203.0.113.1").Code,
		"the fourth attempt from one IP must be blocked")

	require.Equal(t, http.StatusUnauthorized,
		postPassword(t, f, "hello", "wrong", "198.51.100.9").Code,
		"a different IP has its own budget")
}

func TestVerifiedPasswordProtectedLinkStillCountsAsUniqueVisitor(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	getInterstitial := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/hello", nil)
		req.Host = f.hostname
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		rec := httptest.NewRecorder()
		verifyRouter(f).ServeHTTP(rec, req)
		return rec
	}

	// Cache miss, then cache hit — both must leave the visitor's uniqueness
	// untouched, since neither is a click yet.
	require.Equal(t, http.StatusOK, getInterstitial().Code)
	require.Equal(t, http.StatusOK, getInterstitial().Code)

	rec := postPassword(t, f, "hello", "hunter2", "203.0.113.1")
	require.Equal(t, http.StatusFound, rec.Code)

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
	require.EqualValues(t, 1, total.clicks)
	require.EqualValues(t, 1, total.unique,
		"the interstitial views must not have consumed the visitor's uniqueness")
}

func TestVerifyOnAnUnprotectedLinkIsNotFound(t *testing.T) {
	f := newFixture(t)

	require.Equal(t, http.StatusNotFound,
		postPassword(t, f, "hello", "anything", "203.0.113.1").Code)
}

func TestVerifyOnAnInactiveLinkIsRefusedBeforeCheckingThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f := newFixture(t, withPasswordHash(hash), withState("flagged"))

	require.Equal(t, http.StatusForbidden,
		postPassword(t, f, "hello", "hunter2", "203.0.113.1").Code)
}

// TestVerifySubmitCapsFailuresPerLinkAcrossAddresses pins the axis the per-IP
// limit cannot cover. Every request here comes from a DIFFERENT address, so
// the existing per-link-per-IP limit lets all of them through; only the
// link-keyed failure counter can refuse the last one.
func TestVerifySubmitCapsFailuresPerLinkAcrossAddresses(t *testing.T) {
	f := protectedFixture(t, "Kartoffelsalat!7")
	f.deps.Config.PasswordFailureRateLimitPerHour = 2

	for i := range 2 {
		require.Equal(t, http.StatusUnauthorized,
			postPassword(t, f, "hello", "wrong", fmt.Sprintf("203.0.113.%d", i+1)).Code)
	}

	require.Equal(t, http.StatusTooManyRequests,
		postPassword(t, f, "hello", "wrong", "203.0.113.99").Code,
		"a third failure from a third address must hit the per-link cap")
}

// TestVerifySubmitDoesNotChargeACorrectPassword is the property the whole
// two-script split in internal/cache exists for. A link whose password a whole
// Verein has been given must never approach the failure cap, no matter how
// many people follow it.
func TestVerifySubmitDoesNotChargeACorrectPassword(t *testing.T) {
	f := protectedFixture(t, "Kartoffelsalat!7")
	f.deps.Config.PasswordFailureRateLimitPerHour = 2

	for i := range 5 {
		require.Equal(t, http.StatusFound,
			postPassword(t, f, "hello", "Kartoffelsalat!7", fmt.Sprintf("198.51.100.%d", i+1)).Code,
			"attempt %d must redirect", i+1)
	}
}
