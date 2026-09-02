package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func requestTo(t *testing.T, handler http.Handler, host, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRouterServesV1OnlyOnTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	require.Equal(t, http.StatusOK, requestTo(t, handler, "api.test", "/v1/health").Code)
}

func TestRouterDoesNotExposeV1OnAShortLinkHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, f.hostname, "/v1/health")

	require.NotEqual(t, http.StatusOK, rec.Code,
		"a team's custom domain must not serve the JSON API")
}

func TestRouterServesRedirectsOnAShortLinkHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, f.hostname, "/hello")

	require.Equal(t, http.StatusFound, rec.Code)
}

func TestRouterDoesNotServeRedirectsOnTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, "api.test", "/hello")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRouterIgnoresThePortWhenMatchingTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "localhost"
	handler := api.NewRouter(f.deps)

	require.Equal(t, http.StatusOK, requestTo(t, handler, "localhost:8080", "/v1/health").Code)
}

func TestRouterServesHealthOnEveryHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	// The uptime monitor and Vercel's own checks hit whichever hostname they
	// are pointed at, so /health must answer on all of them.
	require.Equal(t, http.StatusOK, requestTo(t, handler, "api.test", "/health").Code)
	require.Equal(t, http.StatusOK, requestTo(t, handler, f.hostname, "/health").Code)
}
