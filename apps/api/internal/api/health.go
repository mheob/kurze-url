package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// healthCheckBudget bounds the whole deep check rather than each dependency.
// The uptime monitor calling this has its own timeout, and two serial
// three-second waits would outlast it — at which point the monitor reports an
// outage caused by the outage check.
//
// "The whole check" is why the two pings run concurrently below. Run
// serially under one shared deadline, a Postgres that hangs for the entire
// budget leaves nothing of it for Redis: the Redis ping then returns
// "context deadline exceeded" the instant it starts, the body says Redis
// failed while Redis is healthy, and an error log — one Sentry event on each
// of the 480 daily polls — says so too.
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
	//
	// ConstantTimeCompare rather than !=: not because a timing attack on this
	// token is practical over the network, but because one line removes the
	// question for good. It returns 0 for differing lengths, so a wrong-length
	// token is refused without a second check.
	if d.Config.HealthCheckToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(r.Header.Get("X-Health-Token")),
			[]byte(d.Config.HealthCheckToken),
		) != 1 {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthCheckBudget)
	defer cancel()

	// Concurrent, sharing the one budget: see healthCheckBudget. It also
	// halves the worst case, since the endpoint now takes as long as its
	// slower dependency rather than as long as both.
	var postgresErr, redisErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		postgresErr = d.pingPostgres(ctx)
	}()
	go func() {
		defer wg.Done()
		redisErr = d.pingRedis(ctx)
	}()

	wg.Wait()

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
