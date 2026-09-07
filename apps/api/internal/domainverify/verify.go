package domainverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/destination"
)

// Reason says which half of verification is not satisfied yet. It is returned
// to the caller and never stored: domain.verification_status has three values
// and gains no fourth.
type Reason string

const (
	// ReasonNone means both checks passed.
	ReasonNone Reason = ""

	// ReasonTokenMissing means no TXT record exists at the challenge name.
	ReasonTokenMissing Reason = "token_missing"

	// ReasonTokenMismatch means TXT records exist but none matches.
	ReasonTokenMismatch Reason = "token_mismatch"

	// ReasonUnreachable means the hostname does not reach this API. Under
	// maintainer-in-the-loop provisioning this is the normal state until the
	// maintainer has added the hostname to the Vercel project.
	ReasonUnreachable Reason = "unreachable"
)

// challengePrefix is a convention, not a standard. It only has to stay stable
// once a Verein has been told to create the record.
const challengePrefix = "_kurze-url-challenge."

// probeTimeout bounds the whole reachability check. This endpoint waits on a
// network nobody here controls.
const probeTimeout = 5 * time.Second

// maxProbeBody caps what is read from a third-party server. Nothing from the
// response is returned to the caller; the cap exists so a hostile server
// cannot stream forever.
const maxProbeBody = 4 << 10

// Resolver is the DNS half, an interface so tests need no network.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Verifier performs both checks.
type Verifier struct {
	Resolver Resolver
	Client   *http.Client
	// Log records what Check swallows on the caller's behalf — a resolver
	// error is not proof of absence and must not become a 500 (see Check's
	// own comment), but silently treating a DNS outage as "TXT record not
	// found yet" leaves no operator-visible signal that anything is actually
	// wrong. May be left nil — struct literals built directly in this
	// package's own tests do not all set it — in which case logger() below
	// falls back to a no-op handler.
	Log *slog.Logger
}

// logger returns v.Log, or a no-op logger if none was set.
func (v *Verifier) logger() *slog.Logger {
	if v.Log != nil {
		return v.Log
	}
	return slog.New(slog.DiscardHandler)
}

// ChallengeName is the record the claiming team must create.
func ChallengeName(hostname string) string { return challengePrefix + hostname }

// refuseNonPublicAddress is installed as a net.Dialer's Control func by
// NewVerifier. Control runs after the resolver and before the socket
// connects, and is handed the address actually being dialed — that ordering
// is what makes this a defense against DNS rebinding. Validating a resolved
// address and then dialing the hostname a second time would let the second
// lookup answer differently from the first; checking the address Control
// receives closes that gap because there is no second lookup.
//
// It is unexported: this is a mechanism NewVerifier wires up, not part of
// this package's contract. Its own test lives in verify_internal_test.go,
// in-package, precisely so that deleting this check is something a test can
// catch without depending on network access, a listener, or a timeout.
func refuseNonPublicAddress(_, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("domainverify: unparseable dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil || !destination.IsPublic(ip) {
		return fmt.Errorf("domainverify: refusing to connect to %s", host)
	}
	return nil
}

// NewVerifier builds the production Verifier: the system resolver, and an
// HTTP client that refuses redirects and validates the address it is about
// to connect to. log records what Check swallows — see Verifier.Log.
func NewVerifier(log *slog.Logger) *Verifier {
	dialer := &net.Dialer{Timeout: probeTimeout}
	// Rebinding — the first lookup answering with a public address and a
	// second, later lookup answering with a private one — is structurally
	// impossible against this dialer: there is exactly one resolution here,
	// and net.Dialer invokes Control once per connection attempt, passing
	// the address that attempt is actually about to dial, not a cached
	// result from an earlier name-to-IP step. There is no separate
	// check-then-dial-again path for a second lookup to race. If this ever
	// changes — a second, independent resolution introduced somewhere
	// between the check and the dial — this comment stops being true before
	// the hole reopens.
	//
	// That guarantee depends on two things below that are easy to lose
	// without noticing, since neither would cause a compile error or an
	// obviously broken probe: Transport.Proxy is left unset (nil), so nothing
	// resolves this hostname a second time to find a proxy to route through —
	// setting it, e.g. by copying http.DefaultTransport's own
	// Proxy: ProxyFromEnvironment, would reintroduce exactly the second
	// resolution this comment says cannot happen. And Transport.DialTLSContext
	// is left unset, so a TLS connection also goes through DialContext (and
	// therefore through Control) rather than around it on a separate path this
	// dialer's Control never sees.
	dialer.Control = func(network, address string, _ syscall.RawConn) error {
		return refuseNonPublicAddress(network, address)
	}

	return &Verifier{
		Resolver: net.DefaultResolver,
		Client: &http.Client{
			Timeout: probeTimeout,
			Transport: &http.Transport{
				DialContext: dialer.DialContext,
				// This probe is one-shot: one request per Check call, never
				// reused. Keeping the connection alive would only hold an idle
				// TCP+TLS socket open for the rest of this long-lived
				// process's life, one per verified hostname, forever.
				DisableKeepAlives: true,
				// Unset defaults to Client.Timeout, which also has to cover
				// reading the body — naming the handshake's own budget keeps
				// a slow TLS peer from eating the whole thing before a single
				// byte of response exists.
				TLSHandshakeTimeout: probeTimeout,
				// Unset defaults to 10 MiB — an unexported constant net/http's
				// Transport falls back to internally, *not*
				// net/http.DefaultMaxHeaderBytes (that one is 1 MiB, and bounds a
				// server's incoming *request* headers, an unrelated setting on
				// the other side of a connection). The body is capped at
				// maxProbeBody; headers were not, which let a hostile server
				// push far more into this process per probe than the "a few
				// kilobytes read" the design calls for.
				MaxResponseHeaderBytes: 8 << 10,
			},
			// A 302 to 169.254.169.254 would walk straight past the address
			// check above, because the redirect is followed by a fresh dial the
			// caller never sees.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("domainverify: redirects are not followed")
			},
		},
		Log: log,
	}
}

// Check answers whether hostname may serve this team's links. It returns the
// first unsatisfied condition.
//
// The returned error is reserved for a check that cannot be performed at
// all, as distinct from one that ran and said no — but every path through
// this method today returns (Reason, nil), the resolver-failure branch below
// included (see its own comment for why that is deliberately
// ReasonTokenMissing, not an error). That makes a caller's "if err != nil,
// answer 500" branch dead code right now, not a case nobody has exercised
// yet — do not add handling for a non-nil error here without also updating
// this comment, or that handling will stay untested by construction.
//
// hostname must already be normalized: Check interpolates it directly into
// the probe URL, and NormalizeHostname in this package is the precondition
// that makes doing so safe — it rejects the scheme/port/path/credential
// separators, whitespace, and IP literals that would make that interpolation
// dangerous.
func (v *Verifier) Check(ctx context.Context, hostname, token string) (Reason, error) {
	// An empty token would otherwise match a whitespace-only TXT value, since
	// strings.TrimSpace(value) == "" for one. A claim can never have an empty
	// token, so treat it as a guaranteed mismatch rather than special-casing
	// whitespace records below.
	if token == "" {
		return ReasonTokenMismatch, nil
	}

	values, err := v.Resolver.LookupTXT(ctx, ChallengeName(hostname))
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return ReasonTokenMissing, nil
	case err != nil:
		// A resolver failure is not proof of absence, but it is also not
		// something the caller can act on differently, and it must not become a
		// 500 for a Verein whose DNS is briefly slow. That still means a real
		// resolver outage is reported to the Verein as "your TXT record is not
		// visible yet" — a wrong instruction, since there is nothing wrong with
		// their DNS — so this is logged here: the one place left where an
		// operator, not the Verein, can find out the resolver itself is the
		// thing that is actually broken.
		v.logger().Error("domainverify: TXT lookup failed, reporting token_missing",
			"hostname", hostname, "error", err)
		return ReasonTokenMissing, nil //nolint:nilerr // deliberate: see comment above
	case len(values) == 0:
		return ReasonTokenMissing, nil
	}

	found := false
	for _, value := range values {
		if strings.TrimSpace(value) == token {
			found = true
			break
		}
	}
	if !found {
		return ReasonTokenMismatch, nil
	}

	if !v.reachable(ctx, hostname) {
		return ReasonUnreachable, nil
	}
	return ReasonNone, nil
}

// reachable asks whether this API answers on the hostname. It is a readiness
// check, not a security boundary: a third party could answer this on a host
// they control and would gain nothing, because the token already proved
// ownership and their links still would not be served here.
func (v *Verifier) reachable(ctx context.Context, hostname string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostname+"/health", nil)
	if err != nil {
		return false
	}

	resp, err := v.Client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// Read and discard under a cap. Nothing from this response is returned to
	// the caller — an endpoint that echoed what it fetched would be a reading
	// primitive for everything reachable from this network.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return false
	}
	return resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"status":"ok"`)
}
