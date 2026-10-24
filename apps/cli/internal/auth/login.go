// Package auth is how the CLI signs in, stays signed in and signs out
// against Supabase's OAuth 2.1 server: discovery of its endpoints, the
// Authorization Code flow with PKCE over a loopback listener, a session that
// hands out a current access token and refreshes it, and logout.
//
// The package never stores anything itself. Login returns the tokens and the
// caller saves them; Session reads and writes through a credentials.Store.
//
// No error this package returns contains a token, a refresh token, an
// authorization code or a code verifier, and nothing here prints one. A
// refusal from the token endpoint is reduced to its HTTP status and RFC 6749
// error code before it travels, because the library's own error text quotes
// the response body.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// DefaultPorts are the loopback ports the login tries, in this order. Supabase
// matches a redirect URI exactly, port included, so these three are the ones
// registered on the CLI's OAuth client, and the list cannot grow without the
// client being edited in both Supabase projects.
var DefaultPorts = []int{53682, 53683, 53684}

// DefaultLoginTimeout is how long the login waits for the browser to come
// back before it gives up.
const DefaultLoginTimeout = 5 * time.Minute

// The ways a login ends without tokens, other than a failing network or
// server. A caller tells the person what to do from these.
var (
	// ErrStateMismatch means the callback carried a state the login did not
	// issue. Something other than the browser the CLI opened reached the
	// listener, so the callback is not trusted.
	ErrStateMismatch = errors.New("the login callback carried an unexpected state")

	// ErrDenied means the authorization server reported an error instead of
	// a code: the person declined, or the server refused the request.
	ErrDenied = errors.New("the login was denied")

	// ErrTimeout means the browser did not come back in time.
	ErrTimeout = errors.New("the login timed out")
)

// errNoCode is a callback with the right state, no error and no code. A
// server does not do that, so the login stops instead of exchanging nothing.
var errNoCode = errors.New("the login callback carried no authorization code")

const (
	// callbackPath is the path of the redirect URI, registered with the
	// client.
	callbackPath = "/callback"

	// shutdownGrace is how long the listener gets to finish writing the page
	// after the flow has decided. A connection a browser opened speculatively
	// and never used would otherwise hold the login open.
	shutdownGrace = 2 * time.Second
)

// Flow is one login: the Authorization Code flow with PKCE against the
// endpoints discovery found, over a listener on 127.0.0.1.
type Flow struct {
	// Endpoints are where the browser is sent and the code is exchanged.
	Endpoints Endpoints
	// ClientID is the CLI's public OAuth client. It has no secret, so the ID
	// travels in the request body and nothing is sent as basic auth.
	ClientID string
	// PublishableKey is sent as the apikey header on the token call.
	PublishableKey string
	// HTTPClient sends the token call. Nil means a client with a 30 second
	// timeout.
	HTTPClient *http.Client
	// Ports are the loopback ports to try in order. Empty means
	// DefaultPorts; a 0 entry asks the system for a free port, which tests
	// use.
	Ports []int
	// Timeout is how long to wait for the callback. Zero means
	// DefaultLoginTimeout.
	Timeout time.Duration
	// OpenBrowser opens the authorize URL. When it is nil or fails, the URL
	// is printed to Stderr instead and the login keeps waiting.
	OpenBrowser func(string) error
	// Stderr receives the progress lines. Nil discards them.
	Stderr io.Writer
	// Catalog words the progress lines and the callback pages. Nil means
	// English.
	Catalog *i18n.Catalog
}

// outcome is how a callback ended the flow.
type outcome struct {
	tokens credentials.Tokens
	err    error
}

// Login runs the flow and returns the tokens. It stores nothing: the caller
// saves them, so a login that cannot be saved is the caller's to report.
//
// It binds the first free port, tells the person it is waiting, opens the
// browser (or prints the URL), and waits for one callback. The first request
// to the callback path decides the flow: a state that is not the one issued
// ends it with ErrStateMismatch, an error parameter with ErrDenied, and
// otherwise the code is exchanged. Whatever happens, the listener is closed
// before Login returns.
func (f Flow) Login(ctx context.Context) (credentials.Tokens, error) {
	cat := f.Catalog
	if cat == nil {
		cat = i18n.Load(i18n.EN)
	}
	stderr := f.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	ports := f.Ports
	if len(ports) == 0 {
		ports = DefaultPorts
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultLoginTimeout
	}

	// Everything that can fail without a listener comes before the bind, so
	// there is no path on which a bound port is left open.
	state, err := randomState()
	if err != nil {
		return credentials.Tokens{}, err
	}
	verifier := oauth2.GenerateVerifier()
	ln, port, err := bindFirstFree(ports)
	if err != nil {
		return credentials.Tokens{}, err
	}

	cfg := &oauth2.Config{
		ClientID: f.ClientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:   f.Endpoints.Authorization,
			TokenURL:  f.Endpoints.Token,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath),
	}
	authURL := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	parent := ctx
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cb := &callback{
		state:    state,
		verifier: verifier,
		cfg:      cfg,
		ctx:      context.WithValue(ctx, oauth2.HTTPClient, apiKeyClient(f.HTTPClient, f.PublishableKey)),
		cat:      cat,
		result:   make(chan outcome, 1),
	}
	srv := &http.Server{
		Handler:           cb,
		ReadHeaderTimeout: 10 * time.Second,
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ln)
	}()
	defer stopServer(srv, served)

	_, _ = fmt.Fprintln(stderr, cat.T("login.waiting"))
	if f.OpenBrowser == nil || f.OpenBrowser(authURL) != nil {
		_, _ = fmt.Fprintln(stderr, cat.T("login.openManually", i18n.Args{"url": authURL}))
	}

	select {
	case out := <-cb.result:
		return out.tokens, out.err
	case <-ctx.Done():
		// The caller's own cancellation or deadline is not the login's
		// timeout, and Ctrl+C must not read as "timed out".
		if err := parent.Err(); err != nil {
			return credentials.Tokens{}, fmt.Errorf("the login was interrupted: %w", err)
		}
		return credentials.Tokens{}, fmt.Errorf("%w: the browser did not come back within %s", ErrTimeout, timeout)
	}
}

// randomState returns 32 random bytes, base64url-encoded: the value that ties
// the callback to this login, and which nothing but the browser the CLI
// opened can know.
func randomState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate the login state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// stopServer shuts the listener down, giving a response still being written
// a moment to finish, and waits until Serve has returned, so that the port is
// free again when Login does.
func stopServer(srv *http.Server, served <-chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
	}
	<-served
}

// callback is the loopback listener's handler. It answers one request that
// matters, the first GET of the callback path, and 404 to everything else.
type callback struct {
	state    string
	verifier string
	cfg      *oauth2.Config
	// ctx carries the login's deadline and the HTTP client for the exchange.
	// It is not the request's context: a browser that closes the tab must not
	// abandon an exchange whose one-time code is already spent.
	ctx context.Context
	cat *i18n.Catalog

	// result receives the single outcome. It is buffered, so the handler
	// never waits for a Login that has already returned.
	result chan outcome

	mu      sync.Mutex
	decided bool
}

// claim reports whether this request is the one that decides the flow. Only
// the first one is: a second callback, a browser's retry or a replay, must
// not exchange the code again.
func (c *callback) claim() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decided {
		return false
	}
	c.decided = true
	return true
}

func (c *callback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != callbackPath || !c.claim() {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()

	var out outcome
	switch {
	// Constant time, so the state cannot be guessed a byte at a time from
	// how long the answer takes.
	case subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(c.state)) != 1:
		out.err = ErrStateMismatch
	case query.Get("error") != "":
		out.err = ErrDenied
	case query.Get("code") == "":
		out.err = errNoCode
	default:
		out.tokens, out.err = c.exchange(query.Get("code"))
	}

	c.respond(w, out.err == nil)
	c.result <- out
}

// exchange trades the code, with the verifier that proves this process asked
// for it, for tokens.
func (c *callback) exchange(code string) (credentials.Tokens, error) {
	tok, err := c.cfg.Exchange(c.ctx, code, oauth2.VerifierOption(c.verifier))
	if err != nil {
		return credentials.Tokens{}, fmt.Errorf("exchange the authorization code: %w", reduceTokenError(err))
	}
	return credentials.Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    expiryOf(tok, time.Now()),
	}, nil
}

// respond writes the page. The security headers are for a page that carries
// a one-time code in its own address: it must not be cached, must not leak
// that address as a referrer, and loads nothing at all.
func (c *callback) respond(w http.ResponseWriter, success bool) {
	body, err := renderPage(c.cat, success)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if !success {
		status = http.StatusBadRequest
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	// The flow is over after this response, so the connection is not kept.
	h.Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
