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
		return fmt.Errorf("%w: %w", ErrMalformed, err)
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

	if ip := net.ParseIP(host); ip != nil && !IsPublic(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
	}

	for _, own := range selfHostnames {
		if host == strings.ToLower(strings.TrimSpace(own)) {
			return fmt.Errorf("%w: %s", ErrSelfReference, host)
		}
	}

	return nil
}

// nonPublicRanges lists CIDR blocks IsPublic must reject that net.IP's own
// IsPrivate/IsLinkLocalUnicast/etc. predicates do not cover. Each is real,
// assigned address space a resolver can legitimately hand back — not merely
// reserved-on-paper — which is exactly what makes leaving it out dangerous:
// this predicate stopped being link-validation-only the day
// refuseNonPublicAddress (internal/domainverify) started relying on it to
// decide what the verification probe may connect to.
var nonPublicRanges = mustParseCIDRs(
	// RFC 6598 — carrier-grade NAT (CGNAT). Several large ISPs and cloud
	// providers route real internal traffic here; it is not unused space
	// reserved "just in case".
	"100.64.0.0/10",
	// RFC 6890 — IETF Protocol Assignments, including the DNS64/NAT64
	// discovery prefix's own host range. Not routed on the public internet.
	"192.0.0.0/24",
	// RFC 2544 — benchmarking. Reserved for device test labs, never routed
	// on the public internet.
	"198.18.0.0/15",
	// RFC 1112 — reserved for future use ("Class E"). No public route has
	// ever existed for it.
	"240.0.0.0/4",
	// RFC 6052 — the well-known NAT64 prefix. An address here embeds an
	// IPv4 address that a NAT64 gateway translates and connects to on this
	// instance's behalf, so it must be judged by the same rule as an IPv4
	// literal above it — otherwise a gateway mapping it to a private v4
	// address would dial straight past every check this package does.
	"64:ff9b::/96",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, len(cidrs))
	for i, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Only ever reachable via a typo in the literals above, at
			// package init — never with attacker-controlled input.
			panic(fmt.Sprintf("destination: invalid CIDR literal %q: %v", cidr, err))
		}
		nets[i] = ipNet
	}
	return nets
}

// IsPublic reports whether an address literal is one a browser could
// meaningfully be sent to across the internet. Exported because
// internal/domainverify applies the same predicate to the address a
// verification probe is about to connect to — the same question, so the same
// answer, rather than a second copy that drifts.
func IsPublic(ip net.IP) bool {
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
	for _, r := range nonPublicRanges {
		if r.Contains(ip) {
			return false
		}
	}
	return true
}
