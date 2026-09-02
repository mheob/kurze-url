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

	document := map[string]any{"keys": []map[string]string{{
		"kty": "EC",
		"crv": "P-256",
		"kid": testKID,
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
