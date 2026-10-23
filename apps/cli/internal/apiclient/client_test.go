package apiclient_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// accessToken and freshToken are the two values every test token source
// hands out. They are long and unusual on purpose, so that a test asserting a
// message does not contain one cannot pass by coincidence.
const (
	accessToken = "access-token-0f3a9c71d2"
	freshToken  = "fresh-token-77be10c4a8"
)

// seenRequest is what the fake API recorded about one request. The handler
// goroutine only records; every assertion runs on the test's own goroutine,
// because require's FailNow must not be called from any other.
type seenRequest struct {
	Method string
	// Path is the escaped path, so a test can tell "a/b" sent as one segment
	// from "a/b" sent as two.
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// fakeAPI is an httptest server that records each request and lets the test
// decide the answer by call number.
type fakeAPI struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

// newFakeAPI starts the server and registers its shutdown. handle receives
// the 1-based call number and the recorded request and writes the answer.
func newFakeAPI(t *testing.T, handle func(call int, r seenRequest, w http.ResponseWriter)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := seenRequest{
			Method: r.Method,
			Path:   r.URL.EscapedPath(),
			Query:  r.URL.Query(),
			Header: r.Header.Clone(),
			Body:   body,
		}
		f.mu.Lock()
		f.seen = append(f.seen, rec)
		call := len(f.seen)
		f.mu.Unlock()
		handle(call, rec, w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// requests returns a copy of everything recorded so far.
func (f *fakeAPI) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

// only returns the single recorded request, failing when there is not
// exactly one.
func (f *fakeAPI) only(t *testing.T) seenRequest {
	t.Helper()
	got := f.requests()
	require.Len(t, got, 1)
	return got[0]
}

// fakeTokens is a TokenSource that counts its calls and can be told to fail.
type fakeTokens struct {
	accessErr, refreshErr     error
	accessCalls, refreshCalls atomic.Int32
}

func (f *fakeTokens) AccessToken(context.Context) (string, error) {
	f.accessCalls.Add(1)
	if f.accessErr != nil {
		return "", f.accessErr
	}
	return accessToken, nil
}

func (f *fakeTokens) Refresh(context.Context) (string, error) {
	f.refreshCalls.Add(1)
	if f.refreshErr != nil {
		return "", f.refreshErr
	}
	return freshToken, nil
}

// newClient returns a client for the fake with the default HTTP client, so a
// test also exercises what New builds when it is given none.
func newClient(f *fakeAPI, tokens apiclient.TokenSource) *apiclient.Client {
	return apiclient.New(apiclient.Options{
		BaseURL:   f.srv.URL,
		Tokens:    tokens,
		UserAgent: "kurze-url/test",
	})
}

// writeJSON answers with a JSON document and the given status.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// writeProblem answers the way the API's Huma layer does for an error.
func writeProblem(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// asProblem unwraps err into the typed error, failing the test when it is
// anything else.
func asProblem(t *testing.T, err error) *apiclient.ProblemError {
	t.Helper()
	var perr *apiclient.ProblemError
	require.ErrorAs(t, err, &perr)
	return perr
}

func TestRequestsCarryTheBearerTokenAndBypass(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{"user_id":"u-1","email":"a@example.org","memberships":[]}`)
	})
	client := apiclient.New(apiclient.Options{
		BaseURL:      f.srv.URL,
		HTTPClient:   f.srv.Client(),
		Tokens:       &fakeTokens{},
		VercelBypass: "bypass-secret",
		UserAgent:    "kurze-url/1.2.3",
	})

	_, err := client.Me(t.Context())

	require.NoError(t, err)
	got := f.only(t)
	require.Equal(t, http.MethodGet, got.Method)
	require.Equal(t, "/v1/me", got.Path)
	require.Equal(t, "Bearer "+accessToken, got.Header.Get("Authorization"))
	require.Equal(t, "kurze-url/1.2.3", got.Header.Get("User-Agent"))
	require.Equal(t, "application/json", got.Header.Get("Accept"))
	require.Equal(t, "bypass-secret", got.Header.Get("x-vercel-protection-bypass"))
}

func TestNoBypassHeaderIsSentWhenNoneIsConfigured(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := newClient(f, &fakeTokens{}).Me(t.Context())

	require.NoError(t, err)
	require.Empty(t, f.only(t).Header.Values("x-vercel-protection-bypass"),
		"an empty bypass value must not be sent as an empty header")
}

func TestMeDecodesTheMemberships(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{
			"user_id": "u-1",
			"email": "a@example.org",
			"is_maintainer": false,
			"memberships": [
				{"team_id": "t-1", "name": "SV Grünwald", "slug": "sv-gruenwald", "role": "admin"},
				{"team_id": "t-2", "name": "Chor", "slug": "chor", "role": "viewer"}
			]
		}`)
	})

	me, err := newClient(f, &fakeTokens{}).Me(t.Context())

	require.NoError(t, err)
	require.Equal(t, apiclient.Me{
		UserID: "u-1",
		Email:  "a@example.org",
		Memberships: []apiclient.Membership{
			{TeamID: "t-1", Name: "SV Grünwald", Slug: "sv-gruenwald", Role: "admin"},
			{TeamID: "t-2", Name: "Chor", Slug: "chor", Role: "viewer"},
		},
	}, me)
}

func TestATrailingSlashInTheBaseURLIsHarmless(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})
	client := apiclient.New(apiclient.Options{BaseURL: f.srv.URL + "/", Tokens: &fakeTokens{}})

	_, err := client.Me(t.Context())

	require.NoError(t, err)
	require.Equal(t, "/v1/me", f.only(t).Path)
}

func TestA401RefreshesOnceAndRetries(t *testing.T) {
	const wantBody = `{"destination_url":"https://example.org/sommerfest","slug":"fest"}`
	f := newFakeAPI(t, func(call int, _ seenRequest, w http.ResponseWriter) {
		if call == 1 {
			writeProblem(w, http.StatusUnauthorized, `{"title":"Unauthorized","status":401,"detail":"token expired"}`)
			return
		}
		writeJSON(w, http.StatusCreated, `{"id":"l-1","slug":"fest","short_url":"https://go.kurze-url.app/fest"}`)
	})
	tokens := &fakeTokens{}

	link, err := newClient(f, tokens).CreateLink(t.Context(), "team-1", apiclient.CreateLinkRequest{
		DestinationURL: "https://example.org/sommerfest",
		Slug:           "fest",
	})

	require.NoError(t, err)
	require.Equal(t, "l-1", link.ID)
	require.Equal(t, "https://go.kurze-url.app/fest", link.ShortURL)
	require.EqualValues(t, 1, tokens.refreshCalls.Load(), "a 401 triggers exactly one refresh")
	require.EqualValues(t, 1, tokens.accessCalls.Load(), "the retry uses the refreshed token, not a second lookup")
	got := f.requests()
	require.Len(t, got, 2)
	require.Equal(t, "Bearer "+accessToken, got[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer "+freshToken, got[1].Header.Get("Authorization"))
	require.JSONEq(t, wantBody, string(got[0].Body))
	require.Equal(t, got[0].Body, got[1].Body,
		"the retry must resend the body byte for byte; a consumed reader would send nothing")
	require.Equal(t, http.MethodPost, got[1].Method)
	require.Equal(t, got[0].Path, got[1].Path)
}

func TestASecond401IsReturned(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeProblem(w, http.StatusUnauthorized, `{"title":"Unauthorized","status":401,"detail":"no"}`)
	})
	tokens := &fakeTokens{}

	_, err := newClient(f, tokens).Me(t.Context())

	perr := asProblem(t, err)
	require.Equal(t, http.StatusUnauthorized, perr.Status)
	require.EqualValues(t, 1, tokens.refreshCalls.Load(), "a second 401 must not refresh again")
	require.Len(t, f.requests(), 2)
}

func TestA500IsNotRetried(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeProblem(w, http.StatusInternalServerError, `{"title":"Internal Server Error","status":500}`)
	})
	tokens := &fakeTokens{}

	_, err := newClient(f, tokens).CreateLink(t.Context(), "team-1", apiclient.CreateLinkRequest{
		DestinationURL: "https://example.org/",
	})

	require.Equal(t, http.StatusInternalServerError, asProblem(t, err).Status)
	require.Len(t, f.requests(), 1, "a POST that failed with a 5xx may have been applied, so it is never repeated")
	require.EqualValues(t, 0, tokens.refreshCalls.Load())
}

// failingTransport fails every attempt and counts them, so a test can tell
// "tried once and gave up" from "never tried" and from "tried again".
type failingTransport struct{ attempts atomic.Int32 }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.attempts.Add(1)
	return nil, errors.New("connection reset by peer")
}

// bothVerbs are the two calls every no-retry test runs: a GET, and the POST
// that must never be sent twice because the first one may have been applied.
var bothVerbs = []struct {
	name string
	call func(ctx context.Context, c *apiclient.Client) error
}{
	{"GET", func(ctx context.Context, c *apiclient.Client) error {
		_, err := c.Me(ctx)
		return err
	}},
	{"POST", func(ctx context.Context, c *apiclient.Client) error {
		_, err := c.CreateLink(ctx, "team-1", apiclient.CreateLinkRequest{DestinationURL: "https://example.org/"})
		return err
	}},
}

func TestANetworkErrorIsNotRetried(t *testing.T) {
	for _, verb := range bothVerbs {
		t.Run("the connection dies after the request arrived/"+verb.name, func(t *testing.T) {
			// The dangerous case: the server has the request, so a POST may
			// well have been applied, and the client never sees an answer.
			f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			})
			tokens := &fakeTokens{}

			err := verb.call(t.Context(), newClient(f, tokens))

			require.Error(t, err)
			require.Len(t, f.requests(), 1, "a request that may have been applied is never sent again")
			require.EqualValues(t, 0, tokens.refreshCalls.Load())
			var perr *apiclient.ProblemError
			require.False(t, errors.As(err, &perr), "a transport failure is not a problem document")
		})

		t.Run("the transport fails/"+verb.name, func(t *testing.T) {
			transport := &failingTransport{}
			client := apiclient.New(apiclient.Options{
				BaseURL:    "http://api.invalid",
				HTTPClient: &http.Client{Transport: transport},
				Tokens:     &fakeTokens{},
			})

			err := verb.call(t.Context(), client)

			require.Error(t, err)
			require.EqualValues(t, 1, transport.attempts.Load(), "one attempt, then the error is the caller's")
		})
	}
}

func TestATimeoutIsNotRetried(t *testing.T) {
	for _, verb := range bothVerbs {
		t.Run(verb.name, func(t *testing.T) {
			release := make(chan struct{})
			f := newFakeAPI(t, func(int, seenRequest, http.ResponseWriter) { <-release })
			// Registered after the server's own cleanup, so it runs first:
			// the server cannot close while a handler is still blocked.
			t.Cleanup(func() { close(release) })
			client := apiclient.New(apiclient.Options{
				BaseURL:    f.srv.URL,
				HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
				Tokens:     &fakeTokens{},
			})

			err := verb.call(t.Context(), client)

			var netErr net.Error
			require.ErrorAs(t, err, &netErr)
			require.True(t, netErr.Timeout(), "the error must say it was a timeout: %v", err)
			require.Len(t, f.requests(), 1, "a request that timed out may have been applied, so it is never sent again")
		})
	}
}

// redirectingAPI starts two servers: the API, which answers every request
// with a redirect of the given status, and a stranger it redirects to. The
// stranger answers 200 with a JSON document a careless client would happily
// decode as a Link.
func redirectingAPI(t *testing.T, status int) (api, stranger *fakeAPI) {
	t.Helper()
	stranger = newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{"id":"from-the-stranger"}`)
	})
	api = newFakeAPI(t, func(_ int, r seenRequest, w http.ResponseWriter) {
		w.Header().Set("Location", stranger.srv.URL+r.Path)
		w.WriteHeader(status)
	})
	return api, stranger
}

func TestARedirectIsReturnedAndNeverFollowed(t *testing.T) {
	// A followed redirect forwards x-vercel-protection-bypass to the new
	// host (Go strips only Authorization), turns a POST into a GET on 301,
	// 302 and 303 so the answer of an unrelated page is decoded as a Link,
	// and re-sends the POST body on 307 and 308. None of that is wanted from
	// an API that has no reason to redirect.
	for _, status := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		for _, verb := range bothVerbs {
			for _, supplied := range []bool{false, true} {
				name := fmt.Sprintf("%d/%s/supplied client %t", status, verb.name, supplied)
				t.Run(name, func(t *testing.T) {
					api, stranger := redirectingAPI(t, status)
					opts := apiclient.Options{
						BaseURL:      api.srv.URL,
						Tokens:       &fakeTokens{},
						VercelBypass: "bypass-secret",
					}
					if supplied {
						opts.HTTPClient = api.srv.Client()
					}

					err := verb.call(t.Context(), apiclient.New(opts))

					require.Equal(t, status, asProblem(t, err).Status)
					require.Len(t, api.requests(), 1)
					require.Empty(t, stranger.requests(), "the redirect target must never see a request")
					if supplied {
						require.Nil(t, opts.HTTPClient.CheckRedirect, "the caller's own client must not be modified")
					}
				})
			}
		}
	}
}

func TestACallersOwnRedirectPolicyIsLeftAlone(t *testing.T) {
	// The default is only for a client that has no opinion. One that sets
	// CheckRedirect has made the decision itself.
	api, stranger := redirectingAPI(t, http.StatusFound)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}

	_, err := apiclient.New(apiclient.Options{BaseURL: api.srv.URL, HTTPClient: hc, Tokens: &fakeTokens{}}).Me(t.Context())

	require.NoError(t, err)
	require.Len(t, stranger.requests(), 1)
}

func TestAFailedRefreshIsWrapped(t *testing.T) {
	errRevoked := errors.New("refresh token revoked")
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeProblem(w, http.StatusUnauthorized, `{"title":"Unauthorized","status":401}`)
	})
	tokens := &fakeTokens{refreshErr: errRevoked}

	_, err := newClient(f, tokens).Me(t.Context())

	require.ErrorIs(t, err, errRevoked)
	require.Len(t, f.requests(), 1, "nothing is resent when the refresh failed")
}

func TestAFailedAccessTokenLookupIsWrapped(t *testing.T) {
	errLoggedOut := errors.New("not logged in")
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := newClient(f, &fakeTokens{accessErr: errLoggedOut}).Me(t.Context())

	require.ErrorIs(t, err, errLoggedOut)
	require.Empty(t, f.requests(), "no request goes out without a token")
}

func TestARequestWithoutATokenSourceFailsBeforeSending(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := apiclient.New(apiclient.Options{BaseURL: f.srv.URL}).Me(t.Context())

	require.Error(t, err)
	require.Empty(t, f.requests())
}

func TestProblemJSONIsDecoded(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeProblem(w, http.StatusUnprocessableEntity, `{
			"title": "Unprocessable Entity",
			"status": 422,
			"detail": "validation failed",
			"errors": [
				{"message": "too faint", "location": "query.fg", "value": "low_contrast"},
				{"message": "blocks links", "location": "path.domain_id", "value": 3}
			]
		}`)
	})

	_, err := newClient(f, &fakeTokens{}).LinkQR(t.Context(), "link-1", apiclient.QRQuery{FG: "eeeeee"})

	perr := asProblem(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, perr.Status)
	require.Equal(t, "Unprocessable Entity", perr.Title)
	require.Equal(t, "validation failed", perr.Detail)
	require.Len(t, perr.Errors, 2)
	require.Equal(t, "too faint", perr.Errors[0].Message)

	for _, tc := range []struct {
		name            string
		location, value string
		want            bool
	}{
		{"the exact location and value", "query.fg", "low_contrast", true},
		{"any value when none is given", "query.fg", "", true},
		{"a different value at that location", "query.fg", "other", false},
		{"a location nobody reported", "query.bg", "", false},
		{"a value that belongs to another location", "query.bg", "low_contrast", false},
		{"a number, compared as it was sent", "path.domain_id", "3", true},
		{"a number that differs", "path.domain_id", "4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, perr.HasDetail(tc.location, tc.value))
		})
	}
}

func TestHasDetailOnANilProblemIsFalse(t *testing.T) {
	var perr *apiclient.ProblemError

	require.False(t, perr.HasDetail("query.fg", ""))
}

func TestANonJSONErrorBodyKeepsTheStatus(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>Bad gateway</body></html>"))
	})

	_, err := newClient(f, &fakeTokens{}).Me(t.Context())

	perr := asProblem(t, err)
	require.Equal(t, http.StatusBadGateway, perr.Status)
	require.Empty(t, perr.Detail)
	require.Empty(t, perr.Errors)
	require.Contains(t, perr.Error(), "502", "the message names the status even with no document behind it")
}

func TestTheStatusComesFromTheResponseNotTheBody(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeProblem(w, http.StatusNotFound, `{"title":"Not Found","status":200}`)
	})

	_, err := newClient(f, &fakeTokens{}).Me(t.Context())

	require.Equal(t, http.StatusNotFound, asProblem(t, err).Status)
}

func TestASuccessBodyThatIsNotJSONIsAnError(t *testing.T) {
	// Vercel answers a cancelled preview deployment with an HTML page and a
	// 200, so "2xx" alone does not mean the API spoke.
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>Deployment was cancelled</html>"))
	})

	_, err := newClient(f, &fakeTokens{}).Me(t.Context())

	require.Error(t, err)
	var perr *apiclient.ProblemError
	require.False(t, errors.As(err, &perr))
}

func TestNoErrorCarriesATokenOrTheBypassValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		handle func(call int, r seenRequest, w http.ResponseWriter)
		stop   bool
	}{
		{"a problem document", func(_ int, _ seenRequest, w http.ResponseWriter) {
			writeProblem(w, http.StatusForbidden, `{"title":"Forbidden","status":403,"detail":"no"}`)
		}, false},
		{"an undecodable success", func(_ int, _ seenRequest, w http.ResponseWriter) {
			_, _ = w.Write([]byte("not json"))
		}, false},
		{"a refused connection", func(int, seenRequest, http.ResponseWriter) {}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tc.handle)
			client := apiclient.New(apiclient.Options{
				BaseURL:      f.srv.URL,
				Tokens:       &fakeTokens{},
				VercelBypass: "bypass-secret-5d1e",
			})
			if tc.stop {
				f.srv.Close()
			}

			_, err := client.Me(t.Context())

			require.Error(t, err)
			for _, secret := range []string{accessToken, freshToken, "bypass-secret-5d1e"} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestFoldersPagesUntilTotalCount(t *testing.T) {
	f := newFakeAPI(t, func(_ int, r seenRequest, w http.ResponseWriter) {
		page, _ := strconv.Atoi(r.Query.Get("page"))
		start, end := (page-1)*100, min(page*100, 150)
		items := make([]string, 0, 100)
		for i := start; i < end; i++ {
			items = append(items, fmt.Sprintf(`{"id":"f-%d","name":"Ordner %d"}`, i, i))
		}
		writeJSON(w, http.StatusOK, fmt.Sprintf(
			`{"items":[%s],"page":%d,"per_page":100,"total_count":150}`, strings.Join(items, ","), page))
	})

	folders, err := newClient(f, &fakeTokens{}).Folders(t.Context(), "team-1")

	require.NoError(t, err)
	require.Len(t, folders, 150)
	require.Equal(t, apiclient.Folder{ID: "f-0", Name: "Ordner 0"}, folders[0])
	require.Equal(t, apiclient.Folder{ID: "f-149", Name: "Ordner 149"}, folders[149])
	got := f.requests()
	require.Len(t, got, 2, "150 items at 100 a page is two requests, and no third to confirm the end")
	for i, r := range got {
		require.Equal(t, "/v1/teams/team-1/folders", r.Path)
		require.Equal(t, strconv.Itoa(i+1), r.Query.Get("page"))
		require.Equal(t, "100", r.Query.Get("per_page"))
	}
}

func TestDomainsStopsAtAnEmptyPage(t *testing.T) {
	// total_count promises more than the server delivers, so only the empty
	// page ends the loop.
	f := newFakeAPI(t, func(call int, _ seenRequest, w http.ResponseWriter) {
		if call == 1 {
			writeJSON(w, http.StatusOK, `{"items":[{"id":"d-1","hostname":"go.verein.test","verification_status":"verified"}],
				"page":1,"per_page":100,"total_count":500}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"items":null,"page":2,"per_page":100,"total_count":500}`)
	})

	domains, err := newClient(f, &fakeTokens{}).Domains(t.Context(), "team-1")

	require.NoError(t, err)
	require.Equal(t, []apiclient.Domain{{ID: "d-1", Hostname: "go.verein.test", VerificationStatus: "verified"}}, domains)
	require.Len(t, f.requests(), 2)
	require.Equal(t, "/v1/teams/team-1/domains", f.requests()[0].Path)
}

func TestAnEmptyListIsAnEmptySliceNotNil(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{"items":null,"page":1,"per_page":100,"total_count":0}`)
	})

	folders, err := newClient(f, &fakeTokens{}).Folders(t.Context(), "team-1")

	require.NoError(t, err)
	require.NotNil(t, folders, "a command printing JSON must print [] and not null")
	require.Empty(t, folders)
	require.Len(t, f.requests(), 1)
}

func TestAnEmptyIDIsRefusedBeforeAnyRequest(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})
	client := newClient(f, &fakeTokens{})

	_, errFolders := client.Folders(t.Context(), "")
	_, errStats := client.LinkStats(t.Context(), "", "", "")
	_, errQR := client.LinkQR(t.Context(), "", apiclient.QRQuery{})

	require.Error(t, errFolders)
	require.Error(t, errStats)
	require.Error(t, errQR)
	require.Empty(t, f.requests(), "an empty ID would otherwise hit /v1/teams//folders, a different route")
}

func TestAnIDIsEscapedIntoOneSegment(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{}`)
	})

	_, err := newClient(f, &fakeTokens{}).Folders(t.Context(), "a/b?c")

	require.NoError(t, err)
	require.Equal(t, "/v1/teams/a%2Fb%3Fc/folders", f.requests()[0].Path)
}

func TestCreateLinkSendsOnlyTheFieldsItWasGiven(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusCreated, `{"id":"l-1"}`)
	})
	client := newClient(f, &fakeTokens{})

	_, err := client.CreateLink(t.Context(), "team-1", apiclient.CreateLinkRequest{DestinationURL: "https://example.org/"})
	require.NoError(t, err)
	_, err = client.CreateLink(t.Context(), "team-1", apiclient.CreateLinkRequest{
		DestinationURL: "https://example.org/",
		Slug:           "fest",
		FolderID:       "f-1",
		DomainID:       "d-1",
	})
	require.NoError(t, err)

	got := f.requests()
	require.Len(t, got, 2)
	require.Equal(t, http.MethodPost, got[0].Method)
	require.Equal(t, "/v1/teams/team-1/links", got[0].Path)
	require.Equal(t, "application/json", got[0].Header.Get("Content-Type"))
	require.JSONEq(t, `{"destination_url":"https://example.org/"}`, string(got[0].Body))
	require.JSONEq(t,
		`{"destination_url":"https://example.org/","slug":"fest","folder_id":"f-1","domain_id":"d-1"}`,
		string(got[1].Body))
}

func TestListLinksOmitsEmptyQueryValues(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{
			"items": [{"id":"l-1","team_id":"t-1","domain_id":"d-1","hostname":"go.kurze-url.app",
				"slug":"fest","short_url":"https://go.kurze-url.app/fest",
				"destination_url":"https://example.org/","state":"active"}],
			"page": 2, "per_page": 10, "total_count": 11
		}`)
	})
	client := newClient(f, &fakeTokens{})

	page, err := client.ListLinks(t.Context(), "team-1", apiclient.ListLinksQuery{})
	require.NoError(t, err)
	_, err = client.ListLinks(t.Context(), "team-1", apiclient.ListLinksQuery{
		Q: "sommer fest", FolderID: "f-1", Sort: "-created_at", Page: 2, PerPage: 10,
	})
	require.NoError(t, err)

	require.Equal(t, apiclient.Page[apiclient.Link]{
		Items: []apiclient.Link{{
			ID: "l-1", TeamID: "t-1", DomainID: "d-1", Hostname: "go.kurze-url.app", Slug: "fest",
			ShortURL: "https://go.kurze-url.app/fest", DestinationURL: "https://example.org/", State: "active",
		}},
		Page: 2, PerPage: 10, TotalCount: 11,
	}, page)
	got := f.requests()
	require.Len(t, got, 2)
	require.Equal(t, "/v1/teams/team-1/links", got[0].Path)
	require.Empty(t, got[0].Query, "zero values must not be sent as q=&page=0")
	require.Equal(t, url.Values{
		"q":         {"sommer fest"},
		"folder_id": {"f-1"},
		"sort":      {"-created_at"},
		"page":      {"2"},
		"per_page":  {"10"},
	}, got[1].Query)
}

func TestLinkStatsSendsTheWindowAndDecodesTheCounts(t *testing.T) {
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, `{
			"link_id": "l-1", "from": "2026-10-01", "to": "2026-10-02", "analytics_enabled": true,
			"totals": {"clicks": 10, "unique_visitors": 7, "human_clicks": 4, "human_unique_visitors": 3},
			"series": [
				{"date": "2026-10-01", "clicks": 6, "unique_visitors": 4, "human_clicks": 2, "human_unique_visitors": 2},
				{"date": "2026-10-02", "clicks": 4, "unique_visitors": 3, "human_clicks": 2, "human_unique_visitors": 1}
			],
			"breakdowns": {}
		}`)
	})
	client := newClient(f, &fakeTokens{})

	stats, err := client.LinkStats(t.Context(), "link-1", "2026-10-01", "2026-10-02")
	require.NoError(t, err)
	_, err = client.LinkStats(t.Context(), "link-1", "", "")
	require.NoError(t, err)

	require.True(t, stats.AnalyticsEnabled)
	require.Equal(t, apiclient.StatCounts{Clicks: 10, UniqueVisitors: 7, HumanClicks: 4, HumanUniqueVisitors: 3}, stats.Totals)
	require.Equal(t, []apiclient.StatDay{
		{Date: "2026-10-01", Clicks: 6, UniqueVisitors: 4, HumanClicks: 2, HumanUniqueVisitors: 2},
		{Date: "2026-10-02", Clicks: 4, UniqueVisitors: 3, HumanClicks: 2, HumanUniqueVisitors: 1},
	}, stats.Series)
	got := f.requests()
	require.Equal(t, "/v1/links/link-1/stats", got[0].Path)
	require.Equal(t, url.Values{"from": {"2026-10-01"}, "to": {"2026-10-02"}}, got[0].Query)
	require.Empty(t, got[1].Query)
}

func TestLinkQRReturnsTheBytes(t *testing.T) {
	// Not valid UTF-8 on purpose: a client that routed the body through a
	// string or a JSON decoder would mangle it.
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0xff, 0xfe}
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	client := newClient(f, &fakeTokens{})

	got, err := client.LinkQR(t.Context(), "link-1", apiclient.QRQuery{Format: "png", Size: 256, FG: "112233", BG: "ffffff"})
	require.NoError(t, err)
	_, err = client.LinkQR(t.Context(), "link-1", apiclient.QRQuery{})
	require.NoError(t, err)

	require.Equal(t, png, got)
	seen := f.requests()
	require.Equal(t, "/v1/links/link-1/qr", seen[0].Path)
	require.Equal(t, "image/*", seen[0].Header.Get("Accept"))
	require.Equal(t, url.Values{"format": {"png"}, "size": {"256"}, "fg": {"112233"}, "bg": {"ffffff"}}, seen[0].Query)
	require.Empty(t, seen[1].Query)
}

func TestLinkQRKeepsAHashInAColourIntact(t *testing.T) {
	// The API's own docs warn that a raw '#' is the fragment delimiter and
	// never reaches the server. The client must escape it, not drop the rest
	// of the query.
	f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte("<svg/>"))
	})

	_, err := newClient(f, &fakeTokens{}).LinkQR(t.Context(), "link-1", apiclient.QRQuery{FG: "#112233", BG: "ffffff"})

	require.NoError(t, err)
	require.Equal(t, "#112233", f.only(t).Query.Get("fg"))
	require.Equal(t, "ffffff", f.only(t).Query.Get("bg"))
}

func TestLinkQRRefreshesAndRetriesLikeEveryOtherCall(t *testing.T) {
	f := newFakeAPI(t, func(call int, _ seenRequest, w http.ResponseWriter) {
		if call == 1 {
			writeProblem(w, http.StatusUnauthorized, `{"status":401}`)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte("<svg/>"))
	})
	tokens := &fakeTokens{}

	got, err := newClient(f, tokens).LinkQR(t.Context(), "link-1", apiclient.QRQuery{})

	require.NoError(t, err)
	require.Equal(t, []byte("<svg/>"), got)
	require.EqualValues(t, 1, tokens.refreshCalls.Load())
}
