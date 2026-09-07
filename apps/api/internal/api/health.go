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
