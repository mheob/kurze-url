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
	"github.com/mheob/kurze-url/apps/api/internal/supabase"
)

const (
	clickFlushInterval = 5 * time.Second
	clickBufferMax     = 5000
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	// Production connects through Supavisor's transaction pooler, which
	// multiplexes many client connections onto far fewer server connections.
	// pgx's default mode caches prepared statements per connection, so a
	// statement it cached earlier is already present on whichever server
	// connection it borrows next — Postgres answers "prepared statement
	// already exists" (SQLSTATE 42P05) and the API dies at startup.
	//
	// QueryExecModeExec uses unnamed statements and keeps no cache, which is
	// the mode the pooler supports. It costs almost nothing here: these are
	// short-lived serverless invocations, so a per-connection statement cache
	// rarely survives long enough to be reused anyway. Set unconditionally
	// rather than only when a pooler host is detected — a connection string
	// that silently changes the query protocol depending on its hostname is
	// worse than one slightly slower path in local development.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	redis, err := cache.New(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()

	queries := db.New(pool)

	sharedDomain, err := api.ProvisionSharedDomain(ctx, queries, cfg.SharedDomainHostname)
	if err != nil {
		return err
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
		Log:          log,
	}

	// Authentication is optional at startup so the redirect surface stays
	// runnable locally without a Supabase project. /v1 operations that declare
	// bearerAuth reject every request until it is configured.
	if cfg.JWKSURL != "" {
		verifier, err := auth.NewVerifier(ctx, cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
		if err != nil {
			return err
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
			return fmt.Errorf("configure the supabase admin client: %w", err)
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
		return err
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

	return shutdownErr
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
