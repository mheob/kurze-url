package auth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// ---- discovery ----

func TestDiscoverReadsTheEndpoints(t *testing.T) {
	srv := newAuthServer(t)

	// A trailing slash on the project URL must not produce "//auth/v1".
	got, err := Discover(context.Background(), srv.Client(), srv.URL+"/", testAPIKey)

	require.NoError(t, err)
	require.Equal(t, srv.endpoints(), got)
	calls := srv.callsTo(stubDiscoveryPath)
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodGet, calls[0].Method)
	require.Equal(t, testAPIKey, calls[0].Apikey)
}

func TestDiscoverRefusesWithoutS256(t *testing.T) {
	srv := newAuthServer(t)
	srv.methods = []string{"plain"}

	_, err := Discover(context.Background(), srv.Client(), srv.URL, testAPIKey)

	require.ErrorIs(t, err, ErrNoS256)
}

func TestDiscoverRefusesAnIncompleteDocument(t *testing.T) {
	cases := map[string]string{
		"no token endpoint":         `{"authorization_endpoint":"https://x.test/a","code_challenge_methods_supported":["S256"]}`,
		"no authorization endpoint": `{"token_endpoint":"https://x.test/t","code_challenge_methods_supported":["S256"]}`,
		"a relative endpoint":       `{"authorization_endpoint":"/a","token_endpoint":"https://x.test/t","code_challenge_methods_supported":["S256"]}`,
		"a non-http endpoint":       `{"authorization_endpoint":"javascript:alert(1)","token_endpoint":"https://x.test/t","code_challenge_methods_supported":["S256"]}`,
		"not json":                  `<html>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			_, err := Discover(context.Background(), srv.Client(), srv.URL, testAPIKey)

			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNoS256)
		})
	}
}

func TestDiscoverReportsAnUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	_, err := Discover(context.Background(), srv.Client(), srv.URL, testAPIKey)

	require.ErrorContains(t, err, "404")
}

// A redirect would re-send the request, and a token endpoint's body is a
// refresh token or a code verifier. None of the auth calls follows one.
func TestCallsRefuseRedirects(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(elsewhere.Close)

	srv := newAuthServer(t)
	srv.redirect = elsewhere.URL
	store := newStore(t, "stored-access", "stored-refresh", -time.Hour)
	session := newSession(srv, store)

	_, err := Discover(context.Background(), srv.Client(), srv.URL, testAPIKey)
	require.Error(t, err)

	err = Logout(context.Background(), srv.Client(), srv.URL, testAPIKey, "an-access-token")
	require.Error(t, err)

	_, err = session.Refresh(context.Background())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLoginRequired)

	require.Zero(t, hits.Load(), "a redirect was followed")
}

func TestDefaultPorts(t *testing.T) {
	require.Equal(t, []int{53682, 53683, 53684}, DefaultPorts)
	require.Equal(t, 5*time.Minute, DefaultLoginTimeout)
}

// ---- the listener ----

func TestBindSkipsABusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })
	busyPort := busy.Addr().(*net.TCPAddr).Port

	ln, port, err := bindFirstFree([]int{busyPort, 0})

	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	require.NotEqual(t, busyPort, port)
	require.Equal(t, ln.Addr().(*net.TCPAddr).Port, port)
}

func TestBindFailsWhenEveryPortIsBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })
	busyPort := busy.Addr().(*net.TCPAddr).Port

	ln, _, err := bindFirstFree([]int{busyPort})

	require.Nil(t, ln)
	require.ErrorIs(t, err, ErrPortsBusy)
}

func TestBindFailsWithoutAnyPort(t *testing.T) {
	_, _, err := bindFirstFree(nil)

	require.ErrorIs(t, err, ErrPortsBusy)
}

// The redirect target is a loopback address, so a listener on every
// interface would also be reachable from the network.
func TestBindListensOnTheLoopbackOnly(t *testing.T) {
	ln, _, err := bindFirstFree([]int{0})

	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	require.True(t, ln.Addr().(*net.TCPAddr).IP.Equal(net.IPv4(127, 0, 0, 1)))
}

// ---- the callback pages ----

func TestPagesAreSelfContained(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.DE} {
		for _, success := range []bool{true, false} {
			t.Run(string(lang)+"/"+strconv.FormatBool(success), func(t *testing.T) {
				cat := i18n.Load(lang)

				body, err := renderPage(cat, success)

				require.NoError(t, err)
				html := string(body)
				require.Contains(t, html, `<html lang="`+string(lang)+`">`)
				require.Equal(t, 1, strings.Count(html, "<main"))
				require.Equal(t, 1, strings.Count(html, "<h1"))
				require.Contains(t, html, "prefers-color-scheme: dark")
				for _, external := range []string{"http://", "https://", "src=", "href=", "<script", "<link"} {
					require.NotContains(t, html, external)
				}
			})
		}
	}
}

func TestPagesCarryTheSpecsCopy(t *testing.T) {
	cases := []struct {
		lang    i18n.Lang
		success bool
		want    []string
	}{
		{i18n.EN, true, []string{"Signed in", "The CLI is now connected to your account. You can close this window and continue in the terminal."}},
		{i18n.DE, true, []string{"Angemeldet", "Die CLI ist jetzt mit deinem Konto verbunden. Du kannst dieses Fenster schließen und im Terminal weitermachen."}},
		{i18n.EN, false, []string{"Sign-in cancelled", "The terminal says how to continue."}},
		{i18n.DE, false, []string{"Anmeldung abgebrochen", "Im Terminal steht, wie es weitergeht."}},
	}
	for _, tc := range cases {
		body, err := renderPage(i18n.Load(tc.lang), tc.success)
		require.NoError(t, err)
		for _, want := range tc.want {
			require.Contains(t, string(body), want)
		}
	}
}

// ---- the login flow ----

// callbackResult is what the fake browser got back from the loopback
// listener: the page the person would have seen.
type callbackResult struct {
	Status int
	Body   string
	Err    error
}

// fakeBrowser follows the authorize URL the way a signed-in browser would
// after consenting: it sends the redirect to the loopback listener with the
// code and the state, and keeps the page it gets back.
type fakeBrowser struct {
	srv *authServer

	// mutate rewrites the callback's query before it is sent, which is how a
	// test makes the browser come back with a bad state or an error.
	mutate func(url.Values)

	// before runs with the redirect URI ahead of the callback.
	before func(redirectURI string)

	// authURL is the URL Login asked to open. Login calls open on the
	// goroutine that called it, so reading it after Login returns is safe.
	authURL string

	result chan callbackResult
}

func newFakeBrowser(srv *authServer) *fakeBrowser {
	return &fakeBrowser{srv: srv, result: make(chan callbackResult, 1)}
}

// open is the Flow's OpenBrowser.
func (b *fakeBrowser) open(authURL string) error {
	b.authURL = authURL
	go func() { b.result <- b.follow(authURL) }()
	return nil
}

// follow does the browser's part. It never calls the testing package, because
// it runs on a goroutine of its own.
func (b *fakeBrowser) follow(authURL string) callbackResult {
	u, err := url.Parse(authURL)
	if err != nil {
		return callbackResult{Err: err}
	}
	q := u.Query()
	b.srv.expectChallenge(q.Get("code_challenge"))
	if b.before != nil {
		b.before(q.Get("redirect_uri"))
	}
	callback := url.Values{"code": {testCode}, "state": {q.Get("state")}}
	if b.mutate != nil {
		b.mutate(callback)
	}
	return get(q.Get("redirect_uri") + "?" + callback.Encode())
}

// page waits for the browser to have received its page.
func (b *fakeBrowser) page(t *testing.T) callbackResult {
	t.Helper()
	select {
	case r := <-b.result:
		require.NoError(t, r.Err)
		return r
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the browser never got a page")
		return callbackResult{}
	}
}

// get is an HTTP GET that does not keep the connection alive, so that the
// listener's shutdown has nothing left to wait for.
func get(rawURL string) callbackResult {
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return callbackResult{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return callbackResult{Status: resp.StatusCode, Body: string(body), Err: err}
}

func newFlow(srv *authServer, open func(string) error, stderr io.Writer) Flow {
	return Flow{
		Endpoints:      srv.endpoints(),
		ClientID:       testClientID,
		PublishableKey: testAPIKey,
		HTTPClient:     srv.Client(),
		Ports:          []int{0},
		Timeout:        10 * time.Second,
		OpenBrowser:    open,
		Stderr:         stderr,
		Catalog:        i18n.Load(i18n.EN),
	}
}

// requireAuthorizeURL checks what the browser is asked to open and returns
// the redirect URI it carries.
func requireAuthorizeURL(t *testing.T, srv *authServer, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, srv.endpoints().Authorization, u.Scheme+"://"+u.Host+u.Path)

	q := u.Query()
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, testClientID, q.Get("client_id"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	// A SHA-256 digest, base64url-encoded without padding, is 43 characters.
	require.Len(t, q.Get("code_challenge"), 43)
	// 32 random bytes, base64url-encoded without padding.
	require.Len(t, q.Get("state"), 43)

	redirect, err := url.Parse(q.Get("redirect_uri"))
	require.NoError(t, err)
	require.Equal(t, "http", redirect.Scheme)
	require.Equal(t, "127.0.0.1", redirect.Hostname(), "the redirect must name the loopback address, never localhost")
	require.Equal(t, "/callback", redirect.Path)
	_, err = strconv.Atoi(redirect.Port())
	require.NoError(t, err)
	return redirect
}

// requireListenerClosed checks that nothing answers on the redirect URI's
// port any more.
func requireListenerClosed(t *testing.T, redirect *url.URL) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", redirect.Host, time.Second)
	if err == nil {
		_ = conn.Close()
	}
	require.Error(t, err, "the loopback listener is still open")
}

func TestLoginExchangesTheCode(t *testing.T) {
	srv := newAuthServer(t)
	browser := newFakeBrowser(srv)
	var stderr syncBuffer
	flow := newFlow(srv, browser.open, &stderr)

	tokens, err := flow.Login(context.Background())

	require.NoError(t, err)
	access, refresh := pair(1)
	sameSecret(t, access, tokens.AccessToken, "the access token")
	sameSecret(t, refresh, tokens.RefreshToken, "the refresh token")
	require.WithinDuration(t, time.Now().Add(time.Hour), tokens.ExpiresAt, time.Minute)

	redirect := requireAuthorizeURL(t, srv, browser.authURL)
	calls := srv.tokenCalls()
	require.Len(t, calls, 1)
	form := calls[0].Form
	require.Equal(t, "authorization_code", form.Get("grant_type"))
	require.Equal(t, testClientID, form.Get("client_id"), "a public client sends its ID in the body")
	require.Equal(t, redirect.String(), form.Get("redirect_uri"))
	require.GreaterOrEqual(t, len(form.Get("code_verifier")), 43)
	require.False(t, form.Has("client_secret"))
	require.Empty(t, calls[0].Authorization, "a public client sends no basic-auth header")
	require.Equal(t, testAPIKey, calls[0].Apikey)

	page := browser.page(t)
	require.Equal(t, http.StatusOK, page.Status)
	require.Contains(t, page.Body, "Signed in")

	out := stderr.String()
	require.Contains(t, out, i18n.Load(i18n.EN).T("login.waiting"))
	require.NotContains(t, out, browser.authURL, "the URL is printed only when the browser could not be opened")
	for _, secret := range []string{testCode, access, refresh} {
		require.NotContains(t, out, secret)
	}
	requireListenerClosed(t, redirect)
}

func TestLoginPrintsTheURLWhenNoBrowserOpens(t *testing.T) {
	srv := newAuthServer(t)
	var stderr syncBuffer
	flow := newFlow(srv, func(string) error { return errBrowser }, &stderr)

	// The person copies the URL out of the terminal; this goroutine does so.
	urlPattern := regexp.MustCompile(`https?://\S+`)
	result := make(chan callbackResult, 1)
	browser := newFakeBrowser(srv)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if found := urlPattern.FindString(stderr.String()); found != "" {
				result <- browser.follow(found)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		result <- callbackResult{Err: context.DeadlineExceeded}
	}()

	tokens, err := flow.Login(context.Background())

	require.NoError(t, err)
	access, _ := pair(1)
	sameSecret(t, access, tokens.AccessToken, "the access token")
	out := stderr.String()
	cat := i18n.Load(i18n.EN)
	require.Contains(t, out, cat.T("login.waiting"))
	require.True(t, strings.Index(out, cat.T("login.waiting")) < strings.Index(out, "http"),
		"the waiting line comes before the URL")
	printed := urlPattern.FindString(out)
	requireAuthorizeURL(t, srv, printed)
	require.Contains(t, out, strings.TrimSuffix(cat.T("login.openManually", i18n.Args{"url": printed}), printed))
	require.Equal(t, http.StatusOK, (<-result).Status)
}

func TestLoginRefusesAStateMismatch(t *testing.T) {
	srv := newAuthServer(t)
	browser := newFakeBrowser(srv)
	browser.mutate = func(q url.Values) { q.Set("state", "not-the-state") }
	flow := newFlow(srv, browser.open, io.Discard)

	_, err := flow.Login(context.Background())

	require.ErrorIs(t, err, ErrStateMismatch)
	page := browser.page(t)
	require.Contains(t, page.Body, "Sign-in cancelled")
	require.Equal(t, http.StatusBadRequest, page.Status)
	require.Empty(t, srv.tokenCalls(), "a code from a mismatched callback must not be exchanged")
	requireListenerClosed(t, requireAuthorizeURL(t, srv, browser.authURL))
}

func TestLoginReportsDenial(t *testing.T) {
	srv := newAuthServer(t)
	browser := newFakeBrowser(srv)
	browser.mutate = func(q url.Values) {
		q.Del("code")
		q.Set("error", "access_denied")
		q.Set("error_description", "The user denied the request")
	}
	flow := newFlow(srv, browser.open, io.Discard)

	_, err := flow.Login(context.Background())

	require.ErrorIs(t, err, ErrDenied)
	page := browser.page(t)
	require.Contains(t, page.Body, "Sign-in cancelled")
	require.Empty(t, srv.tokenCalls())
}

func TestLoginReportsAnExchangeFailure(t *testing.T) {
	srv := newAuthServer(t)
	srv.tokenStatus = http.StatusServiceUnavailable
	browser := newFakeBrowser(srv)
	flow := newFlow(srv, browser.open, io.Discard)

	_, err := flow.Login(context.Background())

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDenied)
	require.NotErrorIs(t, err, ErrStateMismatch)
	require.NotErrorIs(t, err, ErrTimeout)
	require.Contains(t, err.Error(), "503")
	require.Contains(t, browser.page(t).Body, "Sign-in cancelled")

	calls := srv.tokenCalls()
	require.Len(t, calls, 1)
	for _, secret := range []string{testCode, calls[0].Form.Get("code_verifier")} {
		require.NotEmpty(t, secret)
		require.NotContains(t, err.Error(), secret)
	}
}

func TestLoginTimesOut(t *testing.T) {
	srv := newAuthServer(t)
	var authURL string
	flow := newFlow(srv, func(raw string) error { authURL = raw; return nil }, io.Discard)
	flow.Timeout = 50 * time.Millisecond

	_, err := flow.Login(context.Background())

	require.ErrorIs(t, err, ErrTimeout)
	requireListenerClosed(t, requireAuthorizeURL(t, srv, authURL))
}

func TestLoginStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := newAuthServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var authURL string
	flow := newFlow(srv, func(raw string) error { authURL = raw; cancel(); return nil }, io.Discard)

	_, err := flow.Login(ctx)

	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrTimeout)
	requireListenerClosed(t, requireAuthorizeURL(t, srv, authURL))
}

func TestLoginPageIsGerman(t *testing.T) {
	srv := newAuthServer(t)
	browser := newFakeBrowser(srv)
	flow := newFlow(srv, browser.open, io.Discard)
	flow.Catalog = i18n.Load(i18n.DE)

	_, err := flow.Login(context.Background())

	require.NoError(t, err)
	page := browser.page(t)
	require.Contains(t, page.Body, `lang="de"`)
	require.Contains(t, page.Body, "Angemeldet")
}

// A browser asks for /favicon.ico, an extension may prefetch, and anything
// else on the machine can reach a loopback port. None of it may end the flow
// or be answered as a callback.
func TestLoginIgnoresRequestsThatAreNotTheCallback(t *testing.T) {
	srv := newAuthServer(t)
	browser := newFakeBrowser(srv)
	var strays []int
	browser.before = func(redirectURI string) {
		base := strings.TrimSuffix(redirectURI, "/callback")
		strays = append(strays,
			get(base+"/favicon.ico").Status,
			get(base+"/callback/extra").Status,
			get(base+"/").Status,
			post(base+"/callback?state=whatever&code=whatever"),
		)
	}
	flow := newFlow(srv, browser.open, io.Discard)

	_, err := flow.Login(context.Background())

	require.NoError(t, err)
	browser.page(t)
	require.Equal(t, []int{404, 404, 404, 404}, strays)
	require.Len(t, srv.tokenCalls(), 1)
}

// post sends a POST with no body and returns the status.
func post(rawURL string) int {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Post(rawURL, "text/plain", strings.NewReader(""))
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// errBrowser is what a machine without a browser makes OpenBrowser return.
var errBrowser = errors.New("no browser here")
