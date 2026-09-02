package auth_test

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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

const (
	testKID      = "test-key-1"
	testIssuer   = "https://project.supabase.co/auth/v1"
	testAudience = "authenticated"
)

// jwksServer serves a JWKS document for a freshly generated P-256 key and
// returns the private half for signing test tokens.
func jwksServer(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	return startJWKSServer(t, true)
}

// jwksServerWithoutAlg is like jwksServer, but the served JWK omits the
// optional "alg" member. MicahParks/keyfunc only refuses to hand back a key
// when the token header's alg disagrees with the JWK's declared one (see
// keyfunc's KeyfuncCtx) — with alg present, that check alone would already
// block a cross-algorithm token before jwt.WithValidMethods ever gets a say,
// which is exactly what makes jwksServer unsuitable for isolating the pin.
// Omitting alg removes that ambiguity.
func jwksServerWithoutAlg(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	return startJWKSServer(t, false)
}

func startJWKSServer(t *testing.T, includeAlg bool) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Bytes() returns the SEC1 uncompressed point encoding (0x04 || X || Y,
	// each coordinate left-padded to the P-256 field size) — the non-deprecated
	// replacement for reading key.PublicKey.X/Y directly (see PublicKey.X's
	// doc comment). Slicing it apart gives the same left-padded 32-byte
	// coordinates that FillBytes(make([]byte, 32)) would have produced.
	uncompressed, err := key.PublicKey.Bytes()
	require.NoError(t, err)
	require.Len(t, uncompressed, 65, "P-256 uncompressed point must be 1+32+32 bytes")
	x, y := uncompressed[1:33], uncompressed[33:65]

	jwk := map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"kid": testKID,
		"use": "sig",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
	}
	if includeAlg {
		jwk["alg"] = "ES256"
	}
	document := map[string]any{"keys": []map[string]string{jwk}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)

	return key, server.URL
}

func sign(t *testing.T, key *ecdsa.PrivateKey, claims jwt.MapClaims, alg jwt.SigningMethod) string {
	t.Helper()
	token := jwt.NewWithClaims(alg, claims)
	token.Header["kid"] = testKID
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func validClaims(subject string) jwt.MapClaims {
	return jwt.MapClaims{
		"sub":   subject,
		"iss":   testIssuer,
		"aud":   testAudience,
		"email": "member@verein.test",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
}

func newVerifier(t *testing.T, jwksURL string) *auth.Verifier {
	t.Helper()
	v, err := auth.NewVerifier(context.Background(), jwksURL, testIssuer, testAudience)
	require.NoError(t, err)
	return v
}

// alwaysValidSigningMethod is a jwt.SigningMethod whose Verify never fails,
// regardless of key or signature — the opposite of every signing method
// golang-jwt ships, each of which type-checks its key before using it (HMAC
// requires []byte, ECDSA requires *ecdsa.PublicKey, "none" requires the
// UnsafeAllowNoneSignatureType sentinel). That built-in type-checking is
// exactly why a real HS256-or-none downgrade attempt is rejected even with
// jwt.WithValidMethods removed: MicahParks/keyfunc returns a concrete
// *ecdsa.PublicKey for our EC JWK regardless of the token's claimed alg, and
// golang-jwt's own HMAC.Verify/none.Verify then refuse that key type on their
// own, independent of any allow-list. A test built from a real algorithm
// therefore cannot tell whether the allow-list or golang-jwt's own type
// safety did the rejecting. This synthetic method has no such check, so only
// jwt.WithValidMethods can stop it.
type alwaysValidSigningMethod struct{}

func (alwaysValidSigningMethod) Verify(string, []byte, any) error { return nil }
func (alwaysValidSigningMethod) Sign(string, any) ([]byte, error) { return []byte("sig"), nil }
func (alwaysValidSigningMethod) Alg() string                      { return "test-always-valid" }

func init() {
	jwt.RegisterSigningMethod("test-always-valid", func() jwt.SigningMethod {
		return alwaysValidSigningMethod{}
	})
}

func TestVerifyAcceptsAValidES256Token(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)
	subject := uuid.NewString()

	claims, err := verifier.Verify(context.Background(),
		sign(t, key, validClaims(subject), jwt.SigningMethodES256))

	require.NoError(t, err)
	require.Equal(t, subject, claims.UserID.String())
	require.Equal(t, "member@verein.test", claims.Email)
}

func TestVerifyRejectsAnExpiredToken(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["exp"] = time.Now().Add(-time.Minute).Unix()

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheWrongIssuer(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["iss"] = "https://attacker.example/auth/v1"

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheWrongAudience(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["aud"] = "someone-else"

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsATokenSignedByAnotherKey(t *testing.T) {
	_, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	attackerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(),
		sign(t, attackerKey, validClaims(uuid.NewString()), jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheNoneAlgorithm(t *testing.T) {
	_, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(uuid.NewString()))
	token.Header["kid"] = testKID
	raw, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), raw)

	require.Error(t, err, "alg=none must never be accepted")
}

func TestVerifyRejectsANonUUIDSubject(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims("not-a-uuid")

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

// TestVerifyRejectsAnAlgorithmOutsideTheAllowList isolates the
// jwt.WithValidMethods pin itself, independent of golang-jwt's own
// per-algorithm type safety and of MicahParks/keyfunc's alg-mismatch check
// (see jwksServerWithoutAlg and alwaysValidSigningMethod). With this fixture,
// removing the pin from Verify would make this token accepted, not rejected
// — see the RED evidence recorded in the fix-round report.
func TestVerifyRejectsAnAlgorithmOutsideTheAllowList(t *testing.T) {
	_, jwksURL := jwksServerWithoutAlg(t)
	verifier := newVerifier(t, jwksURL)

	token := jwt.NewWithClaims(alwaysValidSigningMethod{}, validClaims(uuid.NewString()))
	token.Header["kid"] = testKID
	raw, err := token.SignedString([]byte("irrelevant: this method's Verify never fails"))
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), raw)

	require.Error(t, err,
		"jwt.WithValidMethods must reject an algorithm outside {ES256} even when "+
			"that algorithm's own Verify never fails")
}

func TestNewVerifierRejectsAnEmptyIssuer(t *testing.T) {
	_, jwksURL := jwksServer(t)

	_, err := auth.NewVerifier(context.Background(), jwksURL, "", testAudience)

	require.Error(t, err, "a misconfigured deployment must fail to start, not accept tokens from any issuer")
}

func TestNewVerifierRejectsAnEmptyAudience(t *testing.T) {
	_, jwksURL := jwksServer(t)

	_, err := auth.NewVerifier(context.Background(), jwksURL, testIssuer, "")

	require.Error(t, err, "a misconfigured deployment must fail to start, not accept tokens for any audience")
}
