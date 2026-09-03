// Package slug owns the shape of the short part of a short link: how one is
// generated, how a user-supplied alias is normalized, and which values are
// refused outright.
package slug

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Alphabet excludes 0, 1, l and o. These links travel on posters, flyers and
// printed newsletters, where a reader has to tell characters apart by eye.
const Alphabet = "23456789abcdefghijkmnpqrstuvwxyz"

// Length is the generated slug length. 32^8 is about 1.1e12 combinations.
const Length = 8

var (
	// ErrMalformed means the value does not match the permitted shape.
	ErrMalformed = errors.New("slug: not a permitted slug")

	// ErrReserved means the value is a path the service itself owns.
	ErrReserved = errors.New("slug: reserved")
)

// pattern is applied after Normalize, so it never needs to consider case.
// Three to sixty-four characters, alphanumeric at both ends, with hyphens and
// underscores permitted only inside.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$`)

// reserved names paths the service serves itself, plus the conventional
// browser and platform paths. It applies on every domain, not only the shared
// one: a single chi mux serves them all, so a custom domain reaches the same
// routes.
var reserved = map[string]struct{}{
	"health":               {}, // registered on the root router today
	"verify":               {}, // the password interstitial's own subpath
	"api":                  {},
	"admin":                {},
	"login":                {},
	"static":               {},
	"assets":               {},
	"robots.txt":           {},
	"favicon.ico":          {},
	"sitemap.xml":          {},
	"apple-touch-icon.png": {},
	".well-known":          {},
	"_next":                {},
}

// Generate draws a slug from crypto/rand.
func Generate() (string, error) {
	limit := big.NewInt(int64(len(Alphabet)))

	var b strings.Builder
	b.Grow(Length)
	for range Length {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("slug: draw a random character: %w", err)
		}
		b.WriteByte(Alphabet[n.Int64()])
	}
	return b.String(), nil
}

// Normalize is applied to every slug on the way in and on the way out of the
// redirect path, so /Abc and /abc are the same link.
func Normalize(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// Validate checks a normalized slug. Pass it the output of Normalize, never
// raw user input.
func Validate(normalized string) error {
	if _, ok := reserved[normalized]; ok {
		return fmt.Errorf("%w: %q", ErrReserved, normalized)
	}
	if !pattern.MatchString(normalized) {
		return fmt.Errorf("%w: %q", ErrMalformed, normalized)
	}
	return nil
}
