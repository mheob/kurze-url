package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

const (
	meTestKID      = "me-test-key-1"
	meTestIssuer   = "https://project.supabase.co/auth/v1"
	meTestAudience = "authenticated"
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

// startAuthenticatedJWKSServer serves a JWKS document for a freshly generated
// P-256 key and returns the private half plus the server URL, so tests can
// build a real *auth.Verifier and sign genuine ES256 bearer tokens against
// it — exercising the actual authenticated code path through RegisterV1,
// not just the nil-Verifier branch that TestMeRejectsA*BearerToken above
// exercise.
func startAuthenticatedJWKSServer(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	uncompressed, err := key.PublicKey.Bytes()
	require.NoError(t, err)
	x, y := uncompressed[1:33], uncompressed[33:65]

	document := map[string]any{"keys": []map[string]string{{
		"kty": "EC",
		"crv": "P-256",
		"kid": meTestKID,
		"alg": "ES256",
		"use": "sig",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
	}}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)

	return key, server.URL
}

// newFixtureWithVerifier builds the usual fixture, then wires a real
// *auth.Verifier onto its Deps (newFixture itself leaves Verifier nil). It
// returns the signing key alongside so callers can produce genuinely valid
// bearer tokens.
func newFixtureWithVerifier(t *testing.T) (*fixture, *ecdsa.PrivateKey) {
	t.Helper()
	f := newFixture(t)

	key, jwksURL := startAuthenticatedJWKSServer(t)
	verifier, err := auth.NewVerifier(context.Background(), jwksURL, meTestIssuer, meTestAudience)
	require.NoError(t, err)
	f.deps.Verifier = verifier

	return f, key
}

func signMeToken(t *testing.T, key *ecdsa.PrivateKey, userID, email string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   userID,
		"iss":   meTestIssuer,
		"aud":   meTestAudience,
		"email": email,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = meTestKID
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func TestMeRejectsAMissingBearerTokenWhenAVerifierIsConfigured(t *testing.T) {
	f, _ := newFixtureWithVerifier(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMeRejectsAGarbageBearerTokenWhenAVerifierIsConfigured(t *testing.T) {
	f, _ := newFixtureWithVerifier(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestMeReturnsTheVerifiedClaimsForAValidBearerToken is the only test that
// exercises the full authenticated path end to end: a real JWKS-backed
// *auth.Verifier, a genuinely ES256-signed token, and the response body
// RegisterV1's handler produces from the resulting auth.Claims.
func TestMeReturnsTheVerifiedClaimsForAValidBearerToken(t *testing.T) {
	f, key := newFixtureWithVerifier(t)
	userID := uuid.NewString()

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+signMeToken(t, key, userID, "member@verein.test"))
	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), userID)
	require.Contains(t, rec.Body.String(), "member@verein.test")
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
