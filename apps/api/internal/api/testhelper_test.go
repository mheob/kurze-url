package api_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestMain(m *testing.M) {
	for name, value := range map[string]string{
		"DATABASE_URL": "postgres://postgres:postgres@127.0.0.1:54322/postgres",
		"REDIS_URL":    "redis://127.0.0.1:6379",
		"VISITOR_SALT": "test-salt",
		"API_HOSTNAME": "api.test",
	} {
		if os.Getenv(name) == "" {
			_ = os.Setenv(name, value)
		}
	}
	os.Exit(m.Run())
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func testCache(t *testing.T) *cache.Client {
	t.Helper()

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	client, err := cache.New(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}

type fixture struct {
	deps     api.Deps
	pool     *pgxpool.Pool
	hostname string
	linkID   uuid.UUID
	rows     *[]analytics.Row
}

// newFixture builds a fully wired Deps against a real Postgres and Redis, plus
// a throwaway team, verified domain and link. Everything it inserts is removed
// on cleanup so the suite can run repeatedly against one local database.
func newFixture(t *testing.T, opts ...func(*linkOptions)) *fixture {
	t.Helper()
	ctx := context.Background()

	pool := testPool(t)
	client := testCache(t)

	options := linkOptions{
		slug:         "hello",
		destination:  "https://example.org/hello",
		redirectType: 302,
		state:        "active",
	}
	for _, opt := range opts {
		opt(&options)
	}

	hostname := "t" + uuid.NewString()[:8] + ".test"

	var userID, teamID, domainID, linkID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('fixture') returning id`).Scan(&teamID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, $3, now()) returning id`,
		teamID, hostname, options.verification()).Scan(&domainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, redirect_type,
		                   state, expires_at, password_hash, analytics_enabled, created_by)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
		domainID, teamID, options.slug, options.destination, options.redirectType,
		options.state, options.expiresAt, options.passwordHash,
		!options.analyticsOff, userID).Scan(&linkID))

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	var recorded []analytics.Row
	recorder := analytics.NewRecorder(
		func(_ context.Context, rows []analytics.Row) error {
			recorded = append(recorded, rows...)
			return nil
		},
		time.Hour, 100000,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	cfg, err := config.Load()
	require.NoError(t, err)

	return &fixture{
		pool:     pool,
		hostname: hostname,
		linkID:   linkID,
		rows:     &recorded,
		deps: api.Deps{
			Config:   cfg,
			Queries:  db.New(pool),
			Cache:    client,
			Recorder: recorder,
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			Now:      func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) },
		},
	}
}

type linkOptions struct {
	slug         string
	destination  string
	redirectType int
	state        string
	expiresAt    *time.Time
	passwordHash *string
	unverified   bool
	analyticsOff bool
}

func (o linkOptions) verification() string {
	if o.unverified {
		return "pending"
	}
	return "verified"
}

//nolint:unused // reserved for a later test file in this package that needs a non-default slug.
func withSlug(s string) func(*linkOptions)  { return func(o *linkOptions) { o.slug = s } }
func withState(s string) func(*linkOptions) { return func(o *linkOptions) { o.state = s } }
func withRedirectType(t int) func(*linkOptions) {
	return func(o *linkOptions) { o.redirectType = t }
}
func withExpiry(at time.Time) func(*linkOptions) {
	return func(o *linkOptions) { o.expiresAt = &at }
}
func withPasswordHash(h string) func(*linkOptions) {
	return func(o *linkOptions) { o.passwordHash = &h }
}
func unverifiedDomain() func(*linkOptions)  { return func(o *linkOptions) { o.unverified = true } }
func analyticsDisabled() func(*linkOptions) { return func(o *linkOptions) { o.analyticsOff = true } }
