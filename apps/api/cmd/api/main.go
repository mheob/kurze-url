// Command api is the single entrypoint Vercel's Go Framework Preset detects.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
	"github.com/mheob/kurze-url/apps/api/internal/observability"
	"github.com/mheob/kurze-url/apps/api/internal/supabase"
)

const (
	clickFlushInterval = 5 * time.Second
	clickBufferMax     = 5000
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	log, flushSentry, err := run(log)
	if err != nil {
		log.Error("api exited with error", "error", err)
		// After the fatal log, before the exit. Capturing an event only
		// enqueues it onto the transport's queue; os.Exit runs no deferred
		// function, so a flush that happened when run returned would have
		// drained the queue just before the one event worth having was put
		// on it — and a Vercel crash loop, the case most in need of a
		// durable record, would produce nothing at all.
		flushSentry()
		os.Exit(1)
	}

	flushSentry()
}

// run returns the logger it ended up using and the Sentry flush to run at
// the very end, alongside any error.
//
// The logger, because a *slog.Logger is a pointer: reassigning the local
// "log" parameter inside run — as the Sentry-wrap below does — cannot be
// observed by main's own "log" variable, so returning it is the only way
// main sees the wrapped one.
//
// The flush for the same reason, rather than an exported
// observability.Flush(timeout) reaching for the global hub: keeping it a
// value the caller holds means the no-op case (no DSN, or an unusable one)
// is the same shape as the real one, with no package-level state deciding
// which happened. Both were acceptable; this one keeps observability's
// surface as small as it already is.
//
// Every return path below returns both as they stand at that point,
// including the early ones that fail before Sentry is configured at all —
// those callers get a usable, if unwrapped, logger and a flush that does
// nothing.
func run(log *slog.Logger) (*slog.Logger, func(), error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Until Sentry is configured a few lines down there is nothing to flush,
	// and every early return before that point hands main this.
	noFlush := func() {}

	cfg, err := config.Load()
	if err != nil {
		return log, noFlush, err
	}

	// As early as the DSN is known, which is after config.Load.
	flushSentry, err := observability.Init(cfg.SentryDSN, cfg.Environment, cfg.Release)

	switch {
	case err != nil:
		// Not fatal, and deliberately unlike every other error in this
		// function. Sentry is an optional dependency exactly as the JWKS URL
		// and the service-role key below are, and it is the only one whose
		// failure would take the redirect surface — the thing this project
		// exists to serve — down over a typo in an environment variable.
		log.Warn("sentry initialisation failed — errors are logged but not reported", "error", err)
		flushSentry = noFlush
	case cfg.SentryDSN == "":
		log.Warn("SENTRY_DSN is unset — errors are logged but not reported")
	default:
		// Every existing Log.Error call site becomes a Sentry event from
		// here on, including the two in HandleDeepHealth.
		log = slog.New(observability.NewSlogHandler(log.Handler()))
	}

	// internal/pages logs through slog's package-level default rather than an
	// injected logger — it renders the redirect surface's HTML and has no
	// Deps to take one from — so without this line a template failure on the
	// surface this whole project exists for is written to stderr and reported
	// nowhere. Not redundant with the wrap above: it is what makes the wrap
	// reach code that never sees this variable, including anything a
	// dependency logs through the default.
	slog.SetDefault(log)

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return log, flushSentry, err
	}

	// Production connects through Supavisor's transaction pooler, which
	// multiplexes many client connections onto far fewer server connections.
	// pgx's default mode (QueryExecModeCacheStatement) prepares and caches a
	// *named* server-side statement per connection, so a statement it cached
	// earlier is already present on whichever server connection it borrows
	// next — Postgres answers "prepared statement already exists" (SQLSTATE
	// 42P05) and the API dies at startup.
	//
	// QueryExecModeExec (tried first) avoids that by never asking Postgres to
	// describe a statement's parameter types at all, but that has a cost of
	// its own: with no server-described type, pgx can only guess a parameter's
	// wire encoding from the Go argument's own type. That guess is wrong for
	// anything whose correct encoding depends on the actual column type —
	// e.g. a []byte bound to jsonb goes out as bytea and Postgres rejects it
	// (SQLSTATE 22P02), and a []uuid.UUID bound to a uuid[] parameter has no
	// default encoding at all ("cannot find encode plan"). Both shapes exist
	// in this codebase (audit_log.metadata, the tag-id array queries) and
	// both broke in production.
	//
	// QueryExecModeCacheDescribe fixes that while remaining pooler-safe. Per
	// pgx's conn.go: on a cache miss it calls Prepare(ctx, "", sql) — an
	// *unnamed* statement (Parse.Name == "") — purely to learn each
	// parameter's real OID, then throws the server-side statement away; the
	// OIDs are cached client-side, keyed by SQL text, on the *pgx.Conn.
	// Every execution — cache hit or miss — then goes through
	// PgConn.ExecParams, which sends its own fresh, self-contained, unnamed
	// Parse+Bind+Describe+Execute+Sync in one flush. Nothing here ever names
	// a statement, and no round trip depends on server-side state a *different*
	// round trip created, which is exactly the failure mode
	// QueryExecModeCacheStatement (named statements) and
	// QueryExecModeDescribeExec (an unnamed statement referenced from a
	// *second*, later round trip) both have under a pooler that may swap the
	// backing backend connection between round trips. The OIDs cached here
	// describe the schema, not a specific backend, so they stay valid no
	// matter which backend a later round trip lands on.
	//
	// Cost: once a given query text has been described on a given pooled
	// connection (once per process lifetime, since this is a long-lived
	// server, not a per-request cold start), every subsequent execution of it
	// costs exactly one round trip — the same as QueryExecModeExec today.
	// GET /<slug>, the redirect hot path, is unaffected either way: it is
	// served from Redis on a hit and only falls through to Postgres on a
	// cache miss.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return log, flushSentry, err
	}
	defer pool.Close()

	redis, err := cache.New(cfg.RedisURL)
	if err != nil {
		return log, flushSentry, err
	}
	defer func() { _ = redis.Close() }()

	queries := db.New(pool)

	sharedDomain, err := api.ProvisionSharedDomain(ctx, queries, cfg.SharedDomainHostname)
	if err != nil {
		return log, flushSentry, err
	}
	log.Info("shared domain ready", "hostname", sharedDomain.Hostname, "domain_id", sharedDomain.ID)

	recorder := analytics.NewRecorder(clickStatsFlush(queries), clickFlushInterval, clickBufferMax, log)

	deps := api.Deps{
		Config:       cfg,
		SharedDomain: sharedDomain,
		Queries:      queries,
		Pool:         pool,
		Cache:        redis,
		Recorder:     recorder,
		// Built once here, not per request: a Verifier built per request
		// would rebuild its transport every time and defeat the connection
		// settings (dialer Control hook, disabled keep-alives, timeouts)
		// domainverify.NewVerifier configures.
		DomainVerifier: domainverify.NewVerifier(log),
		Log:            log,
	}

	// Authentication is optional at startup so the redirect surface stays
	// runnable locally without a Supabase project. /v1 operations that declare
	// bearerAuth reject every request until it is configured.
	if cfg.JWKSURL != "" {
		verifier, err := auth.NewVerifier(ctx, cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
		if err != nil {
			return log, flushSentry, err
		}
		deps.Verifier = verifier
	} else {
		log.Warn("SUPABASE_JWKS_URL is unset — authenticated /v1 operations will reject all requests")
	}

	// Invitations are optional at startup, like authentication: without a
	// service-role key the API runs, and only the invite branch of
	// POST /v1/teams/{team_id}/members refuses.
	if cfg.SupabaseServiceRoleKey != "" {
		admin, err := supabase.NewClient(cfg.SupabaseAuthURL, cfg.SupabaseServiceRoleKey)
		switch {
		case errors.Is(err, supabase.ErrNotConfigured):
			// SUPABASE_SERVICE_ROLE_KEY is set but SUPABASE_AUTH_URL (and its
			// SUPABASE_JWT_ISSUER fallback) is not — an incomplete but
			// recoverable configuration. Disabling invitations, like the
			// unset-key branch below, keeps the rest of the API usable
			// instead of refusing to start over one optional feature.
			log.Warn("supabase auth url is unset — team invitations are disabled")
		case err != nil:
			return log, flushSentry, fmt.Errorf("configure the supabase admin client: %w", err)
		default:
			deps.Admin = admin
			log.Info("supabase invitations enabled")
		}
	} else {
		log.Warn("SUPABASE_SERVICE_ROLE_KEY is unset — team invitations are disabled")
	}

	// The recorder gets its own context, independent of the signal-cancelled
	// one everything else uses. If it shared ctx, SIGTERM would cancel it
	// immediately — the recorder would perform its final flush and exit
	// before srv.Shutdown finishes draining in-flight redirects, so those
	// requests' Record calls would land in a buffer nothing ever flushes.
	// recorderCtx is cancelled only once srv.Shutdown has returned, below.
	recorderCtx, cancelRecorder := context.WithCancel(context.Background())
	defer cancelRecorder()

	recorderDone := make(chan struct{})
	go func() {
		recorder.Run(recorderCtx)
		close(recorderDone)
	}()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "port", cfg.Port, "api_hostname", cfg.APIHostname)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return log, flushSentry, err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)

	// Only now, with every in-flight redirect's Record call already made, is
	// it safe to let the recorder perform its final flush.
	cancelRecorder()

	// The recorder flushes whatever is still buffered when recorderCtx is
	// cancelled; wait for that before the process exits, or those clicks are
	// lost.
	select {
	case <-recorderDone:
	case <-shutdownCtx.Done():
		log.Warn("timed out waiting for the final click-stats flush")
	}

	return log, flushSentry, shutdownErr
}

// clickStatsFlush adapts the recorder's rows onto the generated batch upsert.
func clickStatsFlush(queries *db.Queries) analytics.FlushFunc {
	return func(ctx context.Context, rows []analytics.Row) error {
		params := make([]db.UpsertClickStatsParams, 0, len(rows))
		for _, row := range rows {
			params = append(params, db.UpsertClickStatsParams{
				LinkID:         row.LinkID,
				BucketStart:    row.Day,
				DimensionType:  row.DimType,
				DimensionValue: row.DimValue,
				Clicks:         row.Clicks,
				UniqueVisitors: row.Unique,
			})
		}

		var firstErr error
		results := queries.UpsertClickStats(ctx, params)
		results.Exec(func(_ int, err error) {
			if err != nil && firstErr == nil {
				firstErr = err
			}
		})
		if err := results.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
}
