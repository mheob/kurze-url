// Package domainverify decides whether a team may serve links on a hostname.
// It answers two separate questions: does the claimant control the DNS zone
// (a TXT token), and does the hostname actually reach this API (a probe).
// Only both together make a domain usable, because a link on a hostname that
// does not resolve is a link that 404s for everyone who clicks it.
package domainverify

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

var (
	// ErrMalformed means the value is not a bare hostname.
	ErrMalformed = errors.New("domainverify: not a bare hostname")

	// ErrApex means the value is a registrable domain rather than a subdomain
	// of one.
	ErrApex = errors.New("domainverify: an apex domain cannot be used")

	// ErrReserved means the hostname belongs to this instance.
	ErrReserved = errors.New("domainverify: this hostname belongs to the instance")
)

// NormalizeHostname turns user input into the exact string stored in
// domain.hostname, or explains why it cannot. reserved is this instance's own
// hostnames — Deps.selfHostnames supplies them.
func NormalizeHostname(raw string, reserved []string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")

	if host == "" {
		return "", fmt.Errorf("%w: empty", ErrMalformed)
	}
	if strings.ContainsAny(host, ":/@ \t") {
		return "", fmt.Errorf("%w: %q carries a scheme, port, path or credentials", ErrMalformed, raw)
	}
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("%w: %q is an address, not a name", ErrMalformed, raw)
	}

	// Punycode before counting labels: an IDN's label count does not change,
	// but the stored value must be the ASCII form the resolver will be asked
	// about.
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%w: %q has no dot", ErrMalformed, raw)
	}
	// idna.Lookup sets verifyDNSLength: false — only the separate
	// idna.Registration profile checks the wire-format DNS limits, so
	// ToASCII above happily accepted a name of any length. Without this
	// check an oversized hostname would sit in domain.hostname as junk that
	// can never verify, and the TXT challenge name would later be built by
	// prefixing a label onto it regardless.
	if len(ascii) > 253 {
		return "", fmt.Errorf("%w: %q is %d octets, over the 253-octet limit", ErrMalformed, raw, len(ascii))
	}
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("%w: %q has an empty label", ErrMalformed, raw)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("%w: %q has a label over 63 octets", ErrMalformed, raw)
		}
	}
	// Two labels is a registrable domain in the common case. This is a
	// deliberate approximation, not a public-suffix lookup: getting it wrong
	// rejects a claim that could have worked, which the maintainer can settle
	// by hand, whereas accepting an apex takes a Verein's website offline.
	if len(labels) == 2 {
		return "", fmt.Errorf("%w: %q", ErrApex, ascii)
	}

	if strings.HasSuffix(ascii, ".vercel.app") {
		return "", fmt.Errorf("%w: %q", ErrReserved, ascii)
	}
	for _, own := range reserved {
		if ascii == strings.ToLower(strings.TrimSpace(own)) {
			return "", fmt.Errorf("%w: %q", ErrReserved, ascii)
		}
	}

	return ascii, nil
}
