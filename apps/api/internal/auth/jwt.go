package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// keySetMaxAge is how old the cached key set may grow before Verify
	// refreshes it. It bounds how long a key Supabase has revoked keeps
	// verifying tokens on one instance.
	keySetMaxAge = time.Hour

	// refreshGap is the least time between two refresh attempts on one
	// instance, whatever prompted them. It keeps an unreachable Supabase from
	// costing every request a timeout, and tokens carrying made-up key IDs
	// from turning into one request to Supabase each.
	refreshGap = time.Minute

	// refreshTimeout bounds one fetch of the key set. A refresh runs inside a
	// request, so this is latency one caller may pay once an hour.
	refreshTimeout = 3 * time.Second
)

// Claims is the part of a Supabase access token this API acts on.
type Claims struct {
	UserID uuid.UUID
	Email  string
}

// Verifier validates Supabase-issued access tokens against the project's
// published JWKS. The key set is cached, so a valid request usually costs no
// network call; the backend holds no signing secret at all.
//
// The cache is refreshed inside Verify, by the request that finds it stale or
// meets a key ID it does not know, and never by a background goroutine.
// Vercel freezes a Go function between requests: a refresh a timer started
// stayed frozen mid-request until its deadline had passed and failed on the
// next thaw, about a dozen times a day, so the hourly refresh rarely landed
// and a revoked key stayed trusted for as long as the instance lived.
type Verifier struct {
	jwksURL  string
	issuer   string
	audience string
	log      *slog.Logger
	storage  jwkset.Storage
	keyfunc  keyfunc.Keyfunc
	now      func() time.Time

	mu sync.Mutex
	// refreshedAt is when the cached key set was last fetched successfully;
	// zero while none has been.
	refreshedAt time.Time
	// attemptedAt is when the last refresh started, whatever its outcome.
	attemptedAt time.Time
}

// NewVerifier fetches the key set at jwksURL once before returning. A failed
// fetch is logged rather than returned, as the library this replaced did: the
// first request that needs a key tries again.
func NewVerifier(ctx context.Context, log *slog.Logger, jwksURL, issuer, audience string) (*Verifier, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: jwks url is required")
	}
	if issuer == "" {
		return nil, fmt.Errorf("auth: issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("auth: audience is required")
	}

	storage := jwkset.NewMemoryStorage()
	k, err := keyfunc.New(keyfunc.Options{Storage: storage})
	if err != nil {
		return nil, fmt.Errorf("auth: create jwks cache: %w", err)
	}

	v := &Verifier{
		jwksURL:  jwksURL,
		issuer:   issuer,
		audience: audience,
		log:      log,
		storage:  storage,
		keyfunc:  k,
		now:      time.Now,
	}
	if v.claimRefresh(false) {
		v.refresh(ctx)
	}
	return v, nil
}

// claimRefresh reports whether the caller should refresh the key set now,
// and if so records the attempt, so that concurrent callers do not refresh
// too. With onlyIfStale set it declines while the cached set is younger than
// keySetMaxAge; either way it declines within refreshGap of the last attempt.
func (v *Verifier) claimRefresh(onlyIfStale bool) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if now.Sub(v.attemptedAt) < refreshGap {
		return false
	}
	if onlyIfStale && now.Sub(v.refreshedAt) < keySetMaxAge {
		return false
	}
	v.attemptedAt = now
	return true
}

// refresh fetches the key set and replaces the cached one with it, and
// reports whether that worked. A failure leaves the cached set in place.
//
// The fetch is detached from ctx's cancellation: the set it brings in serves
// every later request, so a caller hanging up must not abandon it.
func (v *Verifier) refresh(ctx context.Context) bool {
	// With RefreshInterval unset, NewStorageFromHTTP starts no goroutine: it
	// fetches once, writes the keys into Storage, and returns the error.
	_, err := jwkset.NewStorageFromHTTP(v.jwksURL, jwkset.HTTPClientStorageOptions{
		Ctx:         context.WithoutCancel(ctx),
		HTTPTimeout: refreshTimeout,
		Storage:     v.storage,
	})

	v.mu.Lock()
	cached := !v.refreshedAt.IsZero()
	if err == nil {
		v.refreshedAt = v.now()
	}
	v.mu.Unlock()

	switch {
	case err == nil:
		return true
	case cached:
		// The cached keys still verify, so nobody is locked out yet; the next
		// attempt follows refreshGap later.
		v.log.WarnContext(ctx, "jwks refresh failed; keeping the cached key set", "error", err)
	default:
		v.log.ErrorContext(ctx, "jwks refresh failed and no key set is cached; every bearer token is refused",
			"error", err)
	}
	return false
}

// keyFor looks up the key a token names, and refreshes the key set once when
// the cache does not know it: a token signed with a key Supabase has just
// rotated in reaches this instance before the hourly refresh does.
func (v *Verifier) keyFor(ctx context.Context) jwt.Keyfunc {
	lookup := v.keyfunc.KeyfuncCtx(ctx)
	return func(token *jwt.Token) (any, error) {
		key, err := lookup(token)
		if !errors.Is(err, jwkset.ErrKeyNotFound) {
			return key, err
		}
		if !v.claimRefresh(false) || !v.refresh(ctx) {
			return nil, err
		}
		return lookup(token)
	}
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

	if v.claimRefresh(true) {
		v.refresh(ctx)
	}

	claims := &supabaseClaims{}

	token, err := jwt.ParseWithClaims(rawToken, claims, v.keyFor(ctx), options...)
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

// claimsKey is the context key the verified claims travel under. It lives in
// this package rather than internal/api because internal/authz needs to read
// the caller's identity and must not import the HTTP layer.
type claimsKey struct{}

// WithClaims returns a context carrying the verified claims.
func WithClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// ClaimsFromContext returns the claims a bearer-authenticated request was
// verified with.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(Claims)
	return claims, ok
}
