// Package destination validates where a short link points. It is deliberately
// creation-time only: no DNS is resolved here, because a record can change
// between validation and the first click. The DNS-rebinding re-check belongs
// wherever the service itself fetches a URL, which nothing in the link
// endpoints does.
package destination

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MaxLength caps a destination. Long enough for any real campaign URL, short
// enough that a row stays small.
const MaxLength = 2048

var (
	// ErrMalformed means the value is not a parseable absolute URL.
	ErrMalformed = errors.New("destination: not an absolute URL")

	// ErrScheme means the scheme is not https.
	ErrScheme = errors.New("destination: only https:// destinations are allowed")

	// ErrPrivateAddress means the host is a literal address inside a range
	// that is not reachable from the public internet.
	ErrPrivateAddress = errors.New("destination: private and local addresses are not allowed")

	// ErrSelfReference means the destination points back at this service.
	ErrSelfReference = errors.New("destination: a link may not point at this service")

	// ErrTooLong means the URL exceeds MaxLength.
	ErrTooLong = errors.New("destination: url is too long")
)

// Validate checks a destination URL. selfHostnames is the set of hostnames
// this instance answers on; a destination naming any of them is a loop.
func Validate(raw string, selfHostnames []string) error {
	if len(raw) > MaxLength {
		return fmt.Errorf("%w: %d characters, limit is %d", ErrTooLong, len(raw), MaxLength)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	// The scheme is checked by allowlist, never by blocklist. A blocklist is a
	// promise to enumerate every dangerous scheme forever.
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: got %q", ErrScheme, parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("%w: no host", ErrMalformed)
	}

	if ip := net.ParseIP(host); ip != nil && !isPublic(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
	}

	for _, own := range selfHostnames {
		if host == strings.ToLower(strings.TrimSpace(own)) {
			return fmt.Errorf("%w: %s", ErrSelfReference, host)
		}
	}

	return nil
}

// isPublic reports whether an address literal is one a browser could
// meaningfully be sent to across the internet.
func isPublic(ip net.IP) bool {
	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast(),
		ip.IsUnspecified():
		return false
	}
	// fc00::/7 — unique local addresses. net.IP.IsPrivate covers fc00::/7
	// already, but only for 16-byte forms; the explicit check costs nothing
	// and documents the intent.
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil && v6[0]&0xfe == 0xfc {
		return false
	}
	return true
}
