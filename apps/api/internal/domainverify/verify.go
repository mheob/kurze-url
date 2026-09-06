package domainverify

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// to connect to.
func NewVerifier() *Verifier {
	dialer := &net.Dialer{Timeout: probeTimeout}
	dialer.Control = func(network, address string, _ syscall.RawConn) error {
		return refuseNonPublicAddress(network, address)
	}

	return &Verifier{
		Resolver: net.DefaultResolver,
		Client: &http.Client{
			Timeout:   probeTimeout,
			Transport: &http.Transport{DialContext: dialer.DialContext},
			// A 302 to 169.254.169.254 would walk straight past the address
			// check above, because the redirect is followed by a fresh dial the
			// caller never sees.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("domainverify: redirects are not followed")
			},
		},
	}
}

// Check answers whether hostname may serve this team's links. It returns the
// first unsatisfied condition; an error means the check could not be
// performed at all, which is different from a check that ran and said no.
func (v *Verifier) Check(ctx context.Context, hostname, token string) (Reason, error) {
	values, err := v.Resolver.LookupTXT(ctx, ChallengeName(hostname))
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return ReasonTokenMissing, nil
	case err != nil:
		// A resolver failure is not proof of absence, but it is also not
		// something the caller can act on differently, and it must not become a
		// 500 for a Verein whose DNS is briefly slow.
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
