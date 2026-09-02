package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func v1Router(f *fixture) http.Handler {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	f.deps.RegisterV1(humaAPI)
	return router
}

func TestMeRejectsAMissingBearerToken(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json",
		"errors must use Huma's RFC 9457 default, not a custom model")
}

func TestMeRejectsAGarbageBearerToken(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenAPIDocumentDeclaresTheBearerScheme(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "bearerAuth")
	require.Contains(t, rec.Body.String(), "3.1.")
}

func TestOpenAPIDocumentExcludesTheRedirectSurface(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	body := rec.Body.String()
	require.NotContains(t, body, "{slug}",
		"the public redirect surface is deliberately not part of the machine contract")
}

func TestHealthIsUnauthenticated(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}
