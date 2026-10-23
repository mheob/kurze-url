package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// The CLI's HTTP client calls the session through this shape without
// importing it, so a drifting signature must fail here and not in Task 7.
var _ interface {
	AccessToken(ctx context.Context) (string, error)
	Refresh(ctx context.Context) (string, error)
} = (*Session)(nil)

// baseTime is the session's clock in these tests. A refresh answers
// "expires_in": 3600, and the session computes the new expiry from its own
// clock, so the expected value is exact.
var baseTime = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// newStore returns an in-memory store holding one pair that expires after
// the given time, counted from baseTime.
func newStore(t *testing.T, access, refresh string, expiresIn time.Duration) *credentials.Memory {
	t.Helper()
	store := credentials.NewMemory()
	require.NoError(t, store.Save(credentials.Tokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    baseTime.Add(expiresIn),
	}))
	return store
}

func newSession(srv *authServer, store credentials.Store) *Session {
	return &Session{
		Store: store,
		Endpoints: func(context.Context) (Endpoints, error) {
			return srv.endpoints(), nil
		},
		ClientID:       testClientID,
		PublishableKey: testAPIKey,
		HTTPClient:     srv.Client(),
		Now:            func() time.Time { return baseTime },
	}
}

// requireStored checks what the store holds without printing a token.
func requireStored(t *testing.T, store credentials.Store, access, refresh string, expiresAt time.Time) {
	t.Helper()
	got, err := store.Load()
	require.NoError(t, err)
	sameSecret(t, access, got.AccessToken, "the stored access token")
	sameSecret(t, refresh, got.RefreshToken, "the stored refresh token")
	require.True(t, expiresAt.Equal(got.ExpiresAt), "the stored expiry is %s, want %s", got.ExpiresAt, expiresAt)
}

func TestAccessTokenReturnsAFreshTokenUntouched(t *testing.T) {
	srv := newAuthServer(t)
	store := newStore(t, "stored-access", "stored-refresh", 2*time.Minute)
	session := newSession(srv, store)
	session.Endpoints = func(context.Context) (Endpoints, error) {
		require.FailNow(t, "a fresh token needs no endpoints")
		return Endpoints{}, nil
	}

	got, err := session.AccessToken(context.Background())

	require.NoError(t, err)
	sameSecret(t, "stored-access", got, "the access token")
	require.Empty(t, srv.tokenCalls())
	requireStored(t, store, "stored-access", "stored-refresh", baseTime.Add(2*time.Minute))
}

// More than 60 seconds left is fresh; 60 or fewer is not, and an expired
// token is the same case.
func TestAccessTokenFreshnessMargin(t *testing.T) {
	cases := map[string]struct {
		expiresIn time.Duration
		refreshes bool
	}{
		"61 seconds left":   {61 * time.Second, false},
		"60 seconds left":   {60 * time.Second, true},
		"59 seconds left":   {59 * time.Second, true},
		"already expired":   {-time.Hour, true},
		"expires right now": {0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newAuthServer(t)
			srv.accept("stored-refresh")
			session := newSession(srv, newStore(t, "stored-access", "stored-refresh", tc.expiresIn))

			got, err := session.AccessToken(context.Background())

			require.NoError(t, err)
			if tc.refreshes {
				access, _ := pair(1)
				sameSecret(t, access, got, "the access token")
				require.Len(t, srv.tokenCalls(), 1)
			} else {
				sameSecret(t, "stored-access", got, "the access token")
				require.Empty(t, srv.tokenCalls())
			}
		})
	}
}

func TestAccessTokenRefreshesWithin60Seconds(t *testing.T) {
	srv := newAuthServer(t)
	srv.accept("stored-refresh")
	store := newStore(t, "stored-access", "stored-refresh", 30*time.Second)
	session := newSession(srv, store)

	got, err := session.AccessToken(context.Background())

	require.NoError(t, err)
	access, refresh := pair(1)
	sameSecret(t, access, got, "the returned access token")
	// The new pair is in the store before AccessToken returned, rotated
	// refresh token included.
	requireStored(t, store, access, refresh, baseTime.Add(time.Hour))

	calls := srv.tokenCalls()
	require.Len(t, calls, 1)
	form := calls[0].Form
	require.Equal(t, "refresh_token", form.Get("grant_type"))
	sameSecret(t, "stored-refresh", form.Get("refresh_token"), "the refresh token sent")
	require.Equal(t, testClientID, form.Get("client_id"))
	require.False(t, form.Has("client_secret"))
	require.Empty(t, calls[0].Authorization)
	require.Equal(t, testAPIKey, calls[0].Apikey)
}

// The API refused the token it was given, so Refresh must obtain a new one
// even when the stored token still looks valid.
func TestRefreshRefreshesAFreshLookingToken(t *testing.T) {
	srv := newAuthServer(t)
	srv.accept("stored-refresh")
	store := newStore(t, "stored-access", "stored-refresh", 2*time.Hour)
	session := newSession(srv, store)

	got, err := session.Refresh(context.Background())

	require.NoError(t, err)
	access, refresh := pair(1)
	sameSecret(t, access, got, "the access token Refresh returned")
	requireStored(t, store, access, refresh, baseTime.Add(time.Hour))
}

// rotatedElsewhere is a Store that, right after the session's first read,
// receives what another CLI process saved: that process refreshed in between
// and spent the refresh token the session just read.
type rotatedElsewhere struct {
	credentials.Store
	other credentials.Tokens

	mu    sync.Mutex
	loads int
}

func (r *rotatedElsewhere) Load() (credentials.Tokens, error) {
	tokens, err := r.Store.Load()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loads++
	if r.loads == 1 {
		if saveErr := r.Save(r.other); saveErr != nil {
			return credentials.Tokens{}, saveErr
		}
	}
	return tokens, err
}

func TestRefreshRereadsTheStoreWhenAnotherProcessRotated(t *testing.T) {
	srv := newAuthServer(t)
	// "stale-refresh" was spent by the other process, so the server refuses
	// it; the pair that process saved is still good for half an hour.
	other := credentials.Tokens{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		ExpiresAt:    baseTime.Add(30 * time.Minute),
	}
	store := &rotatedElsewhere{Store: newStore(t, "stale-access", "stale-refresh", -time.Minute), other: other}
	session := newSession(srv, store)

	got, err := session.AccessToken(context.Background())

	require.NoError(t, err)
	sameSecret(t, "rotated-access", got, "the access token")
	calls := srv.tokenCalls()
	require.Len(t, calls, 1, "the other process's pair is fresh, so there is no second refresh")
	sameSecret(t, "stale-refresh", calls[0].Form.Get("refresh_token"), "the refresh token sent")
	requireStored(t, store.Store, "rotated-access", "rotated-refresh", other.ExpiresAt)
}

func TestRefreshRetriesWithTheRotatedToken(t *testing.T) {
	srv := newAuthServer(t)
	srv.accept("rotated-refresh")
	// The other process's access token has already run out.
	other := credentials.Tokens{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		ExpiresAt:    baseTime.Add(10 * time.Second),
	}
	store := &rotatedElsewhere{Store: newStore(t, "stale-access", "stale-refresh", -time.Minute), other: other}
	session := newSession(srv, store)

	got, err := session.AccessToken(context.Background())

	require.NoError(t, err)
	access, refresh := pair(1)
	sameSecret(t, access, got, "the access token")
	calls := srv.tokenCalls()
	require.Len(t, calls, 2)
	sameSecret(t, "stale-refresh", calls[0].Form.Get("refresh_token"), "the first refresh token sent")
	sameSecret(t, "rotated-refresh", calls[1].Form.Get("refresh_token"), "the second refresh token sent")
	requireStored(t, store.Store, access, refresh, baseTime.Add(time.Hour))
}

// The retry happens once. A rotated token that is refused too means the
// session is gone, and trying a third time would only repeat the question.
func TestRefreshGivesUpAfterOneRetry(t *testing.T) {
	srv := newAuthServer(t)
	other := credentials.Tokens{
		AccessToken:  "rotated-access",
		RefreshToken: "rotated-refresh",
		ExpiresAt:    baseTime.Add(10 * time.Second),
	}
	store := &rotatedElsewhere{Store: newStore(t, "stale-access", "stale-refresh", -time.Minute), other: other}
	session := newSession(srv, store)

	_, err := session.Refresh(context.Background())

	require.ErrorIs(t, err, ErrLoginRequired)
	require.Len(t, srv.tokenCalls(), 2)
}

func TestRefreshAsksForLoginWhenNothingChanged(t *testing.T) {
	srv := newAuthServer(t)
	store := newStore(t, "stale-access", "stale-refresh", -time.Minute)
	session := newSession(srv, store)

	_, err := session.Refresh(context.Background())

	require.ErrorIs(t, err, ErrLoginRequired)
	require.Len(t, srv.tokenCalls(), 1, "the refresh token is unchanged, so asking again would be the same question")
	// A failed refresh leaves the store as it was.
	requireStored(t, store, "stale-access", "stale-refresh", baseTime.Add(-time.Minute))
}

func TestA503DuringRefreshIsNotLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	srv.tokenStatus = http.StatusServiceUnavailable
	store := newStore(t, "stale-access", "stale-refresh", -time.Minute)
	session := newSession(srv, store)

	_, err := session.AccessToken(context.Background())

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLoginRequired)
	require.Contains(t, err.Error(), "503")
	require.NotContains(t, err.Error(), "stale-refresh")
	require.Len(t, srv.tokenCalls(), 1, "a server error is not a reason to look for a rotated token")
	requireStored(t, store, "stale-access", "stale-refresh", baseTime.Add(-time.Minute))
}

func TestANetworkErrorDuringRefreshIsNotLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	srv.Close()
	session := newSession(srv, newStore(t, "stale-access", "stale-refresh", -time.Minute))

	_, err := session.Refresh(context.Background())

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLoginRequired)
	require.NotContains(t, err.Error(), "stale-refresh")
}

func TestAnEndpointsFailureIsNotLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	errDiscovery := errors.New("discovery is down")
	session := newSession(srv, newStore(t, "stale-access", "stale-refresh", -time.Minute))
	session.Endpoints = func(context.Context) (Endpoints, error) { return Endpoints{}, errDiscovery }

	_, err := session.Refresh(context.Background())

	require.ErrorIs(t, err, errDiscovery)
	require.NotErrorIs(t, err, ErrLoginRequired)
}

func TestNothingStoredIsLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	session := newSession(srv, credentials.NewMemory())

	_, err := session.AccessToken(context.Background())
	require.ErrorIs(t, err, ErrLoginRequired)

	_, err = session.Refresh(context.Background())
	require.ErrorIs(t, err, ErrLoginRequired)

	require.Empty(t, srv.tokenCalls())
}

func TestWithoutARefreshTokenTheSessionIsLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	session := newSession(srv, newStore(t, "stale-access", "", -time.Minute))

	_, err := session.AccessToken(context.Background())

	require.ErrorIs(t, err, ErrLoginRequired)
	require.Empty(t, srv.tokenCalls())
}

// A store that cannot be read is broken, not empty, and `login` would not
// fix it.
func TestAnUnreadableStoreIsNotLoginRequired(t *testing.T) {
	srv := newAuthServer(t)
	errKeychain := errors.New("the keychain is locked")
	session := newSession(srv, brokenStore{loadErr: errKeychain})

	_, err := session.AccessToken(context.Background())

	require.ErrorIs(t, err, errKeychain)
	require.NotErrorIs(t, err, ErrLoginRequired)
}

// The server has rotated the refresh token by the time Save fails, so the
// failure has to be reported and not swallowed: the person's next command
// would otherwise quietly depend on a token the server has already replaced.
func TestARefreshThatCannotBeSavedIsReported(t *testing.T) {
	srv := newAuthServer(t)
	srv.accept("stale-refresh")
	errDisk := errors.New("the disk is full")
	inner := newStore(t, "stale-access", "stale-refresh", -time.Minute)
	session := newSession(srv, brokenStore{Store: inner, saveErr: errDisk})

	_, err := session.AccessToken(context.Background())

	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, ErrLoginRequired)
}

// brokenStore fails the operations it is given an error for and defers the
// others to its Store.
type brokenStore struct {
	credentials.Store
	loadErr error
	saveErr error
}

func (b brokenStore) Load() (credentials.Tokens, error) {
	if b.loadErr != nil {
		return credentials.Tokens{}, b.loadErr
	}
	return b.Store.Load()
}

func (b brokenStore) Save(t credentials.Tokens) error {
	if b.saveErr != nil {
		return b.saveErr
	}
	return b.Store.Save(t)
}

// Several requests can be refused at once, and each asks for a refresh. The
// token endpoint rotates, so a refresh token it has seen once is spent: the
// session has to read the store, call the endpoint and write the result back
// as one step, or two goroutines would both send the same token.
func TestConcurrentRefreshesNeverSpendATokenTwice(t *testing.T) {
	const goroutines = 6
	srv := newAuthServer(t)
	srv.accept("stored-refresh")
	// Long enough that every goroutine has read the store before the first
	// answer arrives, if the session lets them.
	srv.refreshDelay = 25 * time.Millisecond
	store := newStore(t, "stored-access", "stored-refresh", -time.Minute)
	session := newSession(srv, store)

	var wg sync.WaitGroup
	tokens := make([]string, goroutines)
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i], errs[i] = session.Refresh(context.Background())
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := range goroutines {
		require.NoError(t, errs[i])
		require.NotEmpty(t, tokens[i])
		require.False(t, seen[tokens[i]], "two refreshes returned the same access token")
		seen[tokens[i]] = true
	}
	spent := srv.timesSpent()
	require.Len(t, spent, goroutines, "each refresh spends the token the one before it issued")
	for _, times := range spent {
		require.Equal(t, 1, times, "a refresh token reached the token endpoint twice")
	}
	require.Len(t, srv.tokenCalls(), goroutines)
	access, refresh := pair(goroutines)
	requireStored(t, store, access, refresh, baseTime.Add(time.Hour))
}

func TestSessionDefaultsItsClientAndClock(t *testing.T) {
	srv := newAuthServer(t)
	srv.accept("stored-refresh")
	store := credentials.NewMemory()
	require.NoError(t, store.Save(credentials.Tokens{
		AccessToken:  "stored-access",
		RefreshToken: "stored-refresh",
		// Without Now the session reads the wall clock, so the expiry has to
		// be relative to it.
		ExpiresAt: time.Now().Add(-time.Hour),
	}))
	session := newSession(srv, store)
	session.HTTPClient = nil
	session.Now = nil

	got, err := session.AccessToken(context.Background())

	require.NoError(t, err)
	access, _ := pair(1)
	sameSecret(t, access, got, "the access token")
}
