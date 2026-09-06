package domainverify

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// tokenBytes is the amount of randomness drawn per token. base32 encodes 5
// bits per output character with no padding at any multiple of 5 input
// bits, and 20 bytes is 160 bits — 20*8/5 = 32 output characters exactly,
// with no padding characters to strip.
const tokenBytes = 20

// GenerateToken produces a domain claim's verification token: the value a
// team publishes as a TXT record and this package later compares against in
// Check.
//
// This is deliberately not slug.Generate reused at a longer length: that
// generator is hardcoded to slug.Length (8) with no way to ask for more
// characters, and its alphabet deliberately omits 0, 1, l and o because a
// slug is transcribed by eye off a printed flyer. A DNS TXT value is
// copy-pasted, never read aloud, so that legibility constraint buys nothing
// here — and reusing it anyway would couple two unrelated concerns: a future
// change to slug legibility would silently change token entropy too. This
// draws its own randomness and encodes it as lowercase base32 instead, at a
// length chosen for this value alone.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("domainverify: generate token: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return strings.ToLower(encoded), nil
}
