package auth_test

import (
	"context"
	"crypto/ecdsa"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

// These tests pin where the key set is refreshed: inside Verify, never on a
// timer. Vercel freezes a Go function between requests, so a refresh started
// by a background goroutine sat frozen mid-request until its deadline had
// passed, and failed on the next thaw (Sentry KURZE-URL-API-2).

// clock is a hand-wound clock for a Verifier. It starts at the real time, so
// the key set NewVerifier fetched is fresh, and moves only when told to.
type clock struct{ now time.Time }

func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newClockedVerifier(t *testing.T, jwksURL string, log *slog.Logger) (*auth.Verifier, *clock) {
	t.Helper()
	v, err := auth.NewVerifier(context.Background(), log, jwksURL, testIssuer, testAudience)
	require.NoError(t, err)
	c := &clock{now: time.Now()}
	auth.SetClockForTest(v, func() time.Time { return c.now })
	return v, c
}

func tokenFor(t *testing.T, key *ecdsa.PrivateKey, kid string) string {
	t.Helper()
	return signWithKID(t, key, kid, validClaims(uuid.NewString()), jwt.SigningMethodES256)
}

// recordingHandler keeps every record logged through it.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) levels() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()
	levels := make([]slog.Level, 0, len(h.records))
	for _, r := range h.records {
		levels = append(levels, r.Level)
	}
	return levels
}

func TestVerifyServesAFreshKeySetFromTheCache(t *testing.T) {
	key := newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, key, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)
	require.Equal(t, 1, server.fetches(), "NewVerifier fetches the key set before the first request")

	for range 3 {
		_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
		require.NoError(t, err)
	}
	c.advance(auth.KeySetMaxAgeForTest - time.Second)
	_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err)

	require.Equal(t, 1, server.fetches(), "a key set younger than an hour costs no request to Supabase")
}

func TestVerifyRefreshesTheKeySetOnceItIsAnHourOld(t *testing.T) {
	old, replacement := newSigningKey(t), newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, old, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)

	// Supabase revokes the old key and publishes a new one under the same key
	// ID, so only the age of the cached set can bring the change in.
	server.serve(publicJWK(t, replacement, testKID, true))
	_, err := v.Verify(context.Background(), tokenFor(t, old, testKID))
	require.NoError(t, err, "an hour has not passed, so the cached set still holds the old key")

	c.advance(auth.KeySetMaxAgeForTest)

	_, err = v.Verify(context.Background(), tokenFor(t, replacement, testKID))
	require.NoError(t, err)
	_, err = v.Verify(context.Background(), tokenFor(t, old, testKID))
	require.Error(t, err, "a key Supabase stopped publishing must stop verifying within the hour")
	require.Equal(t, 2, server.fetches())
}

func TestVerifyFetchesTheKeySetForAKeyIDItHasNotSeen(t *testing.T) {
	current, next := newSigningKey(t), newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, current, "current", true))
	v, c := newClockedVerifier(t, server.url, discardLog)

	server.serve(publicJWK(t, current, "current", true), publicJWK(t, next, "next", true))
	c.advance(auth.RefreshGapForTest)

	_, err := v.Verify(context.Background(), tokenFor(t, next, "next"))
	require.NoError(t, err, "a key rotation must not wait for the hourly refresh")
	require.Equal(t, 2, server.fetches())
}

func TestVerifyAsksForUnknownKeyIDsAtMostOncePerGap(t *testing.T) {
	key := newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, key, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)
	c.advance(auth.RefreshGapForTest)

	forged := tokenFor(t, newSigningKey(t), "made-up")

	_, err := v.Verify(context.Background(), forged)
	require.Error(t, err)
	require.Equal(t, 2, server.fetches())

	_, err = v.Verify(context.Background(), forged)
	require.Error(t, err)
	require.Equal(t, 2, server.fetches(), "made-up key IDs must not turn into one Supabase request each")

	c.advance(auth.RefreshGapForTest)
	_, err = v.Verify(context.Background(), forged)
	require.Error(t, err)
	require.Equal(t, 3, server.fetches())
}

func TestVerifyKeepsTheCachedKeySetWhenARefreshFails(t *testing.T) {
	key := newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, key, testKID, true))
	log := &recordingHandler{}
	v, c := newClockedVerifier(t, server.url, slog.New(log))

	server.fail()
	c.advance(auth.KeySetMaxAgeForTest)

	_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err, "a failed refresh must not lock anyone out while the cached keys still work")
	require.Equal(t, 2, server.fetches())
	require.Equal(t, []slog.Level{slog.LevelWarn}, log.levels(),
		"the cached keys still verify, so a failed refresh is no Sentry event")
}

func TestVerifyRetriesAFailedRefreshOnlyAfterTheGap(t *testing.T) {
	key := newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, key, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)

	server.fail()
	c.advance(auth.KeySetMaxAgeForTest)
	_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err)
	require.Equal(t, 2, server.fetches())

	_, err = v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err)
	require.Equal(t, 2, server.fetches(), "an unreachable Supabase must not cost every request a timeout")

	server.serve(publicJWK(t, key, testKID, true))
	c.advance(auth.RefreshGapForTest)
	_, err = v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err)
	require.Equal(t, 3, server.fetches())
}

func TestNewVerifierReportsAKeySetItCouldNotFetchAsAnError(t *testing.T) {
	server := newKeySetServer(t)
	server.fail()
	log := &recordingHandler{}

	v, c := newClockedVerifier(t, server.url, slog.New(log))

	require.Equal(t, []slog.Level{slog.LevelError}, log.levels(),
		"with no key set cached every bearer token is refused, which the maintainer must hear about")

	key := newSigningKey(t)
	server.serve(publicJWK(t, key, testKID, true))
	c.advance(auth.RefreshGapForTest)
	_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
	require.NoError(t, err, "the verifier recovers on its own once Supabase answers again")
}

func TestVerifyFinishesARefreshItsCallerCancelled(t *testing.T) {
	old, replacement := newSigningKey(t), newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, old, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)

	server.serve(publicJWK(t, replacement, testKID, true))
	c.advance(auth.KeySetMaxAgeForTest)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := v.Verify(ctx, tokenFor(t, replacement, testKID))

	require.NoError(t, err,
		"the refreshed set serves every later request, so a caller hanging up must not abandon it")
}

func TestVerifyRefreshesOnceForConcurrentCallers(t *testing.T) {
	key := newSigningKey(t)
	server := newKeySetServer(t)
	server.serve(publicJWK(t, key, testKID, true))
	v, c := newClockedVerifier(t, server.url, discardLog)
	c.advance(auth.KeySetMaxAgeForTest)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, err := v.Verify(context.Background(), tokenFor(t, key, testKID))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "callers that arrive during a refresh verify against the cached set")
	}
	require.Equal(t, 2, server.fetches())
}
