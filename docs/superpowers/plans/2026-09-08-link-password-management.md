# Link Password Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team member with the editor role can protect a link with a password, change it, and remove it — from the dashboard, in German or English, taking effect on the next visitor rather than after the cache expires.

**Architecture:** Two new `/v1` endpoints on a link subresource, backed by one sqlc query that writes `link.password_hash` and nothing else. A password policy in `internal/auth` beside the hashing. A second rate-limit axis on the public verify path that counts only failures, which needs the sliding-window limiter split into a read half and a write half. Both endpoints invalidate the redirect cache, because `has_password` lives in the cached record.

**Tech Stack:** Go 1.27, chi + Huma v2, sqlc, pgx/v5, Redis via go-redis/v9 with Lua, Argon2id from `golang.org/x/crypto`; TanStack Start + Router/Query/Form, shadcn/ui on Radix, i18next, Vitest + RTL, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-08-link-password-management-design.md`

## Global Constraints

Copied from the spec and from `CLAUDE.md`; every task's requirements include these.

- No RLS. Every query filters by `team_id`; the check lives in Go. A query without a tenancy filter is a data-leak bug.
- A non-member gets **404, never 403**. So does a member whose `link_id` belongs to another team.
- The redirect path `GET /{slug}` is the hot path and **this plan does not touch it**. The new Redis reads live on `POST /{slug}/verify`.
- No hardcoded user-facing string. English and German ship together, both in `apps/web/src/i18n/locales/`.
- WCAG 2.1 AA, gated in CI at two levels.
- **`audit_log.metadata` never carries a plaintext password or a hash.** `audit.ErrForbiddenMetadata` enforces this; do not work around it.
- Errors use Huma's default RFC 9457 `application/problem+json`. A typed value travels in `huma.ErrorDetail{Location, Value}`, never parsed out of the free-text `detail`.
- Conventional Commits, **max 50 characters including type and scope**. Run `pnpm format` before every commit. Never pass `--no-verify`.
- `apps/web/src/routeTree.gen.ts` is only committed when a route file was added or removed. No route files change in this plan, so it must not appear in any diff.
- Argon2id parameters stay at `auth.DefaultParams` (19 MiB, 2 iterations, 1 lane).

---

### Task 1: The password policy

**Files:**

- Create: `apps/api/internal/auth/policy.go`
- Create: `apps/api/internal/auth/common-passwords.txt`
- Test: `apps/api/internal/auth/policy_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `auth.ValidatePassword(plain string, ctx auth.PolicyContext) error`; `auth.PolicyContext{LinkSlug, DestinationURL, TeamName, TeamSlug string}`; the sentinel errors `auth.ErrPasswordTooShort`, `auth.ErrPasswordTooLong`, `auth.ErrPasswordTooRepetitive`, `auth.ErrPasswordFromContext`, `auth.ErrPasswordTooCommon`, whose `Error()` strings are exactly `too_short`, `too_long`, `too_repetitive`, `derived_from_context`, `too_common`; the constants `auth.MinPasswordLength = 8` and `auth.MaxPasswordLength = 128`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/auth/policy_test.go`:

```go
package auth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

// gruenwald is the context every case below is judged against: a real-shaped
// Verein, whose name carries an umlaut the policy has to fold before it can
// catch the password that Verein would actually pick.
var gruenwald = auth.PolicyContext{
	LinkSlug:       "sommerfest-2026",
	DestinationURL: "https://www.sv-gruenwald.de/verein/sommerfest",
	TeamName:       "SV Grünwald e.V.",
	TeamSlug:       "sv-gruenwald",
}

func TestValidatePasswordAcceptsAnUnrelatedPassphrase(t *testing.T) {
	require.NoError(t, auth.ValidatePassword("Kartoffelsalat!7", gruenwald))
}

func TestValidatePasswordRejectsByRule(t *testing.T) {
	for name, tc := range map[string]struct {
		password string
		want     error
	}{
		"seven characters":        {"Abcdef1", auth.ErrPasswordTooShort},
		"one hundred twenty nine": {repeatRunes(129), auth.ErrPasswordTooLong},
		"three distinct runes":    {"abababab", auth.ErrPasswordTooRepetitive},
		"only punctuation":        {"!!!!!!!!", auth.ErrPasswordTooRepetitive},
		"the link's own slug":     {"sommerfest2026", auth.ErrPasswordFromContext},
		"contained by the slug":   {"sommerfest", auth.ErrPasswordFromContext},
		"the team slug extended":  {"svgruenwaldsommerfest", auth.ErrPasswordFromContext},
		"the team name folded":    {"Gruenwald2026", auth.ErrPasswordFromContext},
		"the destination host":    {"svgruenwald.de!", auth.ErrPasswordFromContext},
		"a common password":       {"Passwort!", auth.ErrPasswordTooCommon},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, auth.ValidatePassword(tc.password, gruenwald), tc.want)
		})
	}
}

// repeatRunes builds a string of n distinct-enough characters, so the only
// rule it can trip is the length ceiling.
func repeatRunes(n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = rune('a' + i%26)
	}
	return string(out)
}

// TestValidatePasswordReasonsAreTheWireTokens pins the one coupling the API
// depends on: the handler puts err.Error() straight into
// huma.ErrorDetail.Value, and apps/web keys its message off that string.
// Rewording one of these sentinels would silently change the wire contract.
func TestValidatePasswordReasonsAreTheWireTokens(t *testing.T) {
	require.Equal(t, "too_short", auth.ErrPasswordTooShort.Error())
	require.Equal(t, "too_long", auth.ErrPasswordTooLong.Error())
	require.Equal(t, "too_repetitive", auth.ErrPasswordTooRepetitive.Error())
	require.Equal(t, "derived_from_context", auth.ErrPasswordFromContext.Error())
	require.Equal(t, "too_common", auth.ErrPasswordTooCommon.Error())
}

// TestValidatePasswordAcceptsAWeakButCompliantPassword records the policy's
// documented limit rather than leaving it to be rediscovered as a bug. The
// spec says so in as many words: the policy raises the floor, the rate limits
// bound the damage. Tightening this needs a decision, not a patch.
func TestValidatePasswordAcceptsAWeakButCompliantPassword(t *testing.T) {
	require.NoError(t, auth.ValidatePassword("passwort1", gruenwald))
}

// TestValidatePasswordChecksTheCommonListBeforeTheContext fixes the reported
// reason for a password that trips both rules, so the frontend's message does
// not depend on evaluation order nobody wrote down.
func TestValidatePasswordChecksTheCommonListBeforeTheContext(t *testing.T) {
	ctx := gruenwald
	ctx.TeamName = "Passwort e.V."
	require.ErrorIs(t, auth.ValidatePassword("passwort", ctx), auth.ErrPasswordTooCommon)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/auth/ -run TestValidatePassword -v` Expected: FAIL — `undefined: auth.ValidatePassword`.

- [ ] **Step 3: Write the common-password list**

Create `apps/api/internal/auth/common-passwords.txt` with exactly the content below. One entry per line; blank lines and lines beginning with `#` are ignored by the loader.

This list is the deliverable, not a stub — add an entry only if you know of a common German or English password it misses. It is deliberately not a hundred-thousand-entry corpus: the binary ships to a serverless function, the comparison is equality rather than substring, and the context rules are the half that earns their size.

```
# Common link passwords, German and English. Compared against the normalized
# password for equality, never as a substring — see policy.go.
passwort
password
passwort1
password1
12345678
123456789
1234567890
qwertz
qwertzui
qwerty
qwertyui
geheim
geheimnis
willkommen
welcome
verein
vereinsheim
mitglieder
sommerfest
weihnachtsfeier
jahreshauptversammlung
vorstand
letmein
iloveyou
sonnenschein
fussball
deutschland
admin123
test1234
abcd1234
```

- [ ] **Step 4: Write the policy**

Create `apps/api/internal/auth/policy.go`:

```go
package auth

import (
	_ "embed"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// The policy's rejection reasons. Each Error() string is the token the API
// puts into huma.ErrorDetail.Value and apps/web keys its message off, so
// these sentinels are a wire contract, not internal prose. policy_test.go
// pins the strings.
var (
	ErrPasswordTooShort      = errors.New("too_short")
	ErrPasswordTooLong       = errors.New("too_long")
	ErrPasswordTooRepetitive = errors.New("too_repetitive")
	ErrPasswordFromContext   = errors.New("derived_from_context")
	ErrPasswordTooCommon     = errors.New("too_common")
)

// The policy's numbers. Length is deliberately enforced here rather than as
// minLength/maxLength on the Huma schema: Huma would reject an out-of-range
// value with its own error shape, and two of the five reasons would be
// unreachable over HTTP while still existing here.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128

	// minDistinctRunes is what stops "!!!!!!!!" and "abababab", both of which
	// clear the length rule. NIST SP 800-63B permits blocking repetitive
	// characters; this is not a character-class rule, and there are none.
	minDistinctRunes = 4

	// minContextToken drops context fragments too short to judge a password
	// by. "sv" or a two-letter country label would reject nearly everything a
	// person could type.
	minContextToken = 4

	// minNormalizedForContext skips the context rules for a password that
	// normalizes to almost nothing — punctuation, which minDistinctRunes has
	// already judged. Comparing an empty string against tokens matches every
	// token.
	minNormalizedForContext = 3
)

// PolicyContext carries the values a link password must not be derived from.
// Link passwords are shared out of band with a group, so the failure that
// actually happens is not weakness in the abstract but predictability from
// context: "Sommerfest26" is the first thing anyone who has seen the link
// would guess.
type PolicyContext struct {
	LinkSlug       string
	DestinationURL string
	TeamName       string
	TeamSlug       string
}

//go:embed common-passwords.txt
var commonPasswordSource string

var commonPasswords = loadCommonPasswords(commonPasswordSource)

func loadCommonPasswords(source string) map[string]struct{} {
	set := make(map[string]struct{})
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if normalized := normalizeForPolicy(line); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

// ValidatePassword applies the policy in a fixed order, so the reason a
// caller sees for a password that trips several rules does not depend on
// anything unwritten.
func ValidatePassword(plain string, ctx PolicyContext) error {
	runes := []rune(plain)
	switch {
	case len(runes) < MinPasswordLength:
		return ErrPasswordTooShort
	case len(runes) > MaxPasswordLength:
		return ErrPasswordTooLong
	}

	// Counted over the raw runes, not the normalized form: normalizing first
	// would collapse "!!!!a!!!!" to a single character and fail a password
	// that is merely odd.
	if distinctRunes(runes) < minDistinctRunes {
		return ErrPasswordTooRepetitive
	}

	normalized := normalizeForPolicy(plain)
	if _, common := commonPasswords[normalized]; common {
		return ErrPasswordTooCommon
	}

	if len(normalized) < minNormalizedForContext {
		return nil
	}
	for _, token := range contextTokens(ctx) {
		// Both directions: "sommerfest" is contained by the slug
		// "sommerfest-2026", and "svgruenwaldsommerfest" contains the team
		// slug "sv-gruenwald".
		if strings.Contains(normalized, token) || strings.Contains(token, normalized) {
			return ErrPasswordFromContext
		}
	}
	return nil
}

func distinctRunes(runes []rune) int {
	seen := make(map[rune]struct{}, len(runes))
	for _, r := range runes {
		seen[r] = struct{}{}
	}
	return len(seen)
}

// normalizeForPolicy folds a string to the form the context and common-list
// rules compare on: German characters transliterated, lower case, everything
// outside [a-z0-9] dropped.
//
// The transliteration is load-bearing rather than decorative. Without it a
// team named "SV Grünwald" does not catch the password "Gruenwald2026", which
// is exactly the password that team will choose. This is the third
// implementation of the same transliteration in this repository — the others
// are suggestTeamSlug in apps/web/src/lib/team-slug.ts and the team-slug
// backfill migration — and they share no code.
func normalizeForPolicy(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'ä':
			b.WriteString("ae")
		case 'ö':
			b.WriteString("oe")
		case 'ü':
			b.WriteString("ue")
		case 'ß':
			b.WriteString("ss")
		default:
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// contextTokens is every normalized string the password must not contain or
// be contained by: each source's whole value plus its parts, split on the
// separators a name or a slug uses.
func contextTokens(ctx PolicyContext) []string {
	sources := []string{ctx.LinkSlug, ctx.TeamName, ctx.TeamSlug}
	if label := destinationLabel(ctx.DestinationURL); label != "" {
		sources = append(sources, label)
	}

	var tokens []string
	for _, source := range sources {
		candidates := append([]string{source}, strings.FieldsFunc(source, isTokenSeparator)...)
		for _, candidate := range candidates {
			if normalized := normalizeForPolicy(candidate); len(normalized) >= minContextToken {
				tokens = append(tokens, normalized)
			}
		}
	}
	return tokens
}

func isTokenSeparator(r rune) bool {
	return r == '-' || r == '.' || r == '_' || unicode.IsSpace(r)
}

// destinationLabel is the destination's hostname with a leading "www." and
// its last label removed, so https://www.sv-gruenwald.de/verein contributes
// "sv-gruenwald" rather than "de". No public-suffix list: being slightly
// over-inclusive costs a rejected password, and a new dependency would cost
// more. A URL that will not parse contributes nothing — destination.Validate
// has already run by the time a password reaches here.
func destinationLabel(destination string) string {
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	labels := strings.Split(strings.TrimPrefix(parsed.Hostname(), "www."), ".")
	if len(labels) > 1 {
		labels = labels[:len(labels)-1]
	}
	return strings.Join(labels, ".")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/auth/ -v` Expected: PASS, including the pre-existing hashing tests.

- [ ] **Step 6: Run the gates**

Run: `cd apps/api && gofmt -l internal && go vet ./... && go test ./internal/auth/` Expected: no output from `gofmt -l`, `vet` silent, tests ok.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): add the link password policy"
```

---

### Task 2: Split the rate limiter into a peek and a count

**Files:**

- Create: `apps/api/internal/cache/lua/ratelimit_peek.lua`
- Create: `apps/api/internal/cache/lua/ratelimit_count.lua`
- Modify: `apps/api/internal/cache/client.go`
- Test: `apps/api/internal/cache/ratelimit_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `(*cache.Client).WithinLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, error)` and `(*cache.Client).Increment(ctx context.Context, key string, window time.Duration) error`.

**Why this exists:** `Allow` checks and increments atomically in one script, which is correct for every existing caller and cannot express "count only on failure". Task 6 needs a counter consulted _before_ an expensive Argon2id verification and incremented only _after_ it fails.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/cache/ratelimit_test.go`. `newTestClient(t) *cache.Client` already exists in `apps/api/internal/cache/testhelper_test.go` and starts one Redis container for the package; use it rather than starting a second.

```go
// TestWithinLimitAndIncrementShareAWindowSlot is the one test that matters for
// this pair. They are two scripts doing half a job each, and if their slot
// arithmetic ever disagrees — a different divisor, a different rounding — the
// increment lands in a slot the peek never reads, the limit silently never
// fires, and nothing else in the suite notices.
func TestWithinLimitAndIncrementShareAWindowSlot(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	key := "test:peek:" + uuid.NewString()

	within, err := client.WithinLimit(ctx, key, 3, time.Hour)
	require.NoError(t, err)
	require.True(t, within, "a fresh key must start inside the limit")

	for range 3 {
		require.NoError(t, client.Increment(ctx, key, time.Hour))
	}

	within, err = client.WithinLimit(ctx, key, 3, time.Hour)
	require.NoError(t, err)
	require.False(t, within,
		"three increments against a limit of three must close the window; "+
			"a peek that still reports true means the two scripts disagree about the slot")
}

// TestWithinLimitDoesNotConsumeTheBudget pins the property the whole split
// exists for: a check is free, so a visitor who knows the password never
// spends a failure budget by arriving.
func TestWithinLimitDoesNotConsumeTheBudget(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	key := "test:peek-free:" + uuid.NewString()

	for range 10 {
		within, err := client.WithinLimit(ctx, key, 1, time.Hour)
		require.NoError(t, err)
		require.True(t, within)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/cache/ -run 'TestWithinLimit' -v` Expected: FAIL — `client.WithinLimit undefined`.

- [ ] **Step 3: Write the two Lua scripts**

Create `apps/api/internal/cache/lua/ratelimit_peek.lua`:

```lua
-- Read-only half of the sliding window in ratelimit.lua: reports whether one
-- more event would stay inside the limit, without recording one. It exists
-- because a counter that must only count failures has to be consulted before
-- the expensive work and incremented after it, which Allow's single atomic
-- step cannot express.
--
-- The slot arithmetic below MUST stay identical to ratelimit.lua and
-- ratelimit_count.lua. A peek and an increment that disagreed about where a
-- window begins would count into a slot nobody reads, and the limit would
-- silently never fire.
--
-- KEYS[1] base key (the two window counters are derived from it)
-- ARGV[1] limit
-- ARGV[2] window length in seconds
-- ARGV[3] current time in unix milliseconds
-- returns 1 when one more event stays inside the limit, 0 when it would not

local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])

local windowMillis = window * 1000
local currentSlot  = math.floor(now / windowMillis)

local currentCount  = tonumber(redis.call('GET', KEYS[1] .. ':' .. currentSlot))       or 0
local previousCount = tonumber(redis.call('GET', KEYS[1] .. ':' .. (currentSlot - 1))) or 0

local elapsed = (now % windowMillis) / windowMillis

if previousCount * (1 - elapsed) + currentCount >= limit then
  return 0
end

return 1
```

Create `apps/api/internal/cache/lua/ratelimit_count.lua`:

```lua
-- Write-only half: records one event in the current window. See
-- ratelimit_peek.lua for why the two are separate, and why the slot
-- arithmetic here must stay identical to it and to ratelimit.lua.
--
-- KEYS[1] base key
-- ARGV[1] window length in seconds
-- ARGV[2] current time in unix milliseconds
-- returns the current window's count after the increment

local window = tonumber(ARGV[1])
local now    = tonumber(ARGV[2])

local windowMillis = window * 1000
local currentKey   = KEYS[1] .. ':' .. math.floor(now / windowMillis)

local count = redis.call('INCR', currentKey)
redis.call('EXPIRE', currentKey, window * 2)

return count
```

- [ ] **Step 4: Wire both scripts into the client**

In `apps/api/internal/cache/client.go`, beside the existing `//go:embed lua/ratelimit.lua` block, add:

```go
//go:embed lua/ratelimit_peek.lua
var rateLimitPeekSource string

var rateLimitPeekScript = redis.NewScript(rateLimitPeekSource)

//go:embed lua/ratelimit_count.lua
var rateLimitCountSource string

var rateLimitCountScript = redis.NewScript(rateLimitCountSource)
```

And, directly after the existing `Allow` method:

```go
// WithinLimit reports whether one more event on this key would stay inside
// limit over window, without recording one. Two Redis commands, no writes.
//
// It is the read half of the pair Allow collapses into a single step. Use
// Allow wherever every attempt should count; use this with Increment where
// only some attempts should — the password interstitial charges failures and
// lets a visitor who knows the password through for free.
func (c *Client) WithinLimit(
	ctx context.Context, key string, limit int, window time.Duration,
) (bool, error) {
	res, err := rateLimitPeekScript.Run(ctx, c.rdb,
		[]string{key},
		limit,
		int(window.Seconds()),
		time.Now().UnixMilli(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("cache: within limit: %w", err)
	}
	return res == 1, nil
}

// Increment records one event on this key, in the same sliding window
// WithinLimit reads. Two Redis commands.
func (c *Client) Increment(ctx context.Context, key string, window time.Duration) error {
	if err := rateLimitCountScript.Run(ctx, c.rdb,
		[]string{key},
		int(window.Seconds()),
		time.Now().UnixMilli(),
	).Err(); err != nil {
		return fmt.Errorf("cache: increment: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/cache/ -v` Expected: PASS, including the pre-existing `Allow` and lookup tests.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): split the rate limiter in two"
```

---

### Task 3: The two new rate-limit values

**Files:**

- Modify: `apps/api/internal/config/config.go`
- Modify: `apps/api/.env.example`
- Test: `apps/api/internal/config/config_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `Config.PasswordSetRateLimitPerHour int` (env `RATE_LIMIT_PASSWORD_SET_PER_HOUR`, default 20) and `Config.PasswordFailureRateLimitPerHour int` (env `RATE_LIMIT_PASSWORD_FAILURES_PER_HOUR`, default 100).

**Context for the implementer:** the rate-limit values in this file were settled on 2026-09-08, and the settlement's whole point is that each value records what it protects **and what it does not**. `.env.example` already has that shape. Match it; a bare `NAME=20` with no prose is a regression here.

- [ ] **Step 1: Write the failing test**

In `apps/api/internal/config/config_test.go`, extend the existing `TestLoadAppliesDefaults` with two lines beside the other rate-limit assertions:

```go
	require.Equal(t, 20, cfg.PasswordSetRateLimitPerHour)
	require.Equal(t, 100, cfg.PasswordFailureRateLimitPerHour)
```

And add a test beside the other rate-limit tests:

```go
func TestPasswordRateLimitsOverrideFromEnv(t *testing.T) {
	setRequired(t)
	t.Setenv("RATE_LIMIT_PASSWORD_SET_PER_HOUR", "3")
	t.Setenv("RATE_LIMIT_PASSWORD_FAILURES_PER_HOUR", "7")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, 3, cfg.PasswordSetRateLimitPerHour)
	require.Equal(t, 7, cfg.PasswordFailureRateLimitPerHour)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/config/ -v` Expected: FAIL — `cfg.PasswordSetRateLimitPerHour undefined`.

- [ ] **Step 3: Add the fields**

In `apps/api/internal/config/config.go`, inside the `Config` struct, directly after `PasswordRateLimitPerMin`'s block and before `LinkCreateRateLimitPerMin`, add:

```go
	// PasswordSetRateLimitPerHour caps PUT /v1/links/{id}/password per user.
	// The endpoint computes an Argon2id hash — 19 MiB and two passes — so an
	// authenticated member looping it is a CPU and memory amplifier against
	// the function, and no other limit covers that route.
	PasswordSetRateLimitPerHour int

	// PasswordFailureRateLimitPerHour caps failed password attempts per link
	// on POST /{slug}/verify, independently of the per-IP limit beside it.
	// It is sized to protect the function rather than the password: with the
	// policy in internal/auth in place, guessing is bounded by the policy,
	// while the cost of each guess is not bounded by anything else. Only
	// failures count, so a visitor who knows the password spends nothing.
	PasswordFailureRateLimitPerHour int
```

And in `Load`, directly after the `RATE_LIMIT_PASSWORD_PER_MIN` block:

```go
	if cfg.PasswordSetRateLimitPerHour, err = envInt(
		"RATE_LIMIT_PASSWORD_SET_PER_HOUR", 20); err != nil {
		return Config{}, err
	}
	if cfg.PasswordFailureRateLimitPerHour, err = envInt(
		"RATE_LIMIT_PASSWORD_FAILURES_PER_HOUR", 100); err != nil {
		return Config{}, err
	}
```

- [ ] **Step 4: Document both in `.env.example`**

In `apps/api/.env.example`, directly after the `RATE_LIMIT_PASSWORD_PER_MIN=5` block, insert:

```
# Password changes per hour, per user. PUT /v1/links/{id}/password computes an
# Argon2id hash: 19 MiB and two passes, every call. An authenticated member
# looping the endpoint is a CPU and memory amplifier, and no other limit
# guards that route. Twenty an hour is far above any real use — a Verein sets
# a link's password once and rarely changes it.
RATE_LIMIT_PASSWORD_SET_PER_HOUR=20

# Failed password attempts per hour, per link, on POST /{slug}/verify. Sits
# beside RATE_LIMIT_PASSWORD_PER_MIN, which is per link AND per IP and so
# bounds nobody who rotates addresses.
#
# This axis protects the function, not the password. With the policy in
# apps/api/internal/auth/policy.go in place, eight characters that are not
# derived from context outlast any rate anyone can drive over HTTP; what
# stays unbounded is 19 MiB and two Argon2id passes per guess, triggered by
# somebody with no account. A hundred an hour bounds that at a known number
# while staying far too loose to be a practical lockout weapon — sustaining
# it means a hundred failures an hour indefinitely, and the sliding window
# recovers within the hour once it stops.
#
# Only FAILURES count. A visitor who knows the password consumes nothing, so
# a link whose password a whole Verein has been given never approaches this
# no matter how many people follow it.
RATE_LIMIT_PASSWORD_FAILURES_PER_HOUR=100
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/config/ -v` Expected: PASS.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): add the password rate limits"
```

---

### Task 4: `PUT /v1/links/{link_id}/password`

**Files:**

- Modify: `apps/api/internal/db/queries/link_crud.sql`
- Create: `apps/api/internal/api/link_password.go`
- Modify: `apps/api/internal/api/links.go` (add `rowFromSetPassword` beside `rowFromGet`; register the operation inside `registerLinks`)
- Modify: `apps/api/internal/audit/audit.go`
- Test: `apps/api/internal/api/link_password_test.go`
- Test: `apps/api/internal/audit/audit_test.go` (extend the existing known-action lists)

**Interfaces:**

- Consumes: `auth.ValidatePassword`, `auth.PolicyContext` and the five sentinels from Task 1; `Config.PasswordSetRateLimitPerHour` from Task 3.
- Produces: `audit.ActionPasswordSet = "link.password_set"`, `audit.ActionPasswordChanged = "link.password_changed"`, `audit.ActionPasswordRemoved = "link.password_removed"`; the sqlc query `SetLinkPassword`; `rowFromSetPassword(db.SetLinkPasswordRow) linkRow`; `(Deps).allowPasswordSet(ctx, uuid.UUID) error`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/api/link_password_test.go`. Everything it needs already exists in the package's test files — do not add new helpers:

- `newTenancyFixture(t) *tenancyFixture` (`tenancy_test.go`), with `f.do`, `f.pool`, `f.deps`, `f.members[authz.Role…]`.
- `f.createLink(t, slug, dest) linkBody` (`links_test.go`) — note it takes an explicit slug; `linkBody` carries `ID`, `Slug`, `Hostname` and `HasPassword`.
- `f.redirect(t, hostname, slug) *httptest.ResponseRecorder` (`tenancy_test.go`) — note it takes the hostname too.
- `decode[T](t, rec) T` (`tenancy_test.go`).

Audit rows are asserted with a direct query through `f.pool`, the way `links_test.go` already does it.

```go
package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// countAuditActions is the same direct query links_test.go uses for
// link.updated, parameterized because this file asserts three different
// actions on the same entity.
func countAuditActions(t *testing.T, f *tenancyFixture, action string, entityID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = $1 and entity_id = $2`,
		action, entityID).Scan(&count))
	return count
}

func TestSetLinkPasswordProtectsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "mitglieder", "https://example.org/mitglieder")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, decode[linkBody](t, rec).HasPassword,
		"the response must carry has_password so the client needs no refetch")
}

func TestSetLinkPasswordIsRefusedBelowEditor(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nurlesen", "https://example.org/nurlesen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestSetLinkPasswordRejectsAContextDerivedPassword pins the wire contract the
// frontend depends on: the reason travels as a typed ErrorDetail keyed by the
// field, never inside the prose, so a reworded message cannot break it. The
// link's own slug is the context source here — the simplest one to control
// from a test, since the fixture's team name is not.
func TestSetLinkPasswordRejectsAContextDerivedPassword(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sommerfest", "https://example.org/sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "sommerfest2026"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), `"location":"body.password"`)
	require.Contains(t, rec.Body.String(), `"value":"derived_from_context"`)
}

// TestSetLinkPasswordInvalidatesTheRedirectCache is the test that matters most
// in this whole plan, and the reason it drives HandleRedirect rather than
// spying on invalidateLink. link.Cached carries HasPassword, so a freshly
// protected link keeps redirecting straight through until the entry expires —
// up to LinkCacheTTL, one hour. That failure is completely silent: the API
// answers 200, the audit log records the change, the dashboard shows a
// protected link, and visitors sail past.
func TestSetLinkPasswordInvalidatesTheRedirectCache(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "geschuetzt", "https://example.org/geschuetzt")

	// Warm the cache: this redirect populates the entry that must be dropped.
	warm := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusFound, warm.Code)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusOK, after.Code,
		"a protected link must render the interstitial, not redirect")
	require.Empty(t, after.Header().Get("Location"))
}

func TestSetLinkPasswordAuditsSetThenChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "zweimal", "https://example.org/zweimal")

	for _, password := range []string{"Kartoffelsalat!7", "Bratkartoffeln!9"} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
			"/v1/links/"+created.ID.String()+"/password",
			map[string]any{"password": password})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}

	require.Equal(t, 1, countAuditActions(t, f, "link.password_set", created.ID))
	require.Equal(t, 1, countAuditActions(t, f, "link.password_changed", created.ID),
		"the second write must be distinguishable from the first in the log")
}

// TestSetLinkPasswordWritesNoMetadata pins the audit-hygiene rule doc 05 set
// out: the log records that a password changed, never anything about its
// value. audit.ErrForbiddenMetadata refuses a plaintext or a hash, but not an
// empty map, so this is what stops someone adding a well-meant field later.
func TestSetLinkPasswordWritesNoMetadata(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "leer", "https://example.org/leer")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select coalesce(metadata::text, '') from audit_log
		 where action = 'link.password_set' and entity_id = $1`,
		created.ID).Scan(&metadata))
	require.NotContains(t, metadata, "Kartoffelsalat")
	require.NotContains(t, metadata, "argon2")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/api/ -run TestSetLinkPassword -v` Expected: FAIL — every case 404, because no route is registered.

- [ ] **Step 3: Add the query and regenerate**

Append to `apps/api/internal/db/queries/link_crud.sql`:

```sql
-- SetLinkPassword writes password_hash and nothing else. One query serves both
-- PUT and DELETE on the password subresource: removal passes null. The
-- returned column list is UpdateLink's, copied verbatim rather than
-- abbreviated, so linkResponse consumes the generated row unchanged and the
-- two queries stay diffable against each other.

-- name: SetLinkPassword :one
with updated as (
  update link set
    password_hash = $3,
    updated_at = now()
  where link.id = $1 and link.team_id = $2
  returning *
)
select u.id, u.domain_id, u.team_id, d.hostname, u.slug, u.destination_url,
       u.redirect_type, u.state, u.expires_at,
       (u.password_hash is not null)::boolean as has_password,
       u.analytics_enabled, u.folder_id, u.created_by, u.created_at, u.updated_at
from updated u
join domain d on d.id = u.domain_id;
```

Run: `cd apps/api && sqlc generate` Then confirm the generated output actually reproduces: `git diff --stat internal/db` should show only the new query's additions.

- [ ] **Step 4: Add the three audit actions**

In `apps/api/internal/audit/audit.go`, inside the const block directly after `ActionLinkDeleted`:

```go
	// The password subresource gets its own actions rather than folding into
	// link.updated, which is why PATCH excludes the field at all: a password
	// change is worth finding in the log on its own. Set and changed are
	// distinguished because the handler learns it for free — it reads the row
	// before writing, the way updateLink does. Metadata stays empty on all
	// three: ErrForbiddenMetadata already refuses a plaintext or a hash, and
	// the action name is the whole of what happened.
	ActionPasswordSet     Action = "link.password_set"
	ActionPasswordChanged Action = "link.password_changed"
	ActionPasswordRemoved Action = "link.password_removed"
```

And add all three to `knownActions`. Extend whichever list in `audit_test.go` enumerates the taxonomy so `CheckAction` is exercised for them.

- [ ] **Step 5: Add the row converter**

In `apps/api/internal/api/links.go`, directly after `rowFromGet`:

```go
func rowFromSetPassword(r db.SetLinkPasswordRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, FolderID: r.FolderID, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
```

- [ ] **Step 6: Write the handler**

Create `apps/api/internal/api/link_password.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// SetLinkPasswordInput declares its authorization in its type: LinkEditorScope
// resolves which team owns the link and requires at least the editor role, the
// same scope PATCH uses. A password is a property of a link, and whoever may
// change where a link points may also decide who reaches it.
//
// Password carries no minLength/maxLength: Huma would then reject an
// out-of-range value with its own error shape, and two of the policy's five
// reasons would be unreachable over HTTP. ValidatePassword owns all five, so
// every rejection has one shape for the frontend to read.
type SetLinkPasswordInput struct {
	authz.LinkEditorScope
	Body struct {
		Password string `json:"password" doc:"8 to 128 characters. Must not repeat one character, and must not be derived from the link's short path, its destination, or the Verein's name."`
	}
}

// setLinkPassword is PUT /v1/links/{link_id}/password.
//
// The link and the team are read BEFORE the transaction, and the hash is
// computed before it too. Argon2id holds 19 MiB for tens of milliseconds, and
// doing that inside a transaction would pin a pooled Postgres connection for
// the duration on a free tier where connections are the scarce thing. The cost
// is that the slug this validates against could change under us between the
// read and the write; the consequence of that race is a password judged
// against a slug one edit stale, which is not worth a longer transaction.
func (d Deps) setLinkPassword(ctx context.Context, in *SetLinkPasswordInput) (*LinkOutput, error) {
	member := in.Member()

	if err := d.allowPasswordSet(ctx, member.UserID); err != nil {
		return nil, err
	}

	before, err := d.Queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
		ID: in.Link().ID, TeamID: member.TeamID,
	})
	if err != nil {
		return nil, d.linkPasswordReadError(err, in.Link().ID)
	}

	team, err := d.Queries.GetTeam(ctx, member.TeamID)
	if err != nil {
		return nil, d.linkPasswordReadError(err, in.Link().ID)
	}

	if err := auth.ValidatePassword(in.Body.Password, auth.PolicyContext{
		LinkSlug:       before.Slug,
		DestinationURL: before.DestinationURL,
		TeamName:       team.Name,
		TeamSlug:       team.Slug,
	}); err != nil {
		return nil, passwordRejected(err)
	}

	hash, err := auth.HashPassword(in.Body.Password)
	if err != nil {
		d.Log.Error("hash link password", "error", err, "link_id", before.ID)
		return nil, huma.Error500InternalServerError("could not set the password")
	}

	action := audit.ActionPasswordSet
	if before.HasPassword {
		action = audit.ActionPasswordChanged
	}

	var updated linkRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		row, err := q.SetLinkPassword(ctx, db.SetLinkPasswordParams{
			ID: before.ID, TeamID: member.TeamID, PasswordHash: &hash,
		})
		if err != nil {
			return err
		}
		updated = rowFromSetPassword(row)

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      action,
			EntityType:  audit.EntityLink,
			EntityID:    row.ID,
		})
	})
	if err != nil {
		d.Log.Error("set link password", "error", err, "link_id", before.ID)
		return nil, huma.Error500InternalServerError("could not set the password")
	}

	// link.Cached carries HasPassword, so without this the link keeps
	// redirecting straight through for up to LinkCacheTTL. The slug does not
	// change here, so unlike updateLink there is only ever one key.
	d.invalidateLink(ctx, updated.Hostname, updated.Slug)

	return &LinkOutput{Status: http.StatusOK, Body: d.linkResponse(updated)}, nil
}

func (d Deps) linkPasswordReadError(err error, linkID uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return huma.Error404NotFound("link not found")
	}
	d.Log.Error("read link for password change", "error", err, "link_id", linkID)
	return huma.Error500InternalServerError("could not set the password")
}

// passwordRejected turns a policy sentinel into the 422 the frontend reads.
// The reason travels in ErrorDetail.Value keyed by the field, the convention
// deleteDomain established and CLAUDE.md records: the prose below stays free
// to reword, and apps/web keys its message off the token instead. The token is
// the sentinel's own Error() string — see policy_test.go, which pins that.
func passwordRejected(err error) error {
	messages := map[error]string{
		auth.ErrPasswordTooShort:      "the password must be at least 8 characters",
		auth.ErrPasswordTooLong:       "the password must be at most 128 characters",
		auth.ErrPasswordTooRepetitive: "the password must use at least 4 different characters",
		auth.ErrPasswordFromContext:   "the password must not be derived from the link, its destination, or the Verein's name",
		auth.ErrPasswordTooCommon:     "that password is too common",
	}
	for sentinel, message := range messages {
		if errors.Is(err, sentinel) {
			return huma.Error422UnprocessableEntity(message,
				&huma.ErrorDetail{Location: "body.password", Value: sentinel.Error()})
		}
	}
	return huma.Error422UnprocessableEntity("that password cannot be used",
		&huma.ErrorDetail{Location: "body.password", Value: "rejected"})
}

// allowPasswordSet caps the mutation per user. It fails closed on a Redis
// error, matching the rest of the password surface — the members path makes
// the same choice — rather than logging and allowing the way allowLinkCreate
// does. The nil-Cache guard is the local-development affordance only;
// REDIS_URL is required in every deployed environment.
func (d Deps) allowPasswordSet(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.PasswordSetRateLimitPerHour <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:password-set:"+userID.String(), d.Config.PasswordSetRateLimitPerHour, time.Hour)
	if err != nil {
		d.Log.Error("password set rate limit check failed", "error", err)
		return huma.Error500InternalServerError("could not check the password rate limit")
	}
	if !ok {
		return huma.Error429TooManyRequests("too many password changes; try again later")
	}
	return nil
}
```

- [ ] **Step 7: Register the operation**

In `apps/api/internal/api/links.go`, at the end of `registerLinks`:

```go
	huma.Register(api, huma.Operation{
		OperationID: "set-link-password",
		Method:      http.MethodPut,
		Path:        "/v1/links/{link_id}/password",
		Summary:     "Set or change a link's password",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.setLinkPassword)
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run TestSetLinkPassword -v && go test ./internal/audit/` Expected: PASS.

- [ ] **Step 9: Prove the invalidation test can fail**

Temporarily delete the `d.invalidateLink(...)` line from `setLinkPassword`, run `cd apps/api && go test ./internal/api/ -run TestSetLinkPasswordInvalidatesTheRedirectCache`, and confirm it FAILS on the interstitial assertion. Then restore the line and confirm it passes again. A test for a silent failure that has never been seen to fail is not yet a test.

- [ ] **Step 10: Run the gates**

Run: `cd apps/api && gofmt -l internal && go vet ./... && go test ./...` Expected: clean.

- [ ] **Step 11: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): add PUT links/{id}/password"
```

---

### Task 5: `DELETE /v1/links/{link_id}/password`

**Files:**

- Modify: `apps/api/internal/api/link_password.go`
- Modify: `apps/api/internal/api/links.go` (register the operation)
- Test: `apps/api/internal/api/link_password_test.go`

**Interfaces:**

- Consumes: `SetLinkPassword` and `rowFromSetPassword` from Task 4; `audit.ActionPasswordRemoved` from Task 4.
- Produces: nothing later tasks import.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/api/link_password_test.go`, reusing the helpers Task 4 already established there.

```go
func TestRemoveLinkPasswordUnprotectsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "wiederfrei", "https://example.org/wiederfrei")

	set := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, decode[linkBody](t, rec).HasPassword)
	require.Equal(t, 1, countAuditActions(t, f, "link.password_removed", created.ID))
}

// TestRemoveLinkPasswordIsIdempotent pins the choice the spec made: DELETE on
// a link that has no password answers 200 with the link unchanged. A 404 there
// would say nothing the caller does not already know while forcing every
// client to special-case it.
func TestRemoveLinkPasswordIsIdempotent(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "niegeschuetzt", "https://example.org/niegeschuetzt")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, decode[linkBody](t, rec).HasPassword)
}

func TestRemoveLinkPasswordIsRefusedBelowEditor(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nichtentfernen", "https://example.org/nichtentfernen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestRemoveLinkPasswordInvalidatesTheRedirectCache is the mirror of the set
// case and fails the other way round: without invalidation a visitor keeps
// being asked for a password the Verein has already withdrawn.
func TestRemoveLinkPasswordInvalidatesTheRedirectCache(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "cachefrei", "https://example.org/cachefrei")

	set := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	warm := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusOK, warm.Code, "the interstitial must be cached first")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusFound, after.Code,
		"an unprotected link must redirect again, not keep asking for a password")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/api/ -run TestRemoveLinkPassword -v` Expected: FAIL — 404, no route registered.

- [ ] **Step 3: Write the handler**

Append to `apps/api/internal/api/link_password.go`:

```go
// RemoveLinkPasswordInput declares its authorization in its type: the same
// LinkEditorScope the setter uses.
type RemoveLinkPasswordInput struct {
	authz.LinkEditorScope
}

// removeLinkPassword is DELETE /v1/links/{link_id}/password. It answers 200
// with the link on a link that has no password too: DELETE is idempotent, and
// a 404 there would say nothing the caller does not already know.
//
// It carries no rate limit of its own. Unlike the setter it computes no hash,
// so it is an ordinary authenticated write with nothing to amplify.
func (d Deps) removeLinkPassword(
	ctx context.Context, in *RemoveLinkPasswordInput,
) (*LinkOutput, error) {
	member := in.Member()

	var updated linkRow
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		row, err := q.SetLinkPassword(ctx, db.SetLinkPasswordParams{
			ID: in.Link().ID, TeamID: member.TeamID, PasswordHash: nil,
		})
		if err != nil {
			return err
		}
		updated = rowFromSetPassword(row)

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionPasswordRemoved,
			EntityType:  audit.EntityLink,
			EntityID:    row.ID,
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("link not found")
	case err != nil:
		d.Log.Error("remove link password", "error", err, "link_id", in.Link().ID)
		return nil, huma.Error500InternalServerError("could not remove the password")
	}

	// Same reason as the setter, in the other direction: without this the
	// interstitial keeps being served for a link the Verein has unprotected.
	d.invalidateLink(ctx, updated.Hostname, updated.Slug)

	return &LinkOutput{Status: http.StatusOK, Body: d.linkResponse(updated)}, nil
}
```

- [ ] **Step 4: Register the operation**

In `apps/api/internal/api/links.go`, at the end of `registerLinks`, after the setter:

```go
	huma.Register(api, huma.Operation{
		OperationID: "remove-link-password",
		Method:      http.MethodDelete,
		Path:        "/v1/links/{link_id}/password",
		Summary:     "Remove a link's password",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.removeLinkPassword)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'LinkPassword' -v` Expected: PASS, both the set and the remove suites.

- [ ] **Step 6: Confirm the tenancy matrix still covers the new routes**

`apps/api/internal/api/tenancy_test.go` holds the matrix that proves a non-member gets 404 on every entity-scoped route. Read it, and if it enumerates routes explicitly, add both password routes. Run: `cd apps/api && go test ./internal/api/ -run Tenancy -v` Expected: PASS.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): add DELETE links/{id}/password"
```

---

### Task 6: Cap password failures per link

**Files:**

- Modify: `apps/api/internal/api/verify.go`
- Test: `apps/api/internal/api/verify_test.go`

**Interfaces:**

- Consumes: `(*cache.Client).WithinLimit` and `.Increment` from Task 2; `Config.PasswordFailureRateLimitPerHour` from Task 3.
- Produces: nothing later tasks import.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/api/verify_test.go`. That file already has everything needed — do not add helpers:

- `protectedFixture(t, password) *fixture` builds a link protected by that password; its slug is `hello` and its hostname is `f.hostname`.
- `postPassword(t, f, slug, password, ip) *httptest.ResponseRecorder` submits the interstitial form; the address goes out as `X-Forwarded-For`, which is what `ClientIP` reads.

**A warning about the neighbourhood:** `TestVerifySubmitRateLimitsTightlyPerLinkAndIP` in this same file flaked once in CI on 2026-09-08 (expected 429, got 401) and is being investigated separately. If it fails while you are working here, check whether your change actually caused it before assuming it did.

```go
// TestVerifySubmitCapsFailuresPerLinkAcrossAddresses pins the axis the per-IP
// limit cannot cover. Every request here comes from a DIFFERENT address, so
// the existing per-link-per-IP limit lets all of them through; only the
// link-keyed failure counter can refuse the last one.
func TestVerifySubmitCapsFailuresPerLinkAcrossAddresses(t *testing.T) {
	f := protectedFixture(t, "Kartoffelsalat!7")
	f.deps.Config.PasswordFailureRateLimitPerHour = 2

	for i := range 2 {
		require.Equal(t, http.StatusUnauthorized,
			postPassword(t, f, "hello", "wrong", fmt.Sprintf("203.0.113.%d", i+1)).Code)
	}

	require.Equal(t, http.StatusTooManyRequests,
		postPassword(t, f, "hello", "wrong", "203.0.113.99").Code,
		"a third failure from a third address must hit the per-link cap")
}

// TestVerifySubmitDoesNotChargeACorrectPassword is the property the whole
// two-script split in internal/cache exists for. A link whose password a whole
// Verein has been given must never approach the failure cap, no matter how
// many people follow it.
func TestVerifySubmitDoesNotChargeACorrectPassword(t *testing.T) {
	f := protectedFixture(t, "Kartoffelsalat!7")
	f.deps.Config.PasswordFailureRateLimitPerHour = 2

	for i := range 5 {
		require.Equal(t, http.StatusFound,
			postPassword(t, f, "hello", "Kartoffelsalat!7", fmt.Sprintf("198.51.100.%d", i+1)).Code,
			"attempt %d must redirect", i+1)
	}
}
```

`fmt` joins the file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/api/ -run TestVerifySubmit -v` Expected: FAIL — the third failure answers 401, not 429.

- [ ] **Step 3: Add the two helpers**

Append to `apps/api/internal/api/verify.go`:

```go
// passwordFailureWindow is the window PasswordFailureRateLimitPerHour is
// measured over.
const passwordFailureWindow = time.Hour

func passwordFailureKey(hostname, slug string) string {
	return "rl:pwfail:" + hostname + ":" + slug
}

// allowPasswordAttempt is the link-keyed half of the verify path's rate
// limiting, independent of the client address. It reports whether one more
// failure would stay inside the cap WITHOUT recording one, because the caller
// does not yet know whether this attempt is a failure — and the whole point
// of the axis is that a visitor who knows the password spends nothing.
//
// Fails closed, like the per-IP check beside it: an unbounded number of
// Argon2id verifications is a worse outcome than a temporarily unusable
// protected link.
func (d Deps) allowPasswordAttempt(ctx context.Context, key string) bool {
	if d.Config.PasswordFailureRateLimitPerHour <= 0 {
		return true
	}

	within, err := d.Cache.WithinLimit(ctx, key,
		d.Config.PasswordFailureRateLimitPerHour, passwordFailureWindow)
	if err != nil {
		d.Log.Error("password failure limit unavailable, failing closed", "error", err)
		return false
	}
	return within
}

// countPasswordFailure records one failed attempt. Best effort: the guess has
// already been made and refused, so a Redis error here must not turn a wrong
// password into a server error for the visitor. It is logged loudly instead,
// which is also how a failing counter becomes visible at all.
func (d Deps) countPasswordFailure(ctx context.Context, key string) {
	if d.Config.PasswordFailureRateLimitPerHour <= 0 {
		return
	}
	if err := d.Cache.Increment(ctx, key, passwordFailureWindow); err != nil {
		d.Log.Error("recording a password failure failed", "error", err)
	}
}
```

- [ ] **Step 4: Wire both into `HandleVerifySubmit`**

In `apps/api/internal/api/verify.go`, directly after the existing per-IP rate-limit block that renders `KindRateLimited`, insert:

```go
	// The second axis: per link, independent of the address, so somebody
	// rotating addresses is bounded too. Consulted before the Argon2id
	// verification below, which is the expensive thing it exists to cap.
	failureKey := passwordFailureKey(hostname, slug)
	if !d.allowPasswordAttempt(ctx, failureKey) {
		w.Header().Set("Retry-After", "3600")
		pages.RenderError(w, http.StatusTooManyRequests, locale, pages.KindRateLimited)
		return
	}
```

And change the wrong-password branch from:

```go
	if !valid {
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}
```

to:

```go
	if !valid {
		// After the verification, not before: the counter's meaning is
		// "failures", not "attempts".
		d.countPasswordFailure(ctx, failureKey)
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run TestVerify -v` Expected: PASS, including the pre-existing verify tests.

- [ ] **Step 6: Run the full Go suite**

Run: `cd apps/api && gofmt -l internal && go vet ./... && go test ./... -count=1` Expected: clean.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(api): cap password failures per link"
```

---

### Task 7: Mirror the policy in TypeScript

**Files:**

- Create: `apps/web/src/lib/link-password.ts`
- Test: `apps/web/src/lib/link-password.test.ts`

**Interfaces:**

- Consumes: the rules from Task 1 (reimplemented, not imported — different language, different runtime).
- Produces: `validateLinkPassword(password: string, context: LinkPasswordContext): LinkPasswordReason | null` and `type LinkPasswordContext = { linkSlug: string; destinationUrl: string; teamName: string; teamSlug: string }` and `type LinkPasswordReason = 'too_short' | 'too_long' | 'too_repetitive' | 'derived_from_context' | 'too_common'`.

**Context for the implementer:** this is a second implementation of the same rules and it will drift from the Go one. That is accepted, not a mistake to solve — the API stays the enforcement point, and drift costs a message one round trip later than it could have arrived. Do **not** build a shared fixture across `apps/api` and `apps/web` to prevent it.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/lib/link-password.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { validateLinkPassword } from './link-password';

const gruenwald = {
	destinationUrl: 'https://www.sv-gruenwald.de/verein/sommerfest',
	linkSlug: 'sommerfest-2026',
	teamName: 'SV Grünwald e.V.',
	teamSlug: 'sv-gruenwald',
};

describe('validateLinkPassword', () => {
	it('accepts an unrelated passphrase', () => {
		expect(validateLinkPassword('Kartoffelsalat!7', gruenwald)).toBeNull();
	});

	it.each([
		['Abcdef1', 'too_short'],
		['abababab', 'too_repetitive'],
		['!!!!!!!!', 'too_repetitive'],
		['sommerfest2026', 'derived_from_context'],
		['sommerfest', 'derived_from_context'],
		['Gruenwald2026', 'derived_from_context'],
		['Passwort!', 'too_common'],
	])('rejects %s as %s', (password, reason) => {
		expect(validateLinkPassword(password, gruenwald)).toBe(reason);
	});

	it('rejects a password longer than 128 characters', () => {
		const long = Array.from({ length: 129 }, (_, i) => String.fromCharCode(97 + (i % 26))).join('');
		expect(validateLinkPassword(long, gruenwald)).toBe('too_long');
	});

	// The same limit the Go policy pins, for the same reason: it is the
	// documented floor, not an oversight, and tightening it is a decision.
	it('accepts a weak but compliant password', () => {
		expect(validateLinkPassword('passwort1', gruenwald)).toBeNull();
	});
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/web && pnpm test -- link-password` Expected: FAIL — cannot resolve `./link-password`.

- [ ] **Step 3: Write the mirror**

Create `apps/web/src/lib/link-password.ts`:

```ts
/**
 * A browser-side copy of the link-password policy that
 * `apps/api/internal/auth/policy.go` enforces, so a rejection is immediate
 * rather than a round trip away.
 *
 * The API remains the enforcement point. This will drift from the Go rules,
 * and that is accepted: the worst outcome of drift is a message arriving a
 * round trip later than it could have, because a 422 still renders under the
 * field. Keep the reason tokens identical to the Go sentinels' `Error()`
 * strings — those are the wire contract, and `api-errors.ts` reads them back
 * out of the response.
 */

export type LinkPasswordReason =
	'derived_from_context' | 'too_common' | 'too_long' | 'too_repetitive' | 'too_short';

export type LinkPasswordContext = {
	destinationUrl: string;
	linkSlug: string;
	teamName: string;
	teamSlug: string;
};

export const MIN_LINK_PASSWORD_LENGTH = 8;
export const MAX_LINK_PASSWORD_LENGTH = 128;

const MIN_DISTINCT_CHARACTERS = 4;
const MIN_CONTEXT_TOKEN = 4;
const MIN_NORMALIZED_FOR_CONTEXT = 3;

/**
 * The same short list the Go policy embeds, trimmed to the entries a person
 * types into a browser. Compared for equality against the normalized
 * password, never as a substring — substring matching would reject
 * `meinpasswortistlang` for containing `passwort`, and the false rejections
 * are harder to explain to a Verein than the passwords they would catch.
 */
const COMMON_PASSWORDS = new Set([
	'passwort',
	'password',
	'passwort1',
	'password1',
	'12345678',
	'123456789',
	'qwertz',
	'qwerty',
	'geheim',
	'willkommen',
	'welcome',
	'verein',
	'vereinsheim',
	'mitglieder',
	'sommerfest',
	'vorstand',
	'letmein',
	'fussball',
	'admin123',
	'test1234',
]);

/**
 * German characters transliterated, lower case, everything outside [a-z0-9]
 * dropped — the same fold `normalizeForPolicy` performs in Go. The
 * transliteration is load-bearing: without it a team called `SV Grünwald`
 * does not catch `Gruenwald2026`, which is the password that team will pick.
 */
function normalize(value: string): string {
	return value
		.toLowerCase()
		.replaceAll('ä', 'ae')
		.replaceAll('ö', 'oe')
		.replaceAll('ü', 'ue')
		.replaceAll('ß', 'ss')
		.replaceAll(/[^a-z0-9]/gu, '');
}

function distinctCharacters(value: string): number {
	return new Set(value).size;
}

/**
 * The destination's hostname with a leading `www.` and its last label
 * removed, so `https://www.sv-gruenwald.de/verein` contributes
 * `sv-gruenwald` rather than `de`. An unparsable URL contributes nothing.
 */
function destinationLabel(destinationUrl: string): string {
	try {
		const labels = new URL(destinationUrl).hostname.replace(/^www\./u, '').split('.');
		return (labels.length > 1 ? labels.slice(0, -1) : labels).join('.');
	} catch {
		return '';
	}
}

function contextTokens(context: LinkPasswordContext): string[] {
	const sources = [context.linkSlug, context.teamName, context.teamSlug];
	const label = destinationLabel(context.destinationUrl);
	if (label !== '') sources.push(label);

	return sources
		.flatMap((source) => [source, ...source.split(/[-._\s]+/u)])
		.map(normalize)
		.filter((token) => token.length >= MIN_CONTEXT_TOKEN);
}

/** Returns the reason the password is refused, or `null` when it passes. */
export function validateLinkPassword(
	password: string,
	context: LinkPasswordContext,
): LinkPasswordReason | null {
	const characters = [...password];
	if (characters.length < MIN_LINK_PASSWORD_LENGTH) return 'too_short';
	if (characters.length > MAX_LINK_PASSWORD_LENGTH) return 'too_long';
	if (distinctCharacters(password) < MIN_DISTINCT_CHARACTERS) return 'too_repetitive';

	const normalized = normalize(password);
	if (COMMON_PASSWORDS.has(normalized)) return 'too_common';
	if (normalized.length < MIN_NORMALIZED_FOR_CONTEXT) return null;

	for (const token of contextTokens(context)) {
		if (normalized.includes(token) || token.includes(normalized)) return 'derived_from_context';
	}
	return null;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/web && pnpm test -- link-password` Expected: PASS.

- [ ] **Step 5: Run the gates**

Run: `pnpm lint && pnpm typecheck` Expected: both exit 0. Read the exit status, do not read only the last line of output.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(web): mirror the password policy"
```

---

### Task 8: Web plumbing — client, server functions, error classification

**Files:**

- Modify: `packages/api-client/**` (generated; do not hand-edit)
- Modify: `apps/api/openapi.json` (generated)
- Modify: `apps/web/src/server/links.ts`
- Modify: `apps/web/src/lib/api-errors.ts`
- Test: `apps/web/src/lib/api-errors.test.ts`

**Interfaces:**

- Consumes: the two endpoints from Tasks 4 and 5; `LinkPasswordReason` from Task 7.
- Produces: `setLinkPasswordFn({ data: { linkId: string; password: string } }): Promise<Link>`, `removeLinkPasswordFn({ data: { linkId: string } }): Promise<Link>`, and the new `ApiFailure` variant `{ kind: 'passwordRejected'; reason: LinkPasswordReason | 'rejected' }`.

- [ ] **Step 1: Regenerate the client**

Run: `pnpm generate:api`

This writes `apps/api/openapi.json`, regenerates `packages/api-client`, and runs `pnpm format`. Confirm the diff contains `setLinkPassword` and `removeLinkPassword` and nothing unrelated.

- [ ] **Step 2: Write the failing test**

Append to `apps/web/src/lib/api-errors.test.ts`, following that file's existing way of building a fake error response:

```ts
// A 422 on the password field carries a typed reason, and it has to be
// read BEFORE the generic field-error branch: `fieldsOf` would otherwise
// swallow it into `{ kind: 'fields' }` and the card would render the
// API's English prose instead of the German the policy deserves.
it('classifies a rejected link password by its typed reason', () => {
	const failure = classifyApiError(
		problemResponse(422, [{ location: 'body.password', value: 'derived_from_context' }]),
	);

	expect(failure).toEqual({ kind: 'passwordRejected', reason: 'derived_from_context' });
});

it('still classifies other 422s as field errors', () => {
	const failure = classifyApiError(
		problemResponse(422, [{ location: 'body.destination_url', message: 'must be https' }]),
	);

	expect(failure.kind).toBe('fields');
});
```

Use whatever helper the file already has in place of `problemResponse`; do not add a second one.

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd apps/web && pnpm test -- api-errors` Expected: FAIL — the first case classifies as `fields`.

- [ ] **Step 4: Extend the classifier**

In `apps/web/src/lib/api-errors.ts`, add the variant to `ApiFailure`:

```ts
	| { kind: 'passwordRejected'; reason: LinkPasswordReason | 'rejected' }
```

Add the extractor beside `isSlugConflict`:

```ts
/**
 * `setLinkPassword` answers a policy violation with 422 and a typed detail on
 * the field, the same convention `deleteDomain`'s blocking-link count and
 * `createTeam`'s taken slug already use. The token is the Go sentinel's own
 * `Error()` string, pinned by `policy_test.go`, so it is a wire contract
 * rather than prose — reading it here rather than matching the message means
 * a reworded message cannot silently turn a precise reason into a generic
 * failure. There is deliberately no text fallback.
 */
function passwordRejectionOf(error: unknown): string | undefined {
	for (const detail of problemDetailsOf(error)) {
		if (detail.location === 'body.password' && typeof detail.value === 'string') {
			return detail.value;
		}
	}
	return undefined;
}
```

And in `classifyApiError`, inside the existing `status === 400 || status === 422` branch, **before** the `fieldsOf` call:

```ts
const reason = passwordRejectionOf(error);
if (reason !== undefined) {
	return { kind: 'passwordRejected', reason: reason as LinkPasswordReason | 'rejected' };
}
```

Import `LinkPasswordReason` as a type from `./link-password`.

- [ ] **Step 5: Add the server functions**

Append to `apps/web/src/server/links.ts`, following the `...For`/`...Fn` split every other function in the file uses:

```ts
/**
 * Same `...For`/`...Fn` split and the same reasoning as `updateLinkFor`. Both
 * password calls return the whole `Link` rather than nothing, because the API
 * answers with it — the card needs `has_password` back and would otherwise
 * refetch every time.
 */
export const setLinkPasswordFor = createServerOnlyFn(
	async (request: Request, linkId: string, password: string): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await setLinkPassword({
			body: { password },
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

export const setLinkPasswordFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string; password: string }) => data)
	.handler(async ({ data }) => setLinkPasswordFor(getRequest(), data.linkId, data.password));

export const removeLinkPasswordFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await removeLinkPassword({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

export const removeLinkPasswordFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => removeLinkPasswordFor(getRequest(), data.linkId));
```

Add `removeLinkPassword` and `setLinkPassword` to the existing `@kurze-url/api-client` import.

- [ ] **Step 6: Run the tests and gates**

Run: `cd apps/web && pnpm test && cd ../.. && pnpm lint && pnpm typecheck` Expected: all pass; check exit statuses, not just the tail of the output.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(web): add password server functions"
```

---

### Task 9: The password protection card

**Files:**

- Create: `apps/web/src/components/link-password-card.tsx`
- Create: `apps/web/src/components/link-password-card.test.tsx`
- Create: `apps/web/src/components/link-password-card.stories.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`
- Modify: `apps/web/src/components/link-list.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`
- Modify: `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `validateLinkPassword`, `LinkPasswordContext`, `LinkPasswordReason` from Task 7; `setLinkPasswordFn`, `removeLinkPasswordFn` and the `passwordRejected` failure from Task 8.
- Produces: `<LinkPasswordCard>`; no later task imports it.

**Context for the implementer:** the card is deliberately **not** a field inside `<LinkForm>`. `<LinkForm>` maps to `PATCH`, which excludes `password` on purpose, and the value is write-only — the server holds an Argon2id hash and cannot produce the password, so there is no initial value to seed an input with. A permanently blank field inside a form that otherwise round-trips the link's current state would read as "the password is empty".

- [ ] **Step 1: Add the translation keys**

In `apps/web/src/i18n/locales/en.json`, inside the existing `links` object:

```json
	"passwordHeading": "Password protection",
	"passwordExplainer": "Visitors have to enter this password before the link takes them anywhere. Share it with the people who should get through.",
	"passwordUnprotected": "This link is not protected.",
	"passwordProtected": "This link is protected by a password.",
	"passwordLabel": "Password",
	"passwordProtect": "Protect this link",
	"passwordChange": "Change password",
	"passwordRemove": "Remove protection",
	"passwordRemoveQuestion": "Remove the password? Anyone with the short URL will get straight through afterwards.",
	"passwordRemoveConfirm": "Yes, remove it",
	"passwordBadge": "Password protected",
	"passwordTooShort": "Use at least 8 characters.",
	"passwordTooLong": "Use at most 128 characters.",
	"passwordTooRepetitive": "Use at least 4 different characters.",
	"passwordDerivedFromContext": "Too easy to guess from this link, its destination, or the Verein's name.",
	"passwordTooCommon": "That password is one of the most common ones. Pick another.",
	"passwordRejected": "That password cannot be used."
```

And the German counterparts in `de.json`, in the same order:

```json
	"passwordHeading": "Passwortschutz",
	"passwordExplainer": "Besucherinnen und Besucher müssen dieses Passwort eingeben, bevor der Link sie weiterleitet. Gib es an die Personen weiter, die durchkommen sollen.",
	"passwordUnprotected": "Dieser Link ist nicht geschützt.",
	"passwordProtected": "Dieser Link ist durch ein Passwort geschützt.",
	"passwordLabel": "Passwort",
	"passwordProtect": "Link schützen",
	"passwordChange": "Passwort ändern",
	"passwordRemove": "Schutz entfernen",
	"passwordRemoveQuestion": "Passwort entfernen? Wer die Kurz-URL hat, kommt danach ohne Umweg durch.",
	"passwordRemoveConfirm": "Ja, entfernen",
	"passwordBadge": "Passwortgeschützt",
	"passwordTooShort": "Mindestens 8 Zeichen verwenden.",
	"passwordTooLong": "Höchstens 128 Zeichen verwenden.",
	"passwordTooRepetitive": "Mindestens 4 verschiedene Zeichen verwenden.",
	"passwordDerivedFromContext": "Zu leicht aus diesem Link, seinem Ziel oder dem Vereinsnamen zu erraten.",
	"passwordTooCommon": "Dieses Passwort gehört zu den häufigsten. Bitte ein anderes wählen.",
	"passwordRejected": "Dieses Passwort kann nicht verwendet werden."
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/link-password-card.test.tsx`, following the setup `link-form.test.tsx` uses for i18n and user events:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { LinkPasswordCard } from './link-password-card';

const context = {
	destinationUrl: 'https://www.sv-gruenwald.de/verein/sommerfest',
	linkSlug: 'sommerfest-2026',
	teamName: 'SV Grünwald e.V.',
	teamSlug: 'sv-gruenwald',
};

describe('LinkPasswordCard', () => {
	it('offers to protect an unprotected link', () => {
		render(
			<LinkPasswordCard context={context} hasPassword={false} onRemove={vi.fn()} onSet={vi.fn()} />,
		);

		expect(screen.getByText('This link is not protected.')).toBeInTheDocument();
	});

	it('reports a protected link and offers removal', () => {
		render(<LinkPasswordCard context={context} hasPassword onRemove={vi.fn()} onSet={vi.fn()} />);

		expect(screen.getByText('This link is protected by a password.')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Remove protection' })).toBeInTheDocument();
	});

	it('refuses a context-derived password without calling the server', async () => {
		const onSet = vi.fn();
		render(
			<LinkPasswordCard context={context} hasPassword={false} onRemove={vi.fn()} onSet={onSet} />,
		);

		await userEvent.type(screen.getByLabelText('Password'), 'sommerfest2026');
		await userEvent.click(screen.getByRole('button', { name: 'Protect this link' }));

		expect(
			screen.getByText("Too easy to guess from this link, its destination, or the Verein's name."),
		).toBeInTheDocument();
		expect(onSet).not.toHaveBeenCalled();
	});

	it('submits a password that passes the mirrored policy', async () => {
		const onSet = vi.fn();
		render(
			<LinkPasswordCard context={context} hasPassword={false} onRemove={vi.fn()} onSet={onSet} />,
		);

		await userEvent.type(screen.getByLabelText('Password'), 'Kartoffelsalat!7');
		await userEvent.click(screen.getByRole('button', { name: 'Protect this link' }));

		expect(onSet).toHaveBeenCalledWith('Kartoffelsalat!7');
	});

	// The mirrored policy is convenience; the API is truth. A reason the
	// browser did not predict still has to reach the reader.
	it('renders a rejection the API reported', () => {
		render(
			<LinkPasswordCard
				context={context}
				hasPassword={false}
				onRemove={vi.fn()}
				onSet={vi.fn()}
				rejection="too_common"
			/>,
		);

		expect(
			screen.getByText('That password is one of the most common ones. Pick another.'),
		).toBeInTheDocument();
	});
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd apps/web && pnpm test -- link-password-card` Expected: FAIL — cannot resolve `./link-password-card`.

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/link-password-card.tsx`.

"Card" is the role, not a component: `link-form.tsx` is plain `<form>`, `<label>` and `<input>` with one `Button` from `./ui/button`, and this follows it. Its error handling is the pattern to copy exactly — an `errorId`, `aria-describedby={errorMessage ? errorId : undefined}` and `aria-invalid={errorMessage ? true : undefined}` on the input, with the message rendered in an element carrying that id. Reuse `<ConfirmDelete label question onConfirm>` from `./confirm-delete` for removal; it arms on the first click and only calls `onConfirm` on the second, and `question` is a rendered string rather than a key.

Its props:

```tsx
type LinkPasswordCardProps = {
	context: LinkPasswordContext;
	hasPassword: boolean;
	/** A reason the API returned that the mirrored policy did not predict. */
	rejection?: LinkPasswordReason | 'rejected';
	onRemove: () => void;
	onSet: (password: string) => void;
};
```

Behaviour, in the order the test asserts it:

1. `hasPassword === false` renders `links.passwordUnprotected`, the input labelled `links.passwordLabel` (`type="password"`, `autoComplete="new-password"`), and a submit labelled `links.passwordProtect`.
2. `hasPassword === true` renders `links.passwordProtected`, a control labelled `links.passwordChange` that reveals the same input, and `<ConfirmDelete>` wired to `onRemove` with `links.passwordRemoveQuestion` and `links.passwordRemoveConfirm`.
3. On submit, call `validateLinkPassword(value, context)` first. A non-null reason renders the matching message and **does not** call `onSet`.
4. `rejection` renders the same message set, so an API reason the mirror missed still reaches the reader.
5. The message element is associated with the input through `aria-describedby`, and the input carries `aria-invalid` while a reason is showing — the same wiring `link-form.tsx` uses for `destination_url`, and what the Storybook accessibility check looks for.

Map reasons to keys with one lookup, so a new reason is a compile error rather than a blank message:

```tsx
const messageKeys: Record<LinkPasswordReason | 'rejected', string> = {
	derived_from_context: 'links.passwordDerivedFromContext',
	rejected: 'links.passwordRejected',
	too_common: 'links.passwordTooCommon',
	too_long: 'links.passwordTooLong',
	too_repetitive: 'links.passwordTooRepetitive',
	too_short: 'links.passwordTooShort',
};
```

- [ ] **Step 5: Wire the card into the link detail route**

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, render `<LinkPasswordCard>` below the existing `<LinkForm>`, with two `useMutation` hooks calling `setLinkPasswordFn` and `removeLinkPasswordFn`. On success, write the returned `Link` into the query cache the same way the existing update mutation does, so `has_password` refreshes without a refetch. On error, run `classifyApiError` and pass a `passwordRejected` failure's `reason` into the card's `rejection` prop; every other failure kind renders through the banner the route already has.

The `context` prop costs no request. `_authed.tsx` already publishes `context.me.memberships` into route context, and each `TeamMembership` carries `name` and `slug` — the same list `requireTeamId` resolves the slug against in this route's own `beforeLoad`:

```tsx
const membership = me.memberships.find((candidate) => candidate.slug === params.teamSlug);
const passwordContext = {
	destinationUrl: link.destination_url,
	linkSlug: link.slug,
	teamName: membership?.name ?? '',
	teamSlug: params.teamSlug,
};
```

An empty `teamName` is unreachable — `beforeLoad` has already thrown `notFound()` for a slug with no membership — and the `?? ''` is there so the type is `string` without a non-null assertion.

- [ ] **Step 6: Add the lock badge to the link list**

In `apps/web/src/components/link-list.tsx`, render a lock icon from `lucide-react` beside a link whose `has_password` is true, with `links.passwordBadge` as its accessible name. The field is already on the list response; no request changes.

- [ ] **Step 7: Write the Storybook story**

Create `apps/web/src/components/link-password-card.stories.tsx` with one story per state — unprotected, protected, and showing a rejection — following the pattern in `link-form.stories.tsx`. This is what carries the component's accessibility check.

- [ ] **Step 8: Run the tests and gates**

Run: `cd apps/web && pnpm test && pnpm test:storybook && cd ../.. && pnpm lint && pnpm typecheck` Expected: all pass. `routeTree.gen.ts` must not appear in `git status` — no route file was added or removed.

- [ ] **Step 9: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "feat(web): add the password protection card"
```

---

### Task 10: End-to-end coverage and the documentation

**Files:**

- Modify: `apps/web/e2e/links.spec.ts`
- Modify: `CLAUDE.md`
- Modify: `docs/planning/05-database-schema.md`
- Modify: `docs/planning/06-api-design.md`

**Interfaces:**

- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Extend the e2e spec**

Append a case to `apps/web/e2e/links.spec.ts` following that file's existing structure: create a link, open its detail page, protect it, assert the card reports the protected state and the list shows the badge, then remove protection and assert both revert.

The interstitial itself is **not** reachable from e2e and must not be attempted: Preview's shared hostname is `short.invalid`, which deliberately does not resolve — that is load-bearing for the `warns that the short domain does not resolve` spec. The interstitial stays covered by the Go tests from Task 6.

Run: read `apps/web/e2e/README` or the spec's own header for how the suite is invoked locally; e2e runs against a Vercel preview, not on every push.

- [ ] **Step 2: Record the decision in `CLAUDE.md`**

In the API-surface summary, `PUT|DELETE /links/{id}/password` is already listed — leave it. Add one entry to "Non-obvious constraints", after the rate-limit entry:

```markdown
- **A link's password lives on its own route, and both ends of it must invalidate the redirect cache.** `PUT|DELETE /v1/links/{link_id}/password` (`apps/api/internal/api/link_password.go`) is separate from `PATCH` so it gets its own audit actions (`link.password_set`, `link.password_changed`, `link.password_removed`) and its own rate limit — the precedent the folders-and-tags decision cites for refusing a tag subresource, which has neither. `link.Cached` carries `HasPassword`, so a handler that forgets `invalidateLink` leaves a freshly protected link redirecting straight through for up to an hour, with a 200, an audit row and a dashboard that all say it worked; `TestSetLinkPasswordInvalidatesTheRedirectCache` drives the real redirect handler for exactly that reason. The policy in `apps/api/internal/auth/policy.go` targets predictability rather than strength — 8 characters, 4 distinct, and rejection of anything derived from the link's slug, its destination's hostname, or the Verein's name and slug after German transliteration — because a link password is shared out of band with a group and `Sommerfest26` is the realistic failure, not a short one. It is mirrored in `apps/web/src/lib/link-password.ts` for immediate feedback and **will drift**; the API is the enforcement point, and the reason travels as `ErrorDetail{Location: "body.password", Value: "<token>"}` where the token is the Go sentinel's own `Error()` string. Creating a link with a password in one request is deliberately not possible: reopen that when bulk create or import arrives and the two-step window stops being milliseconds.
```

- [ ] **Step 3: Close the open question in doc 05**

In `docs/planning/05-database-schema.md`, under "Password protection", the brute-force bullet says the limit should be scoped "per-link (or per-link-per-IP)". Replace that parenthetical with the settled answer: both axes exist, per link **and** per IP is the tight per-minute one, per link alone is a per-hour cap on failures only, sized to bound Argon2id cost rather than guessing. Add a sentence recording that the policy itself is now decided and where it lives.

- [ ] **Step 4: Settle the response shapes in doc 06**

In `docs/planning/06-api-design.md`, the two password endpoints are described but carry no response shape and name only two of the three audit actions. Record: both answer 200 with the full link, `DELETE` is idempotent, a policy violation is 422 with a typed `ErrorDetail` on `body.password`, and the third action `link.password_removed` exists.

- [ ] **Step 5: Run everything**

Run: `cd apps/api && gofmt -l internal && go vet ./... && go test ./... -count=1` Run: `pnpm lint && pnpm typecheck && pnpm --filter @kurze-url/web test` Expected: all clean.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-password -m "docs: record link password management"
```

---

## After the last task

The branch adds no migration, so it needs no hand-applied Preview schema change — the one thing that has broken e2e on the last two feature branches does not apply here.

Use `superpowers:finishing-a-development-branch`. Pull requests in this repository are merged automatically once every required check passes, so poll the merge state after opening one and clean the workspace rather than leaving that to the maintainer.
