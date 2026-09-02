package auth

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the part of a Supabase access token this API acts on.
type Claims struct {
	UserID uuid.UUID
	Email  string
}

// Verifier validates Supabase-issued access tokens against the project's
// published JWKS. Keys are fetched and cached in the background, so a valid
// request costs no network call; the backend holds no signing secret at all.
type Verifier struct {
	keyfunc  keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewVerifier starts the JWKS cache for jwksURL. The context governs the
// background refresh, so it should live as long as the process.
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: jwks url is required")
	}
	if issuer == "" {
		return nil, fmt.Errorf("auth: issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("auth: audience is required")
	}

	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("auth: create jwks cache: %w", err)
	}

	return &Verifier{keyfunc: k, issuer: issuer, audience: audience}, nil
}

// supabaseClaims adds the one non-registered claim this API reads.
type supabaseClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

// Verify parses and validates a raw bearer token. Only ES256 is accepted:
// pinning the algorithm is what stops an attacker downgrading to alg=none or
// to HS256 keyed with the published public key.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	}

	claims := &supabaseClaims{}

	token, err := jwt.ParseWithClaims(rawToken, claims, v.keyfunc.KeyfuncCtx(ctx), options...)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify token: %w", err)
	}
	if !token.Valid {
		return Claims{}, fmt.Errorf("auth: token is not valid")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: subject is not a user id: %w", err)
	}

	return Claims{UserID: userID, Email: claims.Email}, nil
}
