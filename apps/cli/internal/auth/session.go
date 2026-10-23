package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// refreshMargin is how much life an access token must have left to be used
// as it is. A request takes time to reach the API and the clocks of the two
// machines differ, so a token about to run out is renewed first.
const refreshMargin = 60 * time.Second

// ErrLoginRequired means the CLI has no usable session: nobody has logged in,
// the person has logged out, or the server no longer honours the stored
// refresh token and no other process has replaced it. Running `login` is the
// only way out, which is what separates it from a failure that may pass.
var ErrLoginRequired = errors.New("login required")

// Session hands out an access token that is good to use, renewing it from
// the refresh token when it is not. It satisfies the API client's
// TokenSource, which is declared there, through AccessToken and Refresh.
//
// Every refresh is written back to the Store before it returns, because the
// token endpoint rotates the refresh token: the one just spent is useless
// afterwards, and losing the new one would end the session.
//
// It is safe for concurrent use, and must not be copied after first use.
type Session struct {
	// Store holds the tokens. Required.
	Store credentials.Store
	// Endpoints supplies the token endpoint, usually through a memoised
	// Discover. Required.
	Endpoints func(ctx context.Context) (Endpoints, error)
	// ClientID is the CLI's public OAuth client.
	ClientID string
	// PublishableKey is sent as the apikey header on the token call.
	PublishableKey string
	// HTTPClient sends the token call. Nil means a client with a 30 second
	// timeout.
	HTTPClient *http.Client
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	// mu serialises refreshes. Several goroutines can get a 401 at once, and
	// two of them reading the same refresh token from the store would both
	// spend it: the second spend is refused, and the session ends although
	// nothing was wrong.
	mu sync.Mutex
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// fresh reports whether a stored access token can be used as it is: it must
// exist and have more than refreshMargin left.
func (s *Session) fresh(t credentials.Tokens) bool {
	return t.AccessToken != "" && t.ExpiresAt.Sub(s.now()) > refreshMargin
}

// AccessToken returns the stored access token when it has more than a minute
// left, and otherwise refreshes it first.
func (s *Session) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.load()
	if err != nil {
		return "", err
	}
	if s.fresh(stored) {
		return stored.AccessToken, nil
	}
	return s.refresh(ctx, stored)
}

// Refresh obtains a new access token and returns it. The API client calls it
// after a 401, so it refreshes even when the stored token still looks valid:
// the API has just said otherwise.
func (s *Session) Refresh(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.load()
	if err != nil {
		return "", err
	}
	return s.refresh(ctx, stored)
}

// load reads the store. Nothing stored is ErrLoginRequired; a store that
// cannot be read is its own error, because logging in again would not fix it.
func (s *Session) load() (credentials.Tokens, error) {
	tokens, err := s.Store.Load()
	if errors.Is(err, credentials.ErrNotFound) {
		return credentials.Tokens{}, ErrLoginRequired
	}
	if err != nil {
		return credentials.Tokens{}, fmt.Errorf("read the stored credentials: %w", err)
	}
	return tokens, nil
}

// refresh spends stored's refresh token and saves what comes back. s.mu must
// be held.
//
// When the server refuses the token (HTTP 400 or 401), another CLI process
// may have rotated it first: the server keeps accepting the newest token and
// its parent, but not a grandparent, so a process that read the store before
// the other one wrote to it holds a token that is now two rotations old. The
// store is read once more. A different refresh token there means the other
// process won, and its access token is used if it has life left, or else
// refreshed once with its refresh token. The same token, or a second
// refusal, means the session is over.
//
// A network failure or a 5xx is not that. It says nothing about the token,
// so it is returned as an error and the stored tokens stay as they are.
func (s *Session) refresh(ctx context.Context, stored credentials.Tokens) (string, error) {
	if stored.RefreshToken == "" {
		return "", fmt.Errorf("%w: nothing to refresh with", ErrLoginRequired)
	}
	endpoints, err := s.Endpoints(ctx)
	if err != nil {
		return "", fmt.Errorf("find the authorization server: %w", err)
	}

	fresh, err := s.redeem(ctx, endpoints, stored.RefreshToken)
	if err == nil {
		return s.keep(fresh)
	}
	if !refused(err) {
		return "", fmt.Errorf("refresh the session: %w", err)
	}

	current, loadErr := s.load()
	if loadErr != nil {
		return "", loadErr
	}
	if current.RefreshToken == "" || current.RefreshToken == stored.RefreshToken {
		return "", fmt.Errorf("%w: %w", ErrLoginRequired, err)
	}
	if s.fresh(current) {
		return current.AccessToken, nil
	}

	fresh, err = s.redeem(ctx, endpoints, current.RefreshToken)
	if err == nil {
		return s.keep(fresh)
	}
	if refused(err) {
		return "", fmt.Errorf("%w: %w", ErrLoginRequired, err)
	}
	return "", fmt.Errorf("refresh the session: %w", err)
}

// redeem asks the token endpoint for a new pair in exchange for a refresh
// token.
func (s *Session) redeem(ctx context.Context, endpoints Endpoints, refreshToken string) (credentials.Tokens, error) {
	cfg := &oauth2.Config{
		ClientID: s.ClientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:   endpoints.Authorization,
			TokenURL:  endpoints.Token,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, apiKeyClient(s.HTTPClient, s.PublishableKey))

	// A token with only a refresh token is not valid, so the source goes
	// straight to the token endpoint.
	tok, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return credentials.Tokens{}, reduceTokenError(err)
	}
	return credentials.Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    expiryOf(tok, s.now()),
	}, nil
}

// keep saves a refreshed pair and returns its access token. The save comes
// first: the server has already replaced the old refresh token, so a pair
// that exists only in memory is lost with the process. A failed save is an
// error and not a quiet success, for the same reason.
func (s *Session) keep(fresh credentials.Tokens) (string, error) {
	if err := s.Store.Save(fresh); err != nil {
		return "", fmt.Errorf("save the refreshed credentials: %w", err)
	}
	return fresh.AccessToken, nil
}

// expiryOf is when a token expires, by the given clock. The response's
// expires_in is relative to the moment it arrives, so it is added to the
// caller's own "now" and not to the library's wall clock, which keeps the
// result testable. Without expires_in, the library's own Expiry stands.
func expiryOf(tok *oauth2.Token, now time.Time) time.Time {
	if tok.ExpiresIn > 0 {
		return now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return tok.Expiry
}

// tokenEndpointError is what a refusal by the token endpoint turns into. The
// library's own error quotes the response body, and a body is server text
// that could carry anything, a credential the server echoes back included.
// This keeps what the CLI needs, the status and RFC 6749's error code, and
// nothing else.
type tokenEndpointError struct {
	status int
	code   string
}

func (e *tokenEndpointError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("the token endpoint answered HTTP %d", e.status)
	}
	return fmt.Sprintf("the token endpoint answered HTTP %d (%s)", e.status, e.code)
}

// errorCodePattern is the shape of an OAuth error code: a short token such as
// invalid_grant. Anything else is dropped rather than printed.
var errorCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// reduceTokenError turns the library's RetrieveError into a
// tokenEndpointError and leaves every other error, a network failure or a
// cancelled context, as it is.
func reduceTokenError(err error) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return err
	}
	reduced := &tokenEndpointError{status: re.Response.StatusCode}
	if errorCodePattern.MatchString(re.ErrorCode) {
		reduced.code = re.ErrorCode
	}
	return reduced
}

// refused reports whether the token endpoint rejected the refresh token
// itself: 400 is invalid_grant, 401 is the same refusal from a stricter
// gateway. Every other status, and every failure to reach the server, says
// nothing about the token.
func refused(err error) bool {
	var te *tokenEndpointError
	return errors.As(err, &te) && (te.status == http.StatusBadRequest || te.status == http.StatusUnauthorized)
}
