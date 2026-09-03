package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Count caps on the resources a team names for itself.
//
// These are not rate limits — the per-user write rate limit still applies on
// top. They exist because the Supabase free tier is 500 MB and has no backups
// at all, which makes unbounded row growth the one failure mode with no
// recovery path. A rate limit makes bulk creation slow; a cap makes it
// impossible.
//
// The numbers are generous for a Verein by roughly an order of magnitude.
// Raising one is a one-line change here.
const (
	maxFoldersPerTeam = 100
	maxTagsPerTeam    = 200
	maxTagsPerLink    = 10
)

// maxResourceNameLength bounds a folder or tag name in characters, not bytes.
// Sixty fits a filter chip and a table column without truncation and holds the
// longest realistic German compound. The cap exists mainly because name is
// `text` in Postgres: the count caps above bound how many rows a team creates,
// and this bounds how large one gets.
const maxResourceNameLength = 60

// ErrNameEmpty and ErrNameTooLong are returned by validateResourceName.
// Handlers turn them into 422s; they are values rather than strings so a test
// can assert which rule fired.
var (
	ErrNameEmpty   = errors.New("name must not be empty")
	ErrNameTooLong = fmt.Errorf("name must be at most %d characters", maxResourceNameLength)
)

// validateResourceName is the one naming rule folders and tags share: trimmed
// of surrounding whitespace, non-empty, and at most maxResourceNameLength
// characters. It returns the trimmed name, which is what gets stored — case,
// umlauts, inner spaces and emoji all survive untouched.
//
// Length is counted in runes. Sixty umlauts are 120 bytes, so a byte-length
// check would reject a name that is exactly at the limit.
func validateResourceName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrNameEmpty
	}
	if utf8.RuneCountInString(name) > maxResourceNameLength {
		return "", ErrNameTooLong
	}
	return name, nil
}
