package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// This file is the harness every command test builds on. It stands in for
// everything a command reaches outside the process through: the API, the
// authorization server, the keychain, the browser, the terminal and the
// environment. None of it touches the real keychain, the real config
// directory, the process's environment or any network beyond httptest, so a
// test can run a whole command through the real Run and read what it printed.
//
// A test builds one with newHarness, arranges what it needs (h.signIn,
// h.writeConfig, h.serveMe, h.api.handle) and calls h.run. Everything else is
// a field on the harness that the test may change before it runs.

// The values every test shares. None is a real credential.
const (
	testClientID = "cli-test-client"
	testAPIKey   = "sb_publishable_test"
	testCode     = "authorization-code-1"

	// The paths the stub authorization server serves, which are the ones
	// Supabase's own server uses.
	stubDiscoveryPath = "/auth/v1/.well-known/oauth-authorization-server"
	stubAuthorizePath = "/auth/v1/oauth/authorize"
	stubTokenPath     = "/auth/v1/oauth/token"
	stubLogoutPath    = "/auth/v1/logout"

	// signedInRefresh is the refresh token h.signIn stores, and the one the
	// stub authorization server honours until a test says otherwise.
	signedInAccess  = "access-signed-in"
	signedInRefresh = "refresh-signed-in"
)

// testNow is the clock every command sees: the day the plan fixes the stats
// windows on. Tokens h.signIn stores expire an hour after it.
var testNow = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

// The two teams the fixtures belong to. Their IDs are made up and distinct,
// so a test that mixes them up fails on the ID and not by luck.
var (
	teamGruenwald = apiclient.Membership{
		TeamID: "00000000-0000-4000-8000-000000000001",
		Name:   "SV Grünwald",
		Slug:   "sv-gruenwald",
		Role:   "owner",
	}
	teamMuster = apiclient.Membership{
		TeamID: "00000000-0000-4000-8000-000000000002",
		Name:   "TC Muster",
		Slug:   "tc-muster",
		Role:   "viewer",
	}
)

// harness is one command invocation's world.
type harness struct {
	t *testing.T

	// deps is what Run receives. Every field is set, and a test may replace
	// any of them (a different store, a failing browser) before h.run.
	deps *Deps

	// stdout and stderr collect what the command printed. A test that runs
	// two commands calls Reset on them in between.
	stdout, stderr *bytes.Buffer

	// api and auth are the fake servers the environment overrides point at.
	api  *fakeAPI
	auth *stubAuth

	// store is the credential store the command gets, in memory.
	store *credentials.Memory
	// storeEnvName and storeDir are what the command asked NewStore for.
	storeEnvName, storeDir string

	// configPath is where KURZE_URL_CONFIG points, inside the test's own
	// temporary directory. The file does not exist until a test writes one
	// or a command saves one.
	configPath string

	// env is what Getenv answers from. It starts with the overrides that
	// point the CLI at the two fake servers; a test adds LANG, LC_ALL and so
	// on to it.
	env map[string]string

	// stdinTTY, stdoutTTY and termWidth are what the terminal probes answer.
	stdinTTY, stdoutTTY bool
	termWidth           int

	// opened is every URL the fake browser was asked to open.
	opened []string

	// home is what HomeDir answers: a temporary directory of the test's own,
	// so a command never reaches the real shell configuration files.
	home string

	// programs are the programs the fake LookPath finds, and outputs is what
	// the fake RunCommand prints for each, by name. ran records every
	// RunCommand call, the program's name first. Nothing is ever started.
	programs map[string]bool
	outputs  map[string]string
	ran      [][]string
}

// newHarness builds a harness whose commands are signed out, with no config
// file, in English, on a terminal that is neither stdin nor stdout, on Linux
// with an empty home directory and no program on the PATH.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		store:     credentials.NewMemory(),
		termWidth: 80,
		home:      t.TempDir(),
		programs:  map[string]bool{},
		outputs:   map[string]string{},
	}
	h.api = newFakeAPI(t)
	h.auth = newStubAuth(t)
	h.configPath = filepath.Join(t.TempDir(), "kurze-url", "config.yaml")
	h.env = map[string]string{
		"KURZE_URL_CONFIG":                   h.configPath,
		"KURZE_URL_API_URL":                  h.api.URL,
		"KURZE_URL_SUPABASE_URL":             h.auth.URL,
		"KURZE_URL_SUPABASE_PUBLISHABLE_KEY": testAPIKey,
		"KURZE_URL_CLIENT_ID":                testClientID,
		// A language is pinned so the test cannot depend on the machine's
		// own locale, on Windows where Detect would otherwise ask for it.
		"LANG": "en_US.UTF-8",
	}
	h.deps = &Deps{
		Stdout:    h.stdout,
		Stderr:    h.stderr,
		Stdin:     strings.NewReader(""),
		Build:     testBuild,
		Getenv:    func(name string) string { return h.env[name] },
		Now:       func() time.Time { return testNow },
		StdinTTY:  func() bool { return h.stdinTTY },
		StdoutTTY: func() bool { return h.stdoutTTY },
		TermWidth: func() int { return h.termWidth },
		// The fake browser completes the callback, so a login needs no
		// person. The listener takes any free port, because the registered
		// ones may be busy on the machine running the tests.
		OpenBrowser:  h.openBrowser,
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		LoginPorts:   []int{0},
		LoginTimeout: 5 * time.Second,
		// Completion keeps its production budget, which no test comes
		// near; one that needs the deadline to pass shortens it.
		CompletionTimeout: completionTimeout,
		// The credentials directory is never used while the store is in
		// memory, but handing over a real, empty one keeps a mistake from
		// writing next to the test.
		CredentialsDir: t.TempDir(),
		NewStore: func(envName, dir string) credentials.Store {
			h.storeEnvName, h.storeDir = envName, dir
			return h.store
		},
		GOOS:       "linux",
		HomeDir:    func() (string, error) { return h.home, nil },
		LookPath:   h.lookPath,
		RunCommand: h.runCommand,
	}
	return h
}

// lookPath is the fake PATH: it finds the programs in h.programs and nothing
// else, failing the way exec.LookPath does.
func (h *harness) lookPath(name string) (string, error) {
	if h.programs[name] {
		return filepath.Join(h.home, "bin", name), nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// runCommand is the fake process starter. It records the call and answers
// with the output the test programmed for the program, or fails for one the
// test did not program.
func (h *harness) runCommand(_ context.Context, name string, args ...string) ([]byte, error) {
	h.ran = append(h.ran, append([]string{name}, args...))
	out, ok := h.outputs[name]
	if !ok {
		return nil, fmt.Errorf("the test programmed no output for %s", name)
	}
	return []byte(out), nil
}

// run executes the real command tree and returns the exit code.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	return Run(h.deps, args)
}

// setStdin makes the next command read text from stdin.
func (h *harness) setStdin(text string) {
	h.deps.Stdin = strings.NewReader(text)
}

// signIn stores tokens that are good for an hour and that the stub
// authorization server would refresh, as a login would have left them.
func (h *harness) signIn() {
	h.t.Helper()
	require.NoError(h.t, h.store.Save(credentials.Tokens{
		AccessToken:  signedInAccess,
		RefreshToken: signedInRefresh,
		ExpiresAt:    testNow.Add(time.Hour),
	}))
	h.auth.accept(signedInRefresh)
}

// writeConfig writes the config file, creating its directory.
func (h *harness) writeConfig(yaml string) {
	h.t.Helper()
	require.NoError(h.t, os.MkdirAll(filepath.Dir(h.configPath), 0o700))
	require.NoError(h.t, os.WriteFile(h.configPath, []byte(yaml), 0o600))
}

// config returns the config file as it is on disk, or "" when there is none.
func (h *harness) config() string {
	h.t.Helper()
	data, err := os.ReadFile(h.configPath)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(h.t, err)
	return string(data)
}

// serveMe makes GET /v1/me answer as ada@example.org with these memberships.
func (h *harness) serveMe(memberships ...apiclient.Membership) {
	h.t.Helper()
	if memberships == nil {
		memberships = []apiclient.Membership{}
	}
	h.api.respond(http.MethodGet, apiclient.PathMe, http.StatusOK, apiclient.Me{
		UserID:      "00000000-0000-4000-8000-0000000000aa",
		Email:       "ada@example.org",
		Memberships: memberships,
	})
}

// openBrowser is the fake browser. It follows the authorize URL the way a
// signed-in person's browser would after consenting: it sends the redirect to
// the loopback listener with the code and the state, on a goroutine of its
// own, because Login is waiting for it on this one.
func (h *harness) openBrowser(authURL string) error {
	h.opened = append(h.opened, authURL)
	go func() {
		// A failure here is not reported: the login would then end in its
		// timeout, which the test sees.
		u, err := url.Parse(authURL)
		if err != nil {
			return
		}
		q := u.Query()
		callback := url.Values{"code": {testCode}, "state": {q.Get("state")}}
		client := &http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{DisableKeepAlives: true},
		}
		resp, err := client.Get(q.Get("redirect_uri") + "?" + callback.Encode())
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return nil
}

// ---- the fake API ----

// apiCall is what the fake API saw of one request.
type apiCall struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// fakeAPI stands in for the kurze-url API. A test registers one handler per
// method and path; a request nobody registered fails the test, because it
// means a command made a call the test did not expect. Every request is
// recorded, answered or not, so a test can also assert that none was made.
type fakeAPI struct {
	*httptest.Server
	t *testing.T

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	calls    []apiCall
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	a := &fakeAPI{t: t, handlers: map[string]http.HandlerFunc{}}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Close)
	return a
}

func routeKey(method, path string) string {
	return method + " " + path
}

// handle registers fn for one method and path, as the router sees the path:
// "/v1/teams/<id>/links", without the query. A second registration replaces
// the first.
func (a *fakeAPI) handle(method, path string, fn http.HandlerFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handlers[routeKey(method, path)] = fn
}

// respond registers a handler answering with status and body as JSON.
func (a *fakeAPI) respond(method, path string, status int, body any) {
	a.handle(method, path, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, "application/json", body)
	})
}

// problem registers a handler answering with an RFC 9457 document, the shape
// Huma writes for every failure. The title is the status text.
func (a *fakeAPI) problem(method, path string, status int, detail string, errs ...apiclient.ProblemDetail) {
	a.handle(method, path, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, "application/problem+json", apiclient.ProblemError{
			Status: status,
			Title:  http.StatusText(status),
			Detail: detail,
			Errors: errs,
		})
	})
}

// callsTo returns the recorded requests to one method and path, oldest first.
func (a *fakeAPI) callsTo(method, path string) []apiCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []apiCall
	for _, c := range a.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// requests returns every recorded request, oldest first.
func (a *fakeAPI) requests() []apiCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]apiCall(nil), a.calls...)
}

func (a *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	a.mu.Lock()
	a.calls = append(a.calls, apiCall{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   body,
	})
	fn := a.handlers[routeKey(r.Method, r.URL.Path)]
	a.mu.Unlock()

	if fn == nil {
		a.t.Errorf("the fake API got a request nobody registered: %s %s", r.Method, r.URL.Path)
		writeJSON(w, http.StatusNotFound, "application/problem+json",
			apiclient.ProblemError{Status: http.StatusNotFound, Title: "Not Found"})
		return
	}
	fn(w, r)
}

// writeJSON answers with body encoded as JSON under the given media type.
func writeJSON(w http.ResponseWriter, status int, contentType string, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---- the stub authorization server ----

// authCall is what the stub saw of one request.
type authCall struct {
	Method        string
	Path          string
	Query         url.Values
	Apikey        string
	Authorization string
	Form          url.Values
}

// stubAuth stands in for Supabase's OAuth 2.1 server: discovery, a token
// endpoint that exchanges the code and refreshes with rotation, and logout.
// It records every call, so a test asserts what the CLI sent.
type stubAuth struct {
	*httptest.Server

	mu    sync.Mutex
	calls []authCall

	// valid holds the refresh tokens the token endpoint honours. A refresh
	// spends its token and adds the one it issues, like Supabase.
	valid  map[string]bool
	minted int

	// refuseRefresh makes every refresh answer invalid_grant, as for a
	// session the server no longer knows.
	refuseRefresh bool

	// logoutStatus is what logout answers; zero means 204.
	logoutStatus int
}

func newStubAuth(t *testing.T) *stubAuth {
	t.Helper()
	s := &stubAuth{valid: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// accept registers refresh tokens the token endpoint honours.
func (s *stubAuth) accept(refreshTokens ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rt := range refreshTokens {
		s.valid[rt] = true
	}
}

// refuseRefreshes makes every later refresh fail as an ended session does.
func (s *stubAuth) refuseRefreshes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuseRefresh = true
}

// failLogout makes logout answer with status.
func (s *stubAuth) failLogout(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logoutStatus = status
}

// callsTo returns the recorded calls to one path, oldest first.
func (s *stubAuth) callsTo(path string) []authCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []authCall
	for _, c := range s.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (s *stubAuth) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, authCall{
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.Query(),
		Apikey:        r.Header.Get("apikey"),
		Authorization: r.Header.Get("Authorization"),
		Form:          r.PostForm,
	})
	s.mu.Unlock()

	switch r.URL.Path {
	case stubDiscoveryPath:
		writeJSON(w, http.StatusOK, "application/json", map[string]any{
			"issuer":                           s.URL + "/auth/v1",
			"authorization_endpoint":           s.URL + stubAuthorizePath,
			"token_endpoint":                   s.URL + stubTokenPath,
			"code_challenge_methods_supported": []string{"S256"},
		})
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

func (s *stubAuth) serveToken(w http.ResponseWriter, r *http.Request) {
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("code") != testCode || r.PostForm.Get("client_id") != testClientID {
			invalidGrant(w)
			return
		}
		s.issue(w)
	case "refresh_token":
		s.mu.Lock()
		ok := s.valid[r.PostForm.Get("refresh_token")] && !s.refuseRefresh
		delete(s.valid, r.PostForm.Get("refresh_token"))
		s.mu.Unlock()
		if !ok {
			invalidGrant(w)
			return
		}
		s.issue(w)
	default:
		invalidGrant(w)
	}
}

// issue answers with the next token pair and honours its refresh token.
func (s *stubAuth) issue(w http.ResponseWriter) {
	s.mu.Lock()
	s.minted++
	access, refresh := fmt.Sprintf("access-%d", s.minted), fmt.Sprintf("refresh-%d", s.minted)
	s.valid[refresh] = true
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, "application/json", map[string]any{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    3600,
		"refresh_token": refresh,
	})
}

func invalidGrant(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, "application/json", map[string]string{"error": "invalid_grant"})
}
