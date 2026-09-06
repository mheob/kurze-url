package domainverify_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

type fakeResolver struct {
	values []string
	err    error
}

func (f fakeResolver) LookupTXT(context.Context, string) ([]string, error) {
	return f.values, f.err
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okProbe() *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
}

func TestCheck(t *testing.T) {
	t.Run("passes when the token is present and the host answers", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonNone, reason)
	})

	t.Run("finds the token among several TXT values", func(t *testing.T) {
		// A zone commonly carries SPF and other TXT records on the same name.
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"v=spf1 -all", "tok-a"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonNone, reason)
	})

	t.Run("reports a missing record", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{err: &net.DNSError{IsNotFound: true}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonTokenMissing, reason)
	})

	t.Run("reports a wrong record", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-b"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonTokenMismatch, reason)
	})

	t.Run("reports unreachable when the probe fails", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection refused")
			})},
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("asks for the challenge name, not the hostname", func(t *testing.T) {
		require.Equal(t,
			"_kurze-url-challenge.links.verein.de",
			domainverify.ChallengeName("links.verein.de"))
	})

	// The subtests below exercise the success predicate itself. Before these
	// existed, the status-code check, the body match, and the io.LimitReader
	// cap could all have been deleted and the suite would have stayed green —
	// "reports unreachable when the probe fails" above only ever drove the
	// probe through a transport error, never a response.

	t.Run("reports unreachable for 200 with the wrong body", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":"nope"}`)),
					Request:    r,
				}, nil
			})},
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("reports unreachable for a non-200 with the right body", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
					Request:    r,
				}, nil
			})},
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("reports unreachable when the marker sits past the body cap", func(t *testing.T) {
		// This pins io.LimitReader(resp.Body, maxProbeBody): the marker is
		// real, but it sits after byte 4096, so a capped read never sees it.
		// Without the cap, this subtest would find the marker and wrongly
		// succeed — that is the failure this subtest exists to catch.
		oversized := strings.Repeat("x", 5000) + `{"status":"ok"}`
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(oversized)),
					Request:    r,
				}, nil
			})},
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("reports unreachable when the probe times out", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			})},
		}
		// A short deadline on the context passed in, rather than on
		// probeTimeout itself, is what keeps this subtest fast: reachable's
		// own context.WithTimeout(ctx, probeTimeout) takes the earlier of the
		// two deadlines, so the request's context still expires quickly.
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		reason, err := v.Check(ctx, "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("reports mismatch for an empty token without matching a blank record", func(t *testing.T) {
		// strings.TrimSpace(" ") == "", so without the guard an empty token
		// would satisfy a zone publishing a whitespace-only TXT record.
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{" "}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonTokenMismatch, reason)
	})
}

// The two tests below stand in for the brief's SSRF tests, which turned out
// to be unfalsifiable:
//
//   - a test that dials "https://localhost/health" through the real Control
//     hook stays green with the hook deleted, because nothing listens on
//     port 443 locally and the dial fails anyway — same reason, wrong cause.
//   - a test whose fake transport always answers 302 stays green with
//     CheckRedirect deleted, because http.Client's built-in redirect limit
//     (10 hops) still eventually errors, landing on ReasonUnreachable either
//     way.
//
// TestNewVerifierWiresUpBothMechanisms below calls through the production
// dialer with a private address instead: that fails fast (Control runs
// before the connect syscall, so there is nothing to time out on) and it
// fails for a different reason once the check is removed, which is what
// makes it a real regression test. The mechanism itself
// (refuseNonPublicAddress) has its own direct test in
// verify_internal_test.go, in-package, since it is not part of the
// exported API.

func TestCheckRedirectRefusesToFollow(t *testing.T) {
	v := domainverify.NewVerifier()
	require.NotNil(t, v.Client.CheckRedirect)

	req, err := http.NewRequest(http.MethodGet, "https://links.verein.de/health", nil)
	require.NoError(t, err)

	require.Error(t, v.Client.CheckRedirect(req, nil))
}

func TestNewVerifierWiresUpBothMechanisms(t *testing.T) {
	v := domainverify.NewVerifier()
	require.NotNil(t, v.Client.CheckRedirect, "wiring: redirects must be refused")

	transport, ok := v.Client.Transport.(*http.Transport)
	require.True(t, ok, "expected the client's transport to be *http.Transport")
	require.NotNil(t, transport.DialContext, "wiring: no dialer installed")

	// Dial a private address through the actual transport. Control runs
	// before the connect syscall, so a correctly wired hook rejects this
	// before any socket work — no listener, no network access needed. A
	// Verifier built with a plain dialer (Control forgotten) would instead
	// try to connect and fail for an unrelated reason (or hang), which is
	// exactly the wiring mistake this test exists to catch.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err := transport.DialContext(ctx, "tcp", "127.0.0.1:1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "domainverify: refusing",
		"expected the Control hook's own error, not a generic dial failure")
}
