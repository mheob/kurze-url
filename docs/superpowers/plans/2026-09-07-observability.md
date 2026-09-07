# Observability: Keep-Alive, Uptime and Error Tracking — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Errors from both apps land durably in Sentry, an outage is noticed by Better Stack, and neither Supabase project pauses — without a successful redirect ever waiting for any of it.

**Architecture:** One new token-guarded endpoint, `GET /health/deep`, checks Postgres and Redis. Better Stack polls it every three minutes, which keeps the production Supabase project awake as a side effect; a daily GitHub Actions workflow keeps the preview project awake and independently pokes production. Sentry captures panics through `sentryhttp` (flushing only on a response that is already 5xx) and every `slog.LevelError` record through a custom `slog.Handler`, with a request-context allowlist that exists in exactly one place.

**Tech Stack:** Go 1.27, chi v5, `github.com/getsentry/sentry-go` + `github.com/getsentry/sentry-go/http`, pgx/v5, `@sentry/tanstackstart-react` (v10+), `@sentry/tanstackstart-react/vite`, TanStack Start, Vite, GitHub Actions, Better Stack.

**Spec:** `docs/superpowers/specs/2026-09-07-observability-design.md`

## Global Constraints

- **Golden rule 2:** the redirect path must never wait on anything optional. A flush is permitted only on a response that is already 5xx.
- **Golden rule 5:** never store a full IP address, ever. This binds Sentry: `event.User.IPAddress` is cleared in Go, `dataCollection.userInfo` is `false` in TypeScript, and "Prevent Storing of IP Addresses" is enabled on both Sentry projects.
- **The request body is always dropped.** `POST /{slug}/verify` carries a link password in cleartext.
- **Header handling is an allowlist, never a denylist.** Only `User-Agent` survives.
- `tracesSampleRate` is `0` and session replay is off. Sentry's free Developer tier allows **5,000 events/month**.
- Only `slog.LevelError` produces a Sentry event. Never `Warn`.
- The whole deep health check shares **one 3-second budget**, not three seconds per dependency.
- `HEALTH_CHECK_TOKEN` unset means `GET /health/deep` answers 404 unconditionally. Fail closed.
- Better Stack polls at **3 minutes**, the free tier's finest interval: 480 requests/day, each with one Redis `PING`, against golden rule 7's ceiling of roughly **16,700 Redis commands/day**.
- `/health` (flat) must not change. `domainverify`'s reachability probe fetches it and requires `"status":"ok"` in the body.
- Commits: Conventional Commits, **max 50 characters including type and scope**, no co-author or generator footer. All git writes go through GitButler (`but`), never `git add`/`git commit`.
- The API and web app share one `Release` value: `VERCEL_GIT_COMMIT_SHA`.

## File Structure

**Created:**

| File | Responsibility |
| --- | --- |
| `apps/api/internal/api/health.go` | The `GET /health/deep` handler, its response shape, and the default dependency pings |
| `apps/api/internal/api/health_test.go` | Token guard, severity rule, and a live-dependency happy path |
| `apps/api/internal/observability/observability.go` | Sentry client options and `Init`; the only file that names a DSN |
| `apps/api/internal/observability/scrub.go` | `BeforeSend`: the request-context allowlist. Pure, so it is testable |
| `apps/api/internal/observability/scrub_test.go` | Proves each field is removed and `User-Agent` survives |
| `apps/api/internal/observability/sloghandler.go` | `slog.Handler` forwarding `LevelError` records to Sentry |
| `apps/api/internal/observability/sloghandler_test.go` | `Error` produces one event, `Warn` produces none |
| `.github/workflows/keep-alive.yml` | Daily keep-alive for the preview database and the production API |
| `apps/web/src/lib/observability.ts` | `sentryOptions()` and `initSentry()` for both bundles; the web-side scrubber |
| `apps/web/src/lib/observability.test.ts` | Cookie and query removal; expected failures report nothing |
| `apps/web/.env.example` | The web app's first documented environment variables |

**Modified:**

| File | Change |
| --- | --- |
| `apps/api/internal/config/config.go` | `HealthCheckToken`, `SentryDSN` |
| `apps/api/internal/api/api.go` | `PingPostgres`, `PingRedis` function fields on `Deps` |
| `apps/api/internal/api/router.go` | Register `/health/deep`; wrap both surfaces in `sentryhttp` |
| `apps/api/cmd/api/main.go` | Initialise Sentry before anything else; install the slog handler |
| `apps/api/.env.example` | `HEALTH_CHECK_TOKEN`, `SENTRY_DSN` |
| `apps/web/src/router.tsx` | Client-side `Sentry.init` behind `!router.isServer` |
| `apps/web/src/routes/__root.tsx` | `errorComponent` that reports only unclassified failures |
| `apps/web/vite.config.ts` | `sentryTanstackStart` as the last plugin, conditional on the auth token |
| `apps/web/package.json` | `@sentry/tanstackstart-react` |
| `CLAUDE.md`, `docs/planning/02-external-services-and-hosting.md` | The amendments the spec's header lists |

---

### Task 0: Maintainer setup (no code)

This task is the human's, and it comes first because tasks 5, 7 and 8 cannot be verified without it. The custom-SMTP episode is the precedent: manual steps buried in prose get skipped, and the resulting failure looks like a code defect.

- [ ] **Step 1: Create the Sentry organisation and two projects**

One project for `apps/api` (platform: Go), one for `apps/web` (platform: TanStack Start / JavaScript). Collect both DSNs.

- [ ] **Step 2: Enable "Prevent Storing of IP Addresses" on both projects**

Settings → Security & Privacy → "Prevent Storing of IP Addresses". This is not optional and not replaceable by an SDK flag: browser events reach Sentry from the visitor's own connection, so Sentry's ingest sees the real address regardless of what the SDK sends.

- [ ] **Step 3: Create a Sentry organization auth token**

Settings → Developer Settings → Organization Tokens → "Create New Token". Its single scope, `org:ci`, is fixed and not selectable: it already covers release creation and source-map upload, which is the whole of what the build needs. A personal token would express the same capability as `project:releases` plus `org:read`, but is bound to one user and reaches every organization that user can see — prefer the organization token. The value is shown once. This is a build-time secret, never a runtime one.

- [ ] **Step 4: Create the Better Stack account and three monitors**

| URL | Interval | Header |
| --- | --- | --- |
| `https://api.kurze-url.app/health/deep` | 3 min | `X-Health-Token: <token>` |
| `https://go.kurze-url.app/health` | 3 min | none |
| `https://kurze-url.app/` | 3 min | none |

Alert channel: email. Invent the `HEALTH_CHECK_TOKEN` value now — 32 random characters — because tasks 1 and 2 both need it.

- [ ] **Step 5: Set the environment variables**

| Variable | Project | Environment |
| --- | --- | --- |
| `HEALTH_CHECK_TOKEN` | `kurze-url-api` | Production, Preview |
| `SENTRY_DSN` | `kurze-url-api` | Production, Preview |
| `VITE_SENTRY_DSN` | `kurze-url-web` | Production, Preview |
| `SENTRY_AUTH_TOKEN` | `kurze-url-web` | Production, Preview |
| `SENTRY_ORG`, `SENTRY_PROJECT` | `kurze-url-web` | Production, Preview |

And two GitHub repository secrets: `HEALTH_CHECK_TOKEN` (new) and `E2E_DATABASE_URL` (already present — do not touch it).

- [ ] **Step 6: Report the DSNs and token back to the implementer**

Nothing is committed in this task, so there is no commit step.

---

### Task 1: `GET /health/deep`

**Files:**
- Create: `apps/api/internal/api/health.go`
- Create: `apps/api/internal/api/health_test.go`
- Modify: `apps/api/internal/config/config.go` (the `Config` struct, and `Load`)
- Modify: `apps/api/internal/api/api.go` (the `Deps` struct)
- Modify: `apps/api/internal/api/router.go:36-39`
- Modify: `apps/api/.env.example`

**Interfaces:**
- Consumes: `Deps.Pool` (`*pgxpool.Pool`), `Deps.Cache` (`*cache.Client`, whose `Raw()` returns `*redis.Client`), `Deps.Log`, `Deps.Config`.
- Produces: `Deps.HandleDeepHealth(w http.ResponseWriter, r *http.Request)`; `config.Config.HealthCheckToken string`; `Deps.PingPostgres`, `Deps.PingRedis` — both `func(ctx context.Context) error`, nil meaning "use the real dependency".

- [ ] **Step 1: Write the failing tests**

Create `apps/api/internal/api/health_test.go`. `newFixture(t)` is defined in `testhelper_test.go` and gives real Postgres and Redis, so the happy path here is a genuine integration test rather than a mock dance.

```go
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

const testHealthToken = "test-health-token"

// deepHealth sends one GET /health/deep. token == "" sends no header at all,
// which is a different case from sending a wrong one.
func deepHealth(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health/deep", nil)
	req.Host = "api.test"
	if token != "" {
		req.Header.Set("X-Health-Token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeChecks(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Checks
}

func TestDeepHealthReportsBothDependencies(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken

	rec := deepHealth(t, api.NewRouter(f.deps), testHealthToken)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, map[string]string{"postgres": "ok", "redis": "ok"}, decodeChecks(t, rec))
}

func TestDeepHealthRefusesWithoutTheToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken

	require.Equal(t, http.StatusNotFound, deepHealth(t, api.NewRouter(f.deps), "").Code)
}

func TestDeepHealthRefusesAWrongToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken

	require.Equal(t, http.StatusNotFound, deepHealth(t, api.NewRouter(f.deps), "wrong").Code)
}

// An unset token disables the endpoint rather than opening it. Without this
// the fail-closed rule is one typo away from publishing dependency status and
// a free database round trip to anyone who guesses the path.
func TestDeepHealthIsDisabledWhenNoTokenIsConfigured(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = ""

	require.Equal(t, http.StatusNotFound, deepHealth(t, api.NewRouter(f.deps), "").Code)
	require.Equal(t, http.StatusNotFound, deepHealth(t, api.NewRouter(f.deps), "anything").Code)
}

func TestDeepHealthAnswers503WhenPostgresIsUnreachable(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken
	f.deps.PingPostgres = func(context.Context) error { return errors.New("no route to host") }

	rec := deepHealth(t, api.NewRouter(f.deps), testHealthToken)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "failed", decodeChecks(t, rec)["postgres"])
}

// 200, deliberately. With Redis down every redirect still works through
// Postgres, so this is degradation, not an outage: Better Stack must not
// page for it, and the error log carries it to Sentry instead. A future
// reader "fixing" this to 503 is exactly what this test exists to stop.
func TestDeepHealthAnswers200WhenOnlyRedisIsUnreachable(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken
	f.deps.PingRedis = func(context.Context) error { return errors.New("connection refused") }

	rec := deepHealth(t, api.NewRouter(f.deps), testHealthToken)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, map[string]string{"postgres": "ok", "redis": "failed"}, decodeChecks(t, rec))
}

// The flat /health must stay flat: domainverify's reachability probe fetches
// it on a claimed hostname and requires "status":"ok" in the body.
func TestFlatHealthNeedsNoToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken

	rec := requestTo(t, api.NewRouter(f.deps), "api.test", "/health")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run TestDeepHealth -v`
Expected: compile failure — `f.deps.PingPostgres` undefined, `Config.HealthCheckToken` undefined.

- [ ] **Step 3: Add the configuration field**

In `apps/api/internal/config/config.go`, inside the `Config` struct next to `DomainDNSTarget`:

```go
	// HealthCheckToken guards GET /health/deep. Empty disables the endpoint
	// outright — it then answers 404 for every caller. Fail closed: a
	// forgotten variable must not publish dependency status and a free
	// database round trip to whoever guesses the path. The cost is that a
	// forgotten variable also breaks the keep-alive, which is not silent —
	// the uptime monitor alerts on the 404.
	HealthCheckToken string
```

And in `Load`, beside the `cfg.DomainDNSTarget` line:

```go
	cfg.HealthCheckToken = os.Getenv("HEALTH_CHECK_TOKEN")
```

- [ ] **Step 4: Add the injectable pings to `Deps`**

In `apps/api/internal/api/api.go`, at the end of the `Deps` struct:

```go
	// PingPostgres and PingRedis back GET /health/deep. Function fields
	// rather than an interface, and nil meaning "use the real dependency",
	// following the same convention as Now above: a test needs to fail one
	// dependency, which is far less work than faking a whole *pgxpool.Pool.
	PingPostgres func(ctx context.Context) error
	PingRedis    func(ctx context.Context) error
```

- [ ] **Step 5: Write the handler**

Create `apps/api/internal/api/health.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// healthCheckBudget bounds the whole deep check rather than each dependency.
// The uptime monitor calling this has its own timeout, and two serial
// three-second waits would outlast it — at which point the monitor reports an
// outage caused by the outage check.
const healthCheckBudget = 3 * time.Second

type deepHealthBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// HandleDeepHealth answers GET /health/deep: the endpoint the uptime monitor
// polls, and — because polling it costs one real Postgres statement — the
// thing that keeps this instance's Supabase project from pausing after seven
// idle days.
//
// It is deliberately not the same endpoint as the flat /health. That one is
// fetched by the platform and by domainverify's reachability probe, neither
// of which should pay for a database round trip.
func (d Deps) HandleDeepHealth(w http.ResponseWriter, r *http.Request) {
	// No token configured disables the endpoint; a wrong one is answered the
	// same way. 404 rather than 401, matching what assertMembership and the
	// entity scopes do everywhere else here: a caller who may not use a
	// route does not learn it exists.
	if d.Config.HealthCheckToken == "" ||
		r.Header.Get("X-Health-Token") != d.Config.HealthCheckToken {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthCheckBudget)
	defer cancel()

	postgresErr := d.pingPostgres(ctx)
	redisErr := d.pingRedis(ctx)

	body := deepHealthBody{
		Status: "ok",
		Checks: map[string]string{
			"postgres": checkStatus(postgresErr),
			"redis":    checkStatus(redisErr),
		},
	}

	status := http.StatusOK
	if postgresErr != nil {
		// An outage: with Postgres unreachable the service cannot serve a
		// cache miss or create a link. The uptime monitor should say so.
		body.Status = "failed"
		status = http.StatusServiceUnavailable
		d.Log.Error("deep health check failed", "dependency", "postgres", "error", postgresErr)
	}
	if redisErr != nil {
		// Still 200. Redirects fall back to Postgres and keep working, so
		// this is degradation — it travels to Sentry through this error log
		// rather than paging the maintainer about a service that is serving.
		d.Log.Error("deep health check failed", "dependency", "redis", "error", redisErr)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func checkStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "ok"
}

func (d Deps) pingPostgres(ctx context.Context) error {
	if d.PingPostgres != nil {
		return d.PingPostgres(ctx)
	}
	// `select 1`, not pool.Ping: Ping sends an empty statement, and whether
	// Supabase's inactivity accounting counts that as a request is an
	// interpretation. Being counted is half of why this endpoint exists.
	var one int
	return d.Pool.QueryRow(ctx, "select 1").Scan(&one)
}

func (d Deps) pingRedis(ctx context.Context) error {
	if d.PingRedis != nil {
		return d.PingRedis(ctx)
	}
	return d.Cache.Raw().Ping(ctx).Err()
}
```

- [ ] **Step 6: Register the route**

In `apps/api/internal/api/router.go`, directly below the existing `root.Get("/health", plainHealth)`:

```go
	// /health/deep answers on every hostname for the same reason the flat one
	// does: a monitor does not know which hostname it is hitting. It is
	// token-guarded and, like /health, stays out of the OpenAPI spec.
	root.Get("/health/deep", deps.HandleDeepHealth)
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'TestDeepHealth|TestFlatHealth' -v`
Expected: all seven PASS. If Postgres or Redis is unavailable locally, `newFixture` skips — start them with `supabase start` rather than accepting a skip as a pass.

- [ ] **Step 8: Falsify the severity rule**

Change the `redisErr != nil` branch to also set `status = http.StatusServiceUnavailable`, rerun, and confirm `TestDeepHealthAnswers200WhenOnlyRedisIsUnreachable` **fails**. Revert. A test that stays green here would leave the whole outage-versus-degradation decision unenforced.

- [ ] **Step 9: Document the variable**

Append to `apps/api/.env.example`:

```bash
# Guards GET /health/deep, the endpoint the uptime monitor polls. Unset
# disables the endpoint entirely (it answers 404), which is deliberate: a
# forgotten value must not expose dependency status. 32 random characters.
HEALTH_CHECK_TOKEN=
```

- [ ] **Step 10: Run the full API suite and commit**

Run: `cd apps/api && go test ./... && go vet ./...`

```bash
but diff
but commit -b feat/observability -m "feat(api): add a deep health endpoint"
```

---

### Task 2: The keep-alive workflow

**Files:**
- Create: `.github/workflows/keep-alive.yml`

**Interfaces:**
- Consumes: `GET /health/deep` from Task 1; repository secrets `E2E_DATABASE_URL` (existing) and `HEALTH_CHECK_TOKEN` (created in Task 0).
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/keep-alive.yml`:

```yaml
# Supabase's free tier pauses a project after seven days without activity.
# Production survives on the Better Stack monitor's three-minute polling of
# /health/deep; the preview project sees no traffic at all between pull
# requests, and would pause exactly when a pull request next needs it.
#
# The production step here is not redundancy for its own sake. Without it,
# production's keep-alive depends entirely on Better Stack continuing to run:
# a paused monitor, a mistyped header or a change to their free tier and the
# project pauses a week later with nothing having said so.
name: keep alive

on:
  schedule:
    # Not on the hour: GitHub queues every scheduled job at :00 and runs the
    # backlog late.
    - cron: '17 4 * * *'
  workflow_dispatch:
  pull_request:
    paths:
      - .github/workflows/keep-alive.yml

concurrency:
  group: keep-alive
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  keep-alive:
    runs-on: ubuntu-latest
    steps:
      - name: Touch the preview database
        env:
          PREVIEW_DATABASE_URL: ${{ secrets.E2E_DATABASE_URL }}
        run: |
          if [ -z "$PREVIEW_DATABASE_URL" ]; then
            echo "E2E_DATABASE_URL is not set" >&2
            exit 1
          fi
          psql "$PREVIEW_DATABASE_URL" --no-psqlrc --quiet -c 'select 1'

      # Skipped on pull requests: /health/deep only reaches production when
      # this branch merges, so running it here would fail for a reason that
      # has nothing to do with the change under review. It proves itself on
      # the first scheduled run, or immediately via workflow_dispatch.
      - name: Touch the production API
        if: github.event_name != 'pull_request'
        env:
          HEALTH_CHECK_TOKEN: ${{ secrets.HEALTH_CHECK_TOKEN }}
        run: |
          if [ -z "$HEALTH_CHECK_TOKEN" ]; then
            echo "HEALTH_CHECK_TOKEN is not set" >&2
            exit 1
          fi
          curl --fail --silent --show-error \
            --header "X-Health-Token: $HEALTH_CHECK_TOKEN" \
            https://api.kurze-url.app/health/deep
```

`--fail` is load-bearing: without it `curl` exits 0 on the 404 a wrong token produces, and the workflow would report success while the endpoint refused it.

- [ ] **Step 2: Check the workflow parses**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/keep-alive.yml'))" && echo parsed`
Expected: `parsed`. GitHub's own validation happens when the branch is pushed; the `pull_request` trigger means the first push runs the preview-database step for real, which is the actual verification.

- [ ] **Step 3: Commit**

```bash
but diff
but commit -b feat/observability -m "ci: keep both supabase projects awake"
```

- [ ] **Step 4: Confirm the run**

After pushing, check that the `keep alive` workflow ran on the pull request and that "Touch the preview database" passed. A failure here means `E2E_DATABASE_URL` is wrong or the preview project is already paused — restore it in the Supabase dashboard and rerun.

---

### Task 3: The request-context allowlist

**Files:**
- Create: `apps/api/internal/observability/scrub.go`
- Create: `apps/api/internal/observability/scrub_test.go`
- Modify: `apps/api/go.mod` (adds `github.com/getsentry/sentry-go`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `observability.Scrub(event *sentry.Event, hint *sentry.EventHint) *sentry.Event` — the value assigned to `sentry.ClientOptions.BeforeSend` in Task 5.

- [ ] **Step 1: Add the dependency**

Run: `cd apps/api && go get github.com/getsentry/sentry-go@latest && go get github.com/getsentry/sentry-go/http@latest`

- [ ] **Step 2: Write the failing test**

Create `apps/api/internal/observability/scrub_test.go`:

```go
package observability_test

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// eventWithEverything is one event carrying every category of request data
// this project must not send: a forwarded client address in three shapes, a
// session cookie, a cleartext password in the body, and a query string.
func eventWithEverything() *sentry.Event {
	event := sentry.NewEvent()
	event.User.IPAddress = "203.0.113.7"
	event.Request = &sentry.Request{
		URL:         "https://go.kurze-url.app/abcd1234/verify?token=secret",
		Method:      "POST",
		Data:        "password=hunter2",
		QueryString: "token=secret",
		Cookies:     "sb-access-token=eyJhbGci",
		Headers: map[string]string{
			"User-Agent":               "Mozilla/5.0",
			"X-Forwarded-For":          "203.0.113.7",
			"X-Vercel-Forwarded-For":   "203.0.113.7",
			"X-Real-Ip":                "203.0.113.7",
			"Cookie":                   "sb-access-token=eyJhbGci",
			"Authorization":            "Bearer eyJhbGci",
		},
		Env: map[string]string{"REMOTE_ADDR": "203.0.113.7"},
	}
	return event
}

// The link password is the sharpest case in this codebase: POST
// /{slug}/verify posts it in cleartext, so a panic there would ship it to a
// third party.
func TestScrubDiscardsTheRequestBody(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.Request.Data)
}

func TestScrubKeepsOnlyTheUserAgentHeader(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Equal(t, map[string]string{"User-Agent": "Mozilla/5.0"}, got.Request.Headers)
}

func TestScrubRemovesEveryTraceOfTheClientAddress(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.User.IPAddress)
	require.Empty(t, got.Request.Env)
}

func TestScrubRemovesCookiesAndQuery(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.Request.Cookies)
	require.Empty(t, got.Request.QueryString)
	require.Equal(t, "https://go.kurze-url.app/abcd1234/verify", got.Request.URL)
}

// Header names arrive lowercased over HTTP/2, so an allowlist keyed on the
// canonical spelling has to fold case or it silently drops everything.
func TestScrubMatchesHeadersCaseInsensitively(t *testing.T) {
	event := sentry.NewEvent()
	event.Request = &sentry.Request{Headers: map[string]string{"user-agent": "curl/8.0"}}

	got := observability.Scrub(event, nil)

	require.Equal(t, map[string]string{"User-Agent": "curl/8.0"}, got.Request.Headers)
}

// A panic captured outside an HTTP request has no Request at all.
func TestScrubToleratesAnEventWithoutARequest(t *testing.T) {
	event := sentry.NewEvent()
	event.User.IPAddress = "203.0.113.7"

	got := observability.Scrub(event, nil)

	require.Empty(t, got.User.IPAddress)
	require.Nil(t, got.Request)
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/observability/ -v`
Expected: build failure — no such package. If a field name in `sentry.Request` differs in the version `go get` resolved, fix the **implementation** to match the SDK; never weaken the test to match a guess.

- [ ] **Step 4: Write the scrubber**

Create `apps/api/internal/observability/scrub.go`:

```go
// Package observability holds everything this API sends to Sentry: the client
// options, the request-context scrubber, and the slog handler that turns an
// error log into an event. No other package imports sentry-go, so the policy
// about what may leave this process has exactly one home.
package observability

import (
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
)

// allowedHeaders is the complete set of request headers permitted to leave
// this process inside a Sentry event.
//
// An allowlist rather than a denylist, and that is the point: the hazards are
// X-Forwarded-For, X-Vercel-Forwarded-For, X-Real-Ip, Cookie and
// Authorization, and a denylist only protects against the ones somebody
// remembered to write down. A new proxy header added by the platform next
// year is excluded here by construction.
//
// User-Agent earns its place because redirect defects are routinely
// device-specific, and a User-Agent is not an IP address.
var allowedHeaders = map[string]struct{}{
	"User-Agent": {},
}

// Scrub is the value of sentry.ClientOptions.BeforeSend. It is an ordinary
// function of an event rather than middleware so that it can be tested
// directly — which is the whole reason the policy lives here.
func Scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	// Golden rule 5: never store a full IP address, ever.
	event.User.IPAddress = ""

	if event.Request == nil {
		return event
	}

	// Mandatory rather than cautious. POST /{slug}/verify carries a link's
	// password in cleartext, so without this line the first panic on the
	// password interstitial ships that password to a third party — the same
	// class of mistake audit.go's checkMetadata exists to prevent on the
	// audit path.
	event.Request.Data = ""

	event.Request.Cookies = ""
	event.Request.QueryString = ""
	// Env carries REMOTE_ADDR under sentry-go's HTTP integration.
	event.Request.Env = nil
	event.Request.URL = withoutQuery(event.Request.URL)

	headers := make(map[string]string, len(allowedHeaders))
	for name, value := range event.Request.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, ok := allowedHeaders[canonical]; ok {
			headers[canonical] = value
		}
	}
	event.Request.Headers = headers

	return event
}

func withoutQuery(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/observability/ -v`
Expected: all six PASS.

- [ ] **Step 6: Falsify both protections**

Two separate checks, each parameter-preserving — change the condition's value rather than deleting the code, so the test is proved to depend on the behaviour and not merely on the code compiling:

1. Change the allowlist lookup to `if _, ok := allowedHeaders[canonical]; ok || true {`. Rerun: `TestScrubKeepsOnlyTheUserAgentHeader` must **fail**. Revert.
2. Change `event.Request.Data = ""` to `event.Request.Data = event.Request.Data`. Rerun: `TestScrubDiscardsTheRequestBody` must **fail**. Revert.

If either stays green the test is worthless — fix the test before moving on.

- [ ] **Step 7: Commit**

```bash
but diff
but commit -b feat/observability -m "feat(api): scrub request context for sentry"
```

---

### Task 4: Error logs become Sentry events

**Files:**
- Create: `apps/api/internal/observability/sloghandler.go`
- Create: `apps/api/internal/observability/sloghandler_test.go`

**Interfaces:**
- Consumes: the `observability` package from Task 3.
- Produces: `observability.NewSlogHandler(inner slog.Handler) slog.Handler`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/observability/sloghandler_test.go`:

```go
package observability_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// fakeTransport collects events instead of sending them. If the pinned
// sentry-go version's Transport interface requires more methods than these,
// add them — the compiler names them precisely.
type fakeTransport struct{ events []*sentry.Event }

func (t *fakeTransport) Configure(sentry.ClientOptions)      {}
func (t *fakeTransport) SendEvent(event *sentry.Event)       { t.events = append(t.events, event) }
func (t *fakeTransport) Flush(time.Duration) bool            { return true }

// loggerWithFakeSentry returns a logger whose error records reach transport,
// via a hub carried on the returned context. A hub on the context rather
// than the global one keeps these tests independent of each other.
func loggerWithFakeSentry(t *testing.T) (*slog.Logger, context.Context, *fakeTransport) {
	t.Helper()

	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)

	hub := sentry.NewHub(client, sentry.NewScope())
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	// io.Discard: this test is about what reaches Sentry, not about the JSON
	// the inner handler writes.
	logger := slog.New(observability.NewSlogHandler(slog.NewJSONHandler(io.Discard, nil)))

	return logger, ctx, transport
}

func TestErrorLogsBecomeSentryEvents(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.ErrorContext(ctx, "redis lookup failed", "error", errors.New("connection refused"))

	require.Len(t, transport.events, 1)
	require.Contains(t, transport.events[0].Message+eventException(transport.events[0]),
		"redis lookup failed")
}

// Warn is deliberately not reported. The free tier allows 5,000 events a
// month, and this codebase warns about ordinary, expected conditions —
// an unset SUPABASE_JWKS_URL at startup, for one.
func TestWarnLogsProduceNoEvent(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.WarnContext(ctx, "supabase auth url is unset")

	require.Empty(t, transport.events)
}

func TestInfoLogsProduceNoEvent(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.InfoContext(ctx, "api listening", "port", "8080")

	require.Empty(t, transport.events)
}

// The wrapped handler must still be a working logger: swallowing records
// would trade one blind spot for another.
func TestTheInnerHandlerStillReceivesEveryRecord(t *testing.T) {
	var written []string
	logger := slog.New(observability.NewSlogHandler(&recordingHandler{lines: &written}))

	logger.Info("kept")
	logger.Error("also kept")

	require.Equal(t, []string{"kept", "also kept"}, written)
}

type recordingHandler struct{ lines *[]string }

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r.Message)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// eventException flattens an event's exception values so one assertion can
// cover both the CaptureMessage and CaptureException shapes.
func eventException(event *sentry.Event) string {
	out := ""
	for _, ex := range event.Exception {
		out += ex.Value
	}
	return out
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/observability/ -run 'Logs|InnerHandler' -v`
Expected: `undefined: observability.NewSlogHandler`.

- [ ] **Step 3: Write the handler**

Create `apps/api/internal/observability/sloghandler.go`:

```go
package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/getsentry/sentry-go"
)

// slogHandler forwards error-level records to Sentry and passes every record
// on to the handler it wraps.
//
// A handler rather than CaptureException calls at each site: this codebase
// already logs its failures consistently, so every existing Log.Error becomes
// an event with no new call site — and, more importantly, with no second
// list of "places that report" to drift out of step with the first.
type slogHandler struct{ inner slog.Handler }

// NewSlogHandler wraps inner so that records at slog.LevelError also reach
// Sentry. Capturing is a non-blocking enqueue onto the transport's queue, so
// this is safe on the redirect hot path: nothing here waits for delivery.
// The events still in that queue when an instance retires are lost, which is
// the deliberate price of not charging every successful redirect for the
// possibility of an error.
func NewSlogHandler(inner slog.Handler) slog.Handler {
	return &slogHandler{inner: inner}
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	// Only LevelError. Warn is used here for expected conditions — an unset
	// SUPABASE_JWKS_URL, disabled invitations — and reporting those would
	// spend the monthly event budget on configuration notes.
	if record.Level >= slog.LevelError {
		hub := sentry.GetHubFromContext(ctx)
		if hub == nil {
			hub = sentry.CurrentHub()
		}

		if err := errorAttr(record); err != nil {
			// Wrapped rather than reported separately so the log message,
			// which is what a human recognises, participates in Sentry's
			// grouping alongside the underlying error.
			hub.CaptureException(fmt.Errorf("%s: %w", record.Message, err))
		} else {
			hub.CaptureMessage(record.Message)
		}
	}

	return h.inner.Handle(ctx, record)
}

// WithAttrs and WithGroup delegate. The attributes reach the inner handler's
// output; the Sentry side reads only the record, which keeps grouping stable
// rather than letting a request-scoped attribute split one failure into many
// issues.
func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	return &slogHandler{inner: h.inner.WithGroup(name)}
}

// errorAttr finds the conventional "error" attribute this codebase logs its
// failures under, so Sentry gets an exception with a stack trace rather than
// a bare message.
func errorAttr(record slog.Record) error {
	var found error

	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != "error" {
			return true
		}
		if err, ok := attr.Value.Any().(error); ok {
			found = err
			return false
		}
		found = errors.New(attr.Value.String())
		return false
	})

	return found
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/observability/ -v`
Expected: all PASS.

- [ ] **Step 5: Falsify the level threshold**

Change the condition to `if record.Level >= slog.LevelWarn`. Rerun: `TestWarnLogsProduceNoEvent` must **fail**. Revert.

- [ ] **Step 6: Commit**

```bash
but diff
but commit -b feat/observability -m "feat(api): report error logs to sentry"
```

---

### Task 5: Wire Sentry into the API

**Files:**
- Create: `apps/api/internal/observability/observability.go`
- Create: `apps/api/internal/observability/middleware_test.go`
- Modify: `apps/api/internal/config/config.go`
- Modify: `apps/api/cmd/api/main.go`
- Modify: `apps/api/internal/api/router.go`
- Modify: `apps/api/.env.example`

**Interfaces:**
- Consumes: `observability.Scrub` (Task 3), `observability.NewSlogHandler` (Task 4), and — in the test — the `fakeTransport` type defined in Task 4's `sloghandler_test.go`. Both test files are in package `observability_test`, so it is shared, not redeclared; declaring it twice is a compile error.
- Produces: `observability.Init(dsn, environment, release string) (flush func(), err error)`; `observability.Middleware() func(http.Handler) http.Handler`; `config.Config.SentryDSN`, `.Environment`, `.Release`.

- [ ] **Step 1: Write the failing test for the middleware order**

Create `apps/api/internal/observability/middleware_test.go`. This is the test that pins the claim in the spec — that a panic is both captured and still handled by the outer recoverer:

```go
package observability_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

func TestMiddlewareCapturesAPanicAndRepanics(t *testing.T) {
	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	handler := observability.Middleware()(panicking)

	// Repanic: true is what lets chi's Recoverer stay outermost and keep
	// logging the stack trace. Without it this middleware would answer 500
	// itself and the stack trace would never reach the logs.
	require.Panics(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
	require.Len(t, transport.events, 1)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/observability/ -run TestMiddleware -v`
Expected: `undefined: observability.Middleware`.

- [ ] **Step 3: Write the init and the middleware**

Create `apps/api/internal/observability/observability.go`:

```go
package observability

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// deliveryTimeout bounds the two places this process ever waits for Sentry:
// the flush at shutdown, and the flush after a panic. Both are already
// failure paths, which is the only reason waiting is permitted at all —
// golden rule 2 forbids it on the redirect path.
const deliveryTimeout = 2 * time.Second

// Init configures the Sentry client and returns the flush to defer at
// shutdown. An empty dsn returns a no-op and configures nothing, so a local
// checkout needs no Sentry account.
//
// No tracing option is set. Tracing is off by default, and this project's
// problem is error visibility, not latency — performance data would spend
// the same 5,000 events a month that errors need.
func Init(dsn, environment, release string) (func(), error) {
	if dsn == "" {
		return func() {}, nil
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Environment: environment,
		Release:     release,
		BeforeSend:  Scrub,
	}); err != nil {
		return func() {}, err
	}

	return func() { sentry.Flush(deliveryTimeout) }, nil
}

// Middleware captures panics. It belongs INSIDE chi's middleware.Recoverer,
// with Repanic set: the two other arrangements each lose something concrete.
// Recoverer innermost swallows the panic and Sentry sees nothing; this
// middleware outermost without Repanic answers 500 itself and Recoverer
// never logs the stack trace.
//
// WaitForDelivery is the only flush on a request path in this codebase, and
// it is reached exclusively by a panic — a response that is already 5xx. A
// successful redirect never enters this code.
func Middleware() func(http.Handler) http.Handler {
	return sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: true,
		Timeout:         deliveryTimeout,
	}).Handle
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd apps/api && go test ./internal/observability/ -v`
Expected: all PASS.

- [ ] **Step 5: Falsify the repanic**

Change `Repanic: true` to `Repanic: false`. Rerun: `TestMiddlewareCapturesAPanicAndRepanics` must **fail** on `require.Panics`. Revert.

- [ ] **Step 6: Add the configuration fields**

In `apps/api/internal/config/config.go`, in the `Config` struct beside `HealthCheckToken`:

```go
	// SentryDSN empty disables error reporting entirely. Errors are still
	// logged; they just do not outlive Vercel's log retention.
	SentryDSN string

	// Environment and Release tag every Sentry event. Release is the commit
	// sha, and apps/web sends the same value, so one bad deployment is
	// correlatable across both projects.
	Environment string
	Release     string
```

And in `Load`, beside `cfg.HealthCheckToken`:

```go
	cfg.SentryDSN = os.Getenv("SENTRY_DSN")
	cfg.Environment = env("VERCEL_ENV", "development")
	cfg.Release = os.Getenv("VERCEL_GIT_COMMIT_SHA")
```

- [ ] **Step 7: Initialise Sentry in main**

In `apps/api/cmd/api/main.go`, add the import `"github.com/mheob/kurze-url/apps/api/internal/observability"` and insert immediately after the `cfg, err := config.Load()` block:

```go
	// As early as the DSN is known, which is after config.Load — the
	// last-gasp log in main() therefore stays unreported. That is accepted:
	// wrapping it would mean initialising Sentry before the configuration
	// that carries the DSN.
	flushSentry, err := observability.Init(cfg.SentryDSN, cfg.Environment, cfg.Release)
	if err != nil {
		return err
	}
	defer flushSentry()

	if cfg.SentryDSN == "" {
		log.Warn("SENTRY_DSN is unset — errors are logged but not reported")
	} else {
		// Every existing Log.Error call site becomes a Sentry event from
		// here on, including the two in HandleDeepHealth.
		log = slog.New(observability.NewSlogHandler(log.Handler()))
	}
```

- [ ] **Step 8: Wrap both surfaces**

In `apps/api/internal/api/router.go`, after each existing `Use(middleware.Recoverer)` line — both the API surface and the redirect surface — add:

```go
	// Inside Recoverer, never outside: see observability.Middleware's own
	// comment for what each wrong order costs.
	apiSurface.Use(observability.Middleware())
```

and the redirect surface's equivalent:

```go
	redirectSurface.Use(observability.Middleware())
```

Add the import for `observability` to `router.go`.

- [ ] **Step 9: Document the variable**

Append to `apps/api/.env.example`:

```bash
# Sentry error reporting. Empty disables it entirely — errors are still
# logged, they just do not outlive Vercel's one-day log retention. The free
# Developer tier allows 5,000 events per month, which is why only
# slog.LevelError records are reported and tracing stays off.
SENTRY_DSN=
```

- [ ] **Step 10: Run everything and commit**

Run: `cd apps/api && go test ./... && go vet ./...`

```bash
but diff
but commit -b feat/observability -m "feat(api): initialise sentry reporting"
```

---

### Task 6: The web-side scrubber and options

**Files:**
- Create: `apps/web/src/lib/observability.ts`
- Create: `apps/web/src/lib/observability.test.ts`
- Create: `apps/web/.env.example`
- Modify: `apps/web/package.json`
- Modify: `apps/web/vite.config.ts` (the `define` block only — Task 8 touches the same file's `plugins`)

**Interfaces:**
- Consumes: `classifyApiError` from `apps/web/src/lib/api-errors.ts`.
- Produces: `sentryOptions(dsn: string): Parameters<typeof Sentry.init>[0]`; `scrubEvent(event: Sentry.ErrorEvent): Sentry.ErrorEvent`; `isReportable(error: unknown): boolean`; `reportUnexpected(error: unknown): void`.

- [ ] **Step 1: Install the SDK and check its major version**

Run: `pnpm --filter @kurze-url/web add @sentry/tanstackstart-react`

Then run: `node -p "require('./apps/web/node_modules/@sentry/tanstackstart-react/package.json').version"`

**If the major version is below 10, stop and report it.** This task's options block uses `dataCollection`, which replaced the now-deprecated `sendDefaultPii` in v10. On v9 the equivalent is `sendDefaultPii: false`, and the plan needs amending rather than guessing.

- [ ] **Step 2: Write the failing tests**

Create `apps/web/src/lib/observability.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { isReportable, scrubEvent } from './observability';

/** Every category of request data this project must not send. */
function eventWithEverything() {
	return {
		breadcrumbs: [
			{ category: 'console', message: 'user typed a password' },
			{ category: 'fetch', message: 'GET /v1/me' },
		],
		request: {
			cookies: { 'sb-access-token': 'eyJhbGci' },
			data: 'password=hunter2',
			headers: {
				authorization: 'Bearer eyJhbGci',
				cookie: 'sb-access-token=eyJhbGci',
				'user-agent': 'Mozilla/5.0',
				'x-forwarded-for': '203.0.113.7',
			},
			query_string: 'token=secret',
			url: 'https://kurze-url.app/teams/sv-gruenwald/links?token=secret',
		},
		user: { ip_address: '203.0.113.7' },
	} as never;
}

describe('scrubEvent', () => {
	it('removes the client address, cookies, body and query', () => {
		const got = scrubEvent(eventWithEverything()) as never as Record<string, never>;
		const request = got.request as unknown as Record<string, unknown>;

		expect((got.user as unknown as Record<string, unknown>).ip_address).toBeUndefined();
		expect(request.cookies).toBeUndefined();
		expect(request.data).toBeUndefined();
		expect(request.query_string).toBeUndefined();
		expect(request.url).toBe('https://kurze-url.app/teams/sv-gruenwald/links');
	});

	it('keeps only the user-agent header', () => {
		const got = scrubEvent(eventWithEverything()) as never as Record<string, never>;
		const request = got.request as unknown as Record<string, unknown>;

		expect(request.headers).toEqual({ 'user-agent': 'Mozilla/5.0' });
	});

	/**
	 * Console breadcrumbs capture whatever any code logged, which on this app
	 * includes values a person typed. Dropped here rather than by disabling
	 * an integration, so the guarantee does not depend on an integration
	 * name staying stable across SDK majors.
	 */
	it('drops console breadcrumbs and keeps the rest', () => {
		const got = scrubEvent(eventWithEverything()) as never as Record<string, never>;

		expect(got.breadcrumbs).toEqual([{ category: 'fetch', message: 'GET /v1/me' }]);
	});
});

describe('isReportable', () => {
	/**
	 * The quota trap. This app renders API failures as UI on purpose —
	 * classifyApiError turns 403, 422 and field errors into copy in two
	 * languages. One Verein mistyping a hostname repeatedly would otherwise
	 * spend the monthly event budget on events carrying nothing the person
	 * was not already shown.
	 */
	it.each([
		['unauthenticated', 401],
		['not found', 404],
		['rate limited', 429],
		['field errors', 422],
	])('does not report an expected %s failure', (_label, status) => {
		expect(isReportable({ errors: [{ location: 'body.hostname', message: 'bad' }], status })).toBe(
			false,
		);
	});

	it('reports a server failure', () => {
		expect(isReportable({ status: 500 })).toBe(true);
	});

	it('reports something that is not an API failure at all', () => {
		expect(isReportable(new TypeError('cannot read properties of undefined'))).toBe(true);
	});
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `pnpm --filter @kurze-url/web test src/lib/observability.test.ts`
Expected: FAIL — cannot resolve `./observability`.

- [ ] **Step 4: Write the module**

Create `apps/web/src/lib/observability.ts`:

```ts
import * as Sentry from '@sentry/tanstackstart-react';

import { classifyApiError } from './api-errors';

/** The complete set of request headers permitted to leave the browser. */
const ALLOWED_HEADERS = new Set(['user-agent']);

/**
 * `beforeSend`, and the thing that actually enforces this project's rule
 * about what may leave a visitor's browser. `dataCollection` below reduces
 * what is collected; this guarantees what is sent.
 *
 * Golden rule 5 — never store a full IP address, ever — has a second half
 * that no code can cover: browser events reach Sentry over the visitor's own
 * connection, so Sentry's ingest sees the address regardless. The project
 * setting "Prevent Storing of IP Addresses" is the other switch, and both
 * are required.
 */
export function scrubEvent(event: Sentry.ErrorEvent): Sentry.ErrorEvent {
	const scrubbed = event as unknown as {
		breadcrumbs?: { category?: string }[];
		request?: {
			cookies?: unknown;
			data?: unknown;
			headers?: Record<string, string>;
			query_string?: unknown;
			url?: string;
		};
		user?: { ip_address?: string };
	};

	if (scrubbed.user) delete scrubbed.user.ip_address;

	if (scrubbed.breadcrumbs) {
		// Console breadcrumbs carry whatever any code logged. Filtered here
		// rather than by disabling the breadcrumbs integration, so the
		// guarantee survives an SDK major renaming that integration.
		scrubbed.breadcrumbs = scrubbed.breadcrumbs.filter((crumb) => crumb.category !== 'console');
	}

	const { request } = scrubbed;
	if (request) {
		delete request.cookies;
		delete request.data;
		delete request.query_string;
		if (request.url) request.url = request.url.split('?')[0];
		if (request.headers) {
			request.headers = Object.fromEntries(
				Object.entries(request.headers).filter(([name]) =>
					ALLOWED_HEADERS.has(name.toLowerCase()),
				),
			);
		}
	}

	return event;
}

/**
 * `classifyApiError` names every failure this app deliberately renders as
 * UI. `unknown` is what is left: a 500, a network failure, a render error —
 * the things nobody chose to handle, and the only things worth an event.
 */
export function isReportable(error: unknown): boolean {
	return classifyApiError(error).kind === 'unknown';
}

/**
 * Errors already reported. A router error component can render more than
 * once for one error, and on the server it renders again on the client after
 * hydration — without this, one failure becomes several events out of the
 * monthly 5,000.
 */
const reported = new WeakSet<object>();

export function reportUnexpected(error: unknown): void {
	if (!isReportable(error)) return;

	if (typeof error === 'object' && error !== null) {
		if (reported.has(error)) return;
		reported.add(error);
	}

	Sentry.captureException(error);
}

/**
 * `dataCollection` is the v10 replacement for the deprecated
 * `sendDefaultPii: false`, in the conservative shape Sentry's own options
 * documentation gives for preserving that behaviour. It is defence in depth
 * next to `scrubEvent`, not a substitute for it.
 *
 * No tracing and no replay: `tracesSampleRate` stays unset, and replay would
 * be PII capture by design.
 */
export function sentryOptions(dsn: string): Parameters<typeof Sentry.init>[0] {
	return {
		beforeSend: scrubEvent,
		dataCollection: {
			cookies: { deny: ['forwarded', '-ip', 'remote-', 'via', '-user'] },
			genAI: { inputs: false, outputs: false },
			httpBodies: [],
			httpHeaders: { deny: ['forwarded', '-ip', 'remote-', 'via', '-user'] },
			urlQueryParams: { deny: ['forwarded', '-ip', 'remote-', 'via', '-user'] },
			userInfo: false,
		},
		dsn,
		// Vercel's own VERCEL_ENV and VERCEL_GIT_COMMIT_SHA exist only at
		// build time and carry no VITE_ prefix, so the browser bundle cannot
		// see them. Step 6 defines these two from them instead of asking the
		// maintainer to duplicate two more variables in the dashboard.
		environment: import.meta.env.VITE_SENTRY_ENVIRONMENT || 'development',
		release: import.meta.env.VITE_SENTRY_RELEASE || undefined,
	};
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `pnpm --filter @kurze-url/web test src/lib/observability.test.ts`
Expected: all PASS.

- [ ] **Step 6: Define the environment and release at build time**

Add to the config object in `apps/web/vite.config.ts`, beside `resolve`:

```ts
	// Vercel sets VERCEL_ENV and VERCEL_GIT_COMMIT_SHA on the build, without
	// the VITE_ prefix Vite needs to expose a value to the browser bundle.
	// Defining them here keeps the release identical to the API's — one bad
	// deployment stays correlatable across both Sentry projects — without
	// two more variables to set by hand and keep in step.
	define: {
		'import.meta.env.VITE_SENTRY_ENVIRONMENT': JSON.stringify(
			process.env.VERCEL_ENV ?? 'development',
		),
		'import.meta.env.VITE_SENTRY_RELEASE': JSON.stringify(
			process.env.VERCEL_GIT_COMMIT_SHA ?? '',
		),
	},
```

- [ ] **Step 7: Document the web variables**

`apps/web` has no `.env.example` — every value it reads so far goes through a
server function and `process.env`. `VITE_SENTRY_DSN` is the first value this
app needs in the *browser*, which is why it carries the prefix: the SDK has to
initialise before the errors it is meant to catch, so fetching the DSN from
the server is not an option. A public DSN is public by design.

Create `apps/web/.env.example`:

```bash
# Sentry, browser and server. Empty disables reporting entirely. Public by
# design — this value ships inside the client bundle.
VITE_SENTRY_DSN=

# Build-time only, for the source-map upload. Absent skips the upload
# without failing the build, which is what keeps local and fork builds
# working.
SENTRY_AUTH_TOKEN=
SENTRY_ORG=
SENTRY_PROJECT=
```

- [ ] **Step 8: Falsify both protections**

1. Change the header filter's predicate to `.filter(([name]) => ALLOWED_HEADERS.has(name.toLowerCase()) || true)`. Rerun: the user-agent test must **fail**. Revert.
2. Change `isReportable` to `return classifyApiError(error).kind !== 'never-a-kind'`. Rerun: every expected-failure case must **fail**. Revert.

- [ ] **Step 9: Typecheck, lint and commit**

Run: `pnpm typecheck && pnpm lint`

```bash
but diff
but commit -b feat/observability -m "feat(web): scrub and classify sentry events"
```

---

### Task 7: Initialise Sentry in the web app

**Files:**
- Modify: `apps/web/src/lib/observability.ts` (adds `initSentry`)
- Modify: `apps/web/src/router.tsx`
- Modify: `apps/web/src/routes/__root.tsx`
- Create: `apps/web/src/routes/__root.test.tsx`

**Interfaces:**
- Consumes: `sentryOptions`, `reportUnexpected` (Task 6).
- Produces: `initSentry(isServer: boolean): void`; `RootErrorPage` rendered as the root route's `errorComponent`.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/routes/__root.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ reportUnexpected: vi.fn() }));

vi.mock('../lib/observability', async (importOriginal) => ({
	...(await importOriginal<typeof import('../lib/observability')>()),
	reportUnexpected: mocks.reportUnexpected,
}));

const { RootErrorPage } = await import('./__root');

describe('RootErrorPage', () => {
	it('reports the failure it renders', () => {
		const error = new Error('boom');

		render(<RootErrorPage error={error} />);

		expect(mocks.reportUnexpected).toHaveBeenCalledWith(error);
	});

	/**
	 * Reported and also *shown*. An error component that reports silently
	 * and renders nothing leaves the visitor on a blank page, which is how
	 * the "Something went wrong" episode looked from the outside.
	 */
	it('tells the visitor something went wrong', () => {
		render(<RootErrorPage error={new Error('boom')} />);

		expect(screen.getByRole('alert')).toBeInTheDocument();
	});
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test src/routes/__root.test.tsx`
Expected: FAIL — `RootErrorPage` is not exported.

- [ ] **Step 3: Add `initSentry`**

Append to `apps/web/src/lib/observability.ts`:

```ts
/**
 * `Sentry.init` is process-global, and `getRouter` runs once per request on
 * the server — so this guards against re-initialising the client on every
 * page view.
 */
let initialized = false;

/**
 * Called from `getRouter`, which is the one place that exists in both
 * bundles. Sentry's own documentation prefers an `instrument.server.mjs`
 * loaded with node's `--import`, which this deployment cannot arrange: the
 * server bundle is built by Nitro and run by Vercel, and neither exposes the
 * node command line. With tracing off, plain `Sentry.init` is enough for
 * error capture, which is all this project asked for. If auto-instrumentation
 * is ever wanted, that constraint is what has to be solved first.
 */
export function initSentry(isServer: boolean): void {
	if (initialized) return;

	const dsn = import.meta.env.VITE_SENTRY_DSN;
	if (!dsn) return;

	initialized = true;
	Sentry.init({ ...sentryOptions(dsn), serverName: isServer ? 'web-ssr' : undefined });
}
```

- [ ] **Step 4: Call it from the router**

In `apps/web/src/router.tsx`, add the import and one line after `setupRouterSsrQueryIntegration`:

```ts
	setupRouterSsrQueryIntegration({ queryClient, router });

	// After the router exists, because the server/client distinction comes
	// from it. Both bundles reach this line; only the one with a DSN acts.
	initSentry(router.isServer);
```

- [ ] **Step 5: Add the error component**

In `apps/web/src/routes/__root.tsx`, add the import `import { reportUnexpected } from '../lib/observability';` and this component next to `NotFound`:

```tsx
/**
 * The one place every unhandled failure in the authenticated tree arrives,
 * which is why reporting happens here rather than in each route's own
 * `errorComponent`. Everything `classifyApiError` names — a 403, a 422, a
 * field error — is rendered as ordinary UI by the route that caused it and
 * never reaches this component; `reportUnexpected` refuses those anyway, so
 * the two guards agree.
 *
 * Reported during render rather than in an effect: this component also
 * renders on the server, where effects never run.
 */
export function RootErrorPage({ error }: { readonly error: Error }) {
	const { t } = useTranslation();

	reportUnexpected(error);

	return (
		<main className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
			<h1 className="text-3xl font-bold">{t('errors.unknown')}</h1>
		</main>
	);
}
```

Give the heading the alert role the test asserts by wrapping it:

```tsx
			<h1 className="text-3xl font-bold" role="alert">
				{t('errors.unknown')}
			</h1>
```

And register it on the root route, beside `notFoundComponent`:

```tsx
	errorComponent: RootErrorPage,
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `pnpm --filter @kurze-url/web test src/routes/__root.test.tsx`
Expected: both PASS.

- [ ] **Step 7: Run the whole web suite, then commit**

Run: `pnpm --filter @kurze-url/web test && pnpm typecheck && pnpm lint`

```bash
but diff
but commit -b feat/observability -m "feat(web): report unhandled errors"
```

---

### Task 8: Source maps

**Files:**
- Modify: `apps/web/vite.config.ts` (the `plugins` array — Task 6 already added this file's `define` block)

**Interfaces:**
- Consumes: the `@sentry/tanstackstart-react` dependency from Task 6.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Add the plugin, conditionally and last**

In `apps/web/vite.config.ts`, add the import and build the plugin list so Sentry's plugin is the final entry:

```ts
import { sentryTanstackStart } from '@sentry/tanstackstart-react/vite';
```

```ts
/**
 * Only with a token, so a local build and a fork's build still work — the
 * plugin is not registered at all without it rather than registered and
 * failing.
 *
 * `filesToDeleteAfterUpload` is not optional tidying. Uploading the maps
 * makes stack traces readable in Sentry; leaving them in the deployed output
 * publishes this app's source next to its bundle, which is worse than the
 * minified traces the upload was meant to fix.
 */
const sentryPlugins = process.env.SENTRY_AUTH_TOKEN
	? [
			sentryTanstackStart({
				authToken: process.env.SENTRY_AUTH_TOKEN,
				org: process.env.SENTRY_ORG,
				project: process.env.SENTRY_PROJECT,
				sourcemaps: {
					filesToDeleteAfterUpload: ['./dist/**/*.map', './.vercel/output/**/*.map'],
				},
			}),
		]
	: [];
```

Then, in the config, append it after `viteReact()` — `sentryTanstackStart` must be the last plugin:

```ts
	plugins: [
		devtools(),
		tailwindcss(),
		tanstackStart(),
		nitroV2Plugin({ compatibilityDate: '2026-09-04' }),
		viteReact(),
		// Last, as Sentry's own setup guide requires.
		...sentryPlugins,
	],
```

- [ ] **Step 2: Verify a build without the token still works**

Run: `cd apps/web && SENTRY_AUTH_TOKEN= pnpm build`
Expected: the build succeeds and mentions no Sentry upload. This is the case that matters locally and for forks.

- [ ] **Step 3: Confirm the plugin is registered last**

Run: `grep -n -A 12 'plugins: \[' apps/web/vite.config.ts`
Expected: `...sentryPlugins` is the final entry. Sentry's setup guide requires it, and a plugin ordered before `nitroV2Plugin` sees the pre-Nitro output rather than what is actually deployed.

A local no-token build deletes nothing, because it uploads nothing — so the absence of `.map` files cannot be checked here. Step 5 checks it where it is real.

- [ ] **Step 4: Typecheck and commit**

Run: `pnpm typecheck && pnpm lint`

```bash
but diff
but commit -b feat/observability -m "build(web): upload sentry source maps"
```

- [ ] **Step 5: Verify the upload on the preview deployment**

After pushing, in the Vercel build log for the web preview: confirm a Sentry upload step ran and reported uploaded artifacts. Then request a bundle's `.map` URL from the preview and confirm it is **not** served. Both halves matter — an upload without the deletion is a source-code leak, and a deletion without the upload leaves Sentry useless.

---

### Task 9: Documentation

**Files:**
- Modify: `CLAUDE.md:56`, `CLAUDE.md:102`, and the "Non-obvious constraints" and "Open items" sections
- Modify: `docs/planning/02-external-services-and-hosting.md:20-21`, `:104`, `:108-117`

**Interfaces:**
- Consumes: everything built in tasks 1 through 8.
- Produces: nothing code depends on.

- [ ] **Step 1: Correct the plan-tier figure in `CLAUDE.md`**

`CLAUDE.md:102` currently reads:

```markdown
- **Vercel Hobby retains runtime logs for 1 hour.** Sentry is the only durable error record — wire it up early, not last.
```

Replace it with:

```markdown
- **Vercel Pro retains runtime logs for 1 day** (Hobby: 1 hour; 30-day retention needs the paid Observability Plus add-on). This project is on Pro, so the earlier "1 hour" figure in these docs was wrong — but the conclusion it supported is not: **Sentry is still the only durable error record.** A day is long enough to miss and short enough to lose a recurring failure nobody was watching for.
```

- [ ] **Step 2: Add the new package to the repo layout**

At `CLAUDE.md:56`, extend the package list so it reads `internal/{analytics,api,audit,auth,authz,cache,config,db,destination,domainverify,link,observability,pages,slug,supabase}`, and extend the sentence that follows:

```markdown
Redis lives behind `cache`, not an `internal/redis`, and Sentry lives behind `observability`, not an `internal/sentry` — that package is the only importer of `sentry-go`, so the policy about what may leave this process has one home.
```

- [ ] **Step 3: Add the new entries under "Non-obvious constraints"**

Append these six bullets:

```markdown
- **`/health` and `/health/deep` are not interchangeable.** The flat one is unauthenticated, answers on every hostname and touches nothing — the platform calls it, and `domainverify`'s reachability probe fetches `https://<claimed-hostname>/health` and requires `"status":"ok"` in the body, so making it touch the database would put a Postgres round trip inside domain verification. `/health/deep` is the token-guarded one that runs `select 1` and a Redis `PING`; `HEALTH_CHECK_TOKEN` unset disables it (404 for everyone), which is fail-closed on purpose. Neither is in the OpenAPI spec.
- **Postgres failing is 503; Redis failing is 200.** In `/health/deep` this is a decision about which channel carries which severity, not an oversight. With Redis down every redirect still works through Postgres — degradation, which travels to Sentry through the error log. Returning 503 there would page the maintainer about a service that is serving, which is how an alert channel gets ignored. `TestDeepHealthAnswers200WhenOnlyRedisIsUnreachable` exists to stop someone "fixing" it.
- **Neither Supabase project may go seven days without activity** — the free tier pauses it, and restoring is manual and slow. Production is kept awake by Better Stack polling `/health/deep` every three minutes, plus a daily `curl` from `.github/workflows/keep-alive.yml` so the whole thing does not depend on Better Stack continuing to run. Preview is kept awake only by that workflow's `psql` step, because a preview deployment has no stable URL to monitor and does not exist at all between pull requests. GitHub disables scheduled workflows after sixty days of repository inactivity, with a warning email first.
- **`observability.Middleware()` goes inside `middleware.Recoverer`, never outside.** Recoverer innermost swallows the panic and Sentry sees nothing; the Sentry middleware outermost without `Repanic` answers 500 itself and Recoverer never logs the stack trace. Its `WaitForDelivery` is the only flush on any request path here, and only a panic reaches it — golden rule 2 permits waiting on a response that is already 5xx, and nothing else.
- **The Sentry scrubber drops the request body, and that line is mandatory.** `POST /{slug}/verify` carries a link's password in cleartext, so without it the first panic on the password interstitial ships that password to Sentry. Headers are an allowlist (`User-Agent` only), never a denylist, so a proxy header the platform adds next year is excluded by construction. Only `slog.LevelError` produces an event: `Warn` is used here for expected conditions, and the free tier allows 5,000 events a month.
- **Web source maps are uploaded and then deleted.** `sentryTanstackStart` must be the **last** Vite plugin, and its `filesToDeleteAfterUpload` is not tidying: uploading without deleting publishes the app's source beside its bundle. The plugin is registered only when `SENTRY_AUTH_TOKEN` is present, so local and fork builds still work. And `apps/web` never reports what `classifyApiError` names — a 403, a 422, a field error are rendered as UI on purpose, and reporting them would spend the monthly budget on events the visitor already saw.
```

- [ ] **Step 4: Update "Open items"**

Replace the "Alert notification channel" bullet with:

```markdown
- **Alert notification channel** is email, to the maintainer's address, for all three of Sentry, Better Stack and the Vercel resource thresholds — chosen because it needs no further service. A webhook into whatever the maintainers actually watch remains the obvious upgrade. Still genuinely open: whether 5,000 Sentry events a month holds once more than a handful of Vereine participate, and whether the three-minute monitor interval stays worth roughly 3% of the daily Redis command budget.
```

- [ ] **Step 5: Correct doc 02**

Three edits in `docs/planning/02-external-services-and-hosting.md`:

At line 20, the Sentry row's note currently ties itself to "a real Vercel Hobby limitation" — change it to "a real Vercel log-retention limitation (1 day on Pro, this project's plan)". At line 21, mark Better Stack as built: append "— built 2026-09-07, three monitors, email alerts".

At line 104, replace the parenthetical figures so the paragraph reads from Pro's perspective:

```markdown
Finding that makes this more than a nice-to-have: **Vercel retains runtime logs for 1 day on the Pro plan** (Hobby: 1 hour; 30-day retention needs the paid Observability Plus add-on). Without a separate error-tracking layer, any bug that isn't caught within a day of happening leaves no trace at all — for a small, infrequently-checked project, that's a real risk of genuinely never finding out about recurring failures (e.g. a Safe Browsing scan silently failing, a Redis connection error on the redirect hot path).
```

Then, at the end of the Better Stack section, record what was actually built and the one thing that was rejected:

```markdown
**Built 2026-09-07** as three monitors rather than one: `/health/deep` on the API hostname (token-guarded, checks Postgres and Redis), the flat `/health` on `go.kurze-url.app` (proves DNS, TLS and the process on the redirect surface), and the web project's landing page. Monitoring a real short link end to end was considered and rejected: a cache hit never reaches Postgres, so it would not keep Supabase awake, and at a three-minute interval it would add 480 clicks a day to a real link's statistics.
```

- [ ] **Step 6: Commit**

```bash
but diff
but commit -b feat/observability -m "docs: record the observability decisions"
```

---

## Closing checklist

Run through this after Task 9, before opening the pull request.

- [ ] `cd apps/api && go test ./... && go vet ./...` passes.
- [ ] `pnpm test && pnpm typecheck && pnpm lint` passes.
- [ ] `pnpm --filter @kurze-url/web build` passes with no `SENTRY_AUTH_TOKEN`.
- [ ] Both Vercel deployments are green, and the e2e suite still passes — no migration is added by this plan, so the preview-schema trap does not apply.
- [ ] The `keep alive` workflow ran on the pull request and its preview-database step passed.
- [ ] `grep -rn "sentry-go" apps/api --include='*.go' | grep -v internal/observability` returns nothing. If it does, the one-home rule is already broken.

Then, and only then, the parts no test can reach:

- [ ] **Trigger one real error in the API** and confirm it appears in the Sentry project — for example, request `/health/deep` on the preview with a valid token while `REDIS_URL` points somewhere dead, which exercises the Redis-degradation path deliberately. Confirm the event carries a `User-Agent` and **no** `X-Forwarded-For`, no cookies, and no IP address.
- [ ] **Trigger one real error in the web app** and confirm it appears in its Sentry project with a readable, un-minified stack trace. A minified trace means the source-map upload did not run.
- [ ] **Confirm a `.map` file is not served** from the preview deployment.
- [ ] **Dispatch the keep-alive workflow manually** once the branch has merged (`workflow_dispatch` only works from the default branch) and confirm both steps pass.
- [ ] **Confirm all three Better Stack monitors report up**, and that the token-guarded one is not quietly reporting 404.
- [ ] Note the Sentry event count after a day. If a single error class is already dominating, that is the sampling conversation the spec's open questions describe — not a reason to raise the log level.
