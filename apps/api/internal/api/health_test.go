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

// The two pings share one budget, and sharing it serially meant the first
// one to hang spent all of it: Postgres holding the full three seconds left
// the Redis ping to return "context deadline exceeded" the moment it started,
// so the body blamed a Redis that was answering fine — and the error log said
// so on all 480 of the day's polls.
//
// The Postgres ping here blocks until its context is done, exactly as a hung
// connection does, and the Redis ping reports the context the way a real
// client does — a call handed an already-expired context fails immediately
// rather than ignoring it. That second half is what gives this test its
// falsification value: run the two serially again and Redis reports failed.
func TestASlowPostgresDoesNotMakeRedisReportFailed(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.HealthCheckToken = testHealthToken
	f.deps.PingPostgres = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	f.deps.PingRedis = func(ctx context.Context) error { return ctx.Err() }

	rec := deepHealth(t, api.NewRouter(f.deps), testHealthToken)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, map[string]string{"postgres": "failed", "redis": "ok"}, decodeChecks(t, rec))
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
