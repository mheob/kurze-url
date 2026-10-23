package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fixtures every test shares. None is a real credential.
const (
	testClientID = "cli-client-id"
	testAPIKey   = "sb_publishable_test"
	testCode     = "authorization-code-1"

	// The paths the stub serves, which are the ones Supabase's auth server
	// uses.
	stubDiscoveryPath = "/auth/v1/.well-known/oauth-authorization-server"
	stubAuthorizePath = "/auth/v1/oauth/authorize"
	stubTokenPath     = "/auth/v1/oauth/token"
	stubLogoutPath    = "/auth/v1/logout"
)

// serverCall is what the stub saw of one request: enough to assert the
// method, the path, the headers that matter and the form fields.
type serverCall struct {
	Method        string
	Path          string
	Query         url.Values
	Apikey        string
	Authorization string
	Form          url.Values
}

// authServer stands in for Supabase's OAuth 2.1 server: discovery, the token
// endpoint (a code exchange and a refresh that rotates the refresh token) and
// logout. It records every call, so a test asserts what the CLI sent.
type authServer struct {
	*httptest.Server

	mu sync.Mutex

	calls []serverCall

	// methods is what discovery lists as code_challenge_methods_supported.
	methods []string

	// challenge, when set, is the PKCE challenge a code exchange has to
	// match: the stub hashes the verifier it receives and refuses any other.
	challenge string

	// valid holds the refresh tokens the token endpoint accepts. A refresh
	// spends its token and adds the one it issues, like Supabase's rotation.
	valid map[string]bool

	// spent counts the requests that carried each refresh token, accepted or
	// not, which is how a test sees a token being used twice.
	spent map[string]int

	// minted numbers the token pairs issued, so every pair is distinct.
	minted int

	// tokenStatus, when not zero, makes the token endpoint answer with that
	// status and an error body, as a failing server would.
	tokenStatus int

	// refreshDelay pauses a refresh before it is answered, so that requests
	// which should have been serialised overlap when they were not.
	refreshDelay time.Duration

	// logoutStatus is what logout answers; zero means 204.
	logoutStatus int

	// redirect, when not empty, makes every request answer a 307 to it.
	redirect string
}

// newAuthServer starts the stub and stops it when the test ends.
func newAuthServer(t *testing.T) *authServer {
	t.Helper()
	s := &authServer{
		methods: []string{"S256", "plain"},
		valid:   map[string]bool{},
		spent:   map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// endpoints are the authorize and token URLs discovery announces.
func (s *authServer) endpoints() Endpoints {
	return Endpoints{
		Authorization: s.URL + stubAuthorizePath,
		Token:         s.URL + stubTokenPath,
	}
}

// accept registers refresh tokens the token endpoint will honour.
func (s *authServer) accept(refreshTokens ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rt := range refreshTokens {
		s.valid[rt] = true
	}
}

// expectChallenge makes the code exchange verify its code_verifier against
// the challenge the browser was sent.
func (s *authServer) expectChallenge(challenge string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenge = challenge
}

// callsTo returns the recorded calls to one path, oldest first.
func (s *authServer) callsTo(path string) []serverCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []serverCall
	for _, c := range s.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// tokenCalls are the requests the token endpoint received.
func (s *authServer) tokenCalls() []serverCall {
	return s.callsTo(stubTokenPath)
}

// timesSpent is how often each refresh token reached the token endpoint.
func (s *authServer) timesSpent() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.spent))
	for k, v := range s.spent {
		out[k] = v
	}
	return out
}

// pair is the access and refresh token the stub issues as its n-th pair.
func pair(n int) (access, refresh string) {
	return fmt.Sprintf("access-%d", n), fmt.Sprintf("refresh-%d", n)
}

func (s *authServer) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, serverCall{
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.Query(),
		Apikey:        r.Header.Get("apikey"),
		Authorization: r.Header.Get("Authorization"),
		Form:          r.PostForm,
	})
	redirect := s.redirect
	s.mu.Unlock()

	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusTemporaryRedirect)
		return
	}

	switch r.URL.Path {
	case stubDiscoveryPath:
		s.serveDiscovery(w)
	case stubTokenPath:
		s.serveToken(w, r)
	case stubLogoutPath:
		s.mu.Lock()
		status := s.logoutStatus
		s.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	default:
		http.NotFound(w, r)
	}
}

func (s *authServer) serveDiscovery(w http.ResponseWriter) {
	s.mu.Lock()
	methods := s.methods
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                           s.URL + "/auth/v1",
		"authorization_endpoint":           s.URL + stubAuthorizePath,
		"token_endpoint":                   s.URL + stubTokenPath,
		"code_challenge_methods_supported": methods,
	})
}

func (s *authServer) serveToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status := s.tokenStatus
	delay := s.refreshDelay
	s.mu.Unlock()

	if status != 0 {
		writeJSON(w, status, map[string]string{"error": "server_error"})
		return
	}

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.serveCodeExchange(w, r)
	case "refresh_token":
		if delay > 0 {
			time.Sleep(delay)
		}
		s.serveRefresh(w, r.PostForm.Get("refresh_token"))
	default:
		invalidGrant(w, "unsupported grant")
	}
}

func (s *authServer) serveCodeExchange(w http.ResponseWriter, r *http.Request) {
	form := r.PostForm
	s.mu.Lock()
	challenge := s.challenge
	s.mu.Unlock()

	if form.Get("code") != testCode || form.Get("client_id") != testClientID || form.Get("redirect_uri") == "" {
		invalidGrant(w, "unknown code")
		return
	}
	if challenge != "" {
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			invalidGrant(w, "code_verifier does not match")
			return
		}
	}
	s.issue(w)
}

func (s *authServer) serveRefresh(w http.ResponseWriter, refreshToken string) {
	s.mu.Lock()
	s.spent[refreshToken]++
	ok := s.valid[refreshToken]
	delete(s.valid, refreshToken)
	s.mu.Unlock()

	if !ok {
		invalidGrant(w, "Invalid Refresh Token: Refresh Token Not Found")
		return
	}
	s.issue(w)
}

// issue answers with the next token pair and accepts its refresh token.
func (s *authServer) issue(w http.ResponseWriter) {
	s.mu.Lock()
	s.minted++
	access, refresh := pair(s.minted)
	s.valid[refresh] = true
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    3600,
		"refresh_token": refresh,
	})
}

func invalidGrant(w http.ResponseWriter, description string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error":             "invalid_grant",
		"error_description": description,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// sameSecret reports a mismatch without printing either value: a failing
// test must not put a token into the log, even a fixture that looks like one.
func sameSecret(t *testing.T, want, got string, what string) {
	t.Helper()
	require.True(t, want == got, "%s is not the expected value (lengths %d and %d)", what, len(want), len(got))
}

// syncBuffer is a bytes.Buffer that a goroutine can read while another one
// writes, for the stderr the login flow prints to.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
