package api_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

var (
	tenancyCacheOnce   sync.Once
	tenancyCacheClient *cache.Client
	tenancyCacheErr    error
)

// tenancyCache starts one Redis container for the whole package. The tenancy
// suite builds many fixtures, and a container per fixture would dominate the
// suite's runtime.
func tenancyCache(t *testing.T) *cache.Client {
	t.Helper()

	tenancyCacheOnce.Do(func() {
		ctx := context.Background()
		container, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			tenancyCacheErr = err
			return
		}
		url, err := container.ConnectionString(ctx)
		if err != nil {
			tenancyCacheErr = err
			return
		}
		tenancyCacheClient, tenancyCacheErr = cache.New(url)
	})

	if tenancyCacheErr != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", tenancyCacheErr)
	}
	return tenancyCacheClient
}

// fakeInviter stands in for Supabase's Admin API. It records what it was asked
// to send so a test can assert an email was or was not triggered.
type fakeInviter struct {
	calls    []string
	metadata []map[string]any
	userID   uuid.UUID
	err      error

	// t and pool let a successful InviteUser seed the auth.users row for the
	// invited address itself, exactly as the real Supabase Admin API would
	// as a side effect of accepting an invite. That row has to exist by the
	// time the handler inserts the team_member row (the foreign key demands
	// it), and it must NOT exist any earlier than that: seeding it upfront,
	// before the request, would make the address indistinguishable from one
	// that already had an account, and GetUserIDByEmail runs before this
	// call — so an early seed would short-circuit the invite entirely.
	t    *testing.T
	pool *pgxpool.Pool
}

func (f *fakeInviter) InviteUser(
	ctx context.Context, email string, data map[string]any,
) (uuid.UUID, error) {
	f.calls = append(f.calls, email)
	f.metadata = append(f.metadata, data)
	if f.err != nil {
		return uuid.Nil, f.err
	}
	seedAuthUserWithID(ctx, f.t, f.pool, f.userID, email)
	return f.userID, nil
}

type testUser struct {
	id    uuid.UUID
	email string
}

// tenancyFixture is one team with one member per role, a stranger who belongs
// to no team, a real JWKS-backed verifier and a wired /v1 router.
type tenancyFixture struct {
	deps     api.Deps
	pool     *pgxpool.Pool
	key      *ecdsa.PrivateKey
	router   http.Handler
	teamID   uuid.UUID
	members  map[authz.Role]testUser
	stranger testUser
	invites  *fakeInviter

	sharedDomainID uuid.UUID
	teamDomainID   uuid.UUID
	teamHostname   string
	linkID         uuid.UUID
}

// seedAuthUser inserts a Supabase auth user. The column list mirrors
// supabase/seed.sql — auth.users belongs to Supabase, and tests must not
// invent a different shape for it.
func seedAuthUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, email string) testUser {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx,
		`insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
		                         email_confirmed_at, created_at, updated_at)
		 values ($1, '00000000-0000-0000-0000-000000000000', 'authenticated',
		         'authenticated', $2, '', now(), now(), now())`, id, email)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, id)
	})

	return testUser{id: id, email: email}
}

// seedAuthUserWithID seeds an auth user under a caller-chosen ID, for the
// invite path where the fake inviter decides the new user's ID.
func seedAuthUserWithID(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool, id uuid.UUID, email string,
) testUser {
	t.Helper()

	_, err := pool.Exec(ctx,
		`insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
		                         email_confirmed_at, created_at, updated_at)
		 values ($1, '00000000-0000-0000-0000-000000000000', 'authenticated',
		         'authenticated', $2, '', now(), now(), now())`, id, email)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, id)
	})

	return testUser{id: id, email: email}
}

// rebuildRouter re-registers /v1 after a test mutated f.deps.Config.
func (f *tenancyFixture) rebuildRouter() {
	router := chi.NewRouter()
	f.deps.RegisterV1(humachi.New(router, api.NewHumaConfig()))
	f.router = router
}

func newTenancyFixture(t *testing.T) *tenancyFixture {
	t.Helper()
	ctx := context.Background()

	pool := testPool(t)
	redis := tenancyCache(t)

	suffix := uuid.NewString()[:8]
	members := map[authz.Role]testUser{}
	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleEditor, authz.RoleAdmin, authz.RoleOwner} {
		members[role] = seedAuthUser(ctx, t, pool, role.String()+"-"+suffix+"@verein.test")
	}
	stranger := seedAuthUser(ctx, t, pool, "stranger-"+suffix+"@verein.test")

	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ($1) returning id`, "Verein "+suffix).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	for role, user := range members {
		_, err := pool.Exec(ctx,
			`insert into team_member (team_id, user_id, role) values ($1, $2, $3)`,
			teamID, user.id, role.String())
		require.NoError(t, err)
	}

	sharedHostname := "shared-" + suffix + ".test"
	var sharedDomainID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values (null, $1, 'verified', now()) returning id`, sharedHostname).Scan(&sharedDomainID))
	// A team-less domain is not reached by the team cascade, so it needs its
	// own cleanup or the suite leaks a row per fixture.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where id = $1`, sharedDomainID)
	})

	teamHostname := "team-" + suffix + ".test"
	var teamDomainID, linkID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		teamID, teamHostname).Scan(&teamDomainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 'fixture', 'https://example.org/fixture', $3) returning id`,
		teamDomainID, teamID, members[authz.RoleOwner].id).Scan(&linkID))

	key, jwksURL := startAuthenticatedJWKSServer(t)
	verifier, err := auth.NewVerifier(ctx, jwksURL, meTestIssuer, meTestAudience)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	// The owner is the instance maintainer in these tests; every other user
	// must be refused by POST /v1/teams.
	cfg.MaintainerUserIDs = []uuid.UUID{members[authz.RoleOwner].id}
	cfg.InviteRateLimitPerHour = 20
	cfg.SharedDomainHostname = sharedHostname
	cfg.LinkCreateRateLimitPerMin = 100

	invites := &fakeInviter{userID: uuid.New(), t: t, pool: pool}

	// The redirect helper below exercises the real HandleRedirect, which
	// records a click on every successful redirect — so this fixture needs a
	// working Recorder too, not only the /v1 surface's dependencies.
	recorder := analytics.NewRecorder(
		func(_ context.Context, _ []analytics.Row) error { return nil },
		time.Hour, 100000,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	f := &tenancyFixture{
		pool:           pool,
		key:            key,
		teamID:         teamID,
		members:        members,
		stranger:       stranger,
		invites:        invites,
		sharedDomainID: sharedDomainID,
		teamDomainID:   teamDomainID,
		teamHostname:   teamHostname,
		linkID:         linkID,
		deps: api.Deps{
			Config:       cfg,
			SharedDomain: api.SharedDomain{ID: sharedDomainID, Hostname: sharedHostname},
			Queries:      db.New(pool),
			Pool:         pool,
			Cache:        redis,
			Verifier:     verifier,
			Admin:        invites,
			Recorder:     recorder,
			Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}

	router := chi.NewRouter()
	f.deps.RegisterV1(humachi.New(router, api.NewHumaConfig()))
	f.router = router

	return f
}

// do issues a request to the /v1 surface as the given user. Pass an empty
// testUser to send it unauthenticated.
func (f *tenancyFixture) do(
	t *testing.T, as testUser, method, path string, body any,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if as.id != uuid.Nil {
		req.Header.Set("Authorization",
			"Bearer "+signMeToken(t, f.key, as.id.String(), as.email))
	}

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// redirect issues a request to the public redirect surface on a short-link
// hostname, using the same router the API serves.
func (f *tenancyFixture) redirect(t *testing.T, hostname, slug string) *httptest.ResponseRecorder {
	t.Helper()

	router := chi.NewRouter()
	router.Get("/{slug}", f.deps.HandleRedirect)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://"+hostname+"/"+slug, nil)
	router.ServeHTTP(rec, req)
	return rec
}

// decode unmarshals a successful JSON response body.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

func TestMeListsTheCallersTeamMemberships(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodGet, "/v1/me", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode[struct {
		UserID      uuid.UUID `json:"user_id"`
		Email       string    `json:"email"`
		Memberships []struct {
			TeamID uuid.UUID `json:"team_id"`
			Name   string    `json:"name"`
			Role   string    `json:"role"`
		} `json:"memberships"`
	}](t, rec)

	require.Equal(t, f.members[authz.RoleEditor].id, body.UserID)
	require.Len(t, body.Memberships, 1)
	require.Equal(t, f.teamID, body.Memberships[0].TeamID)
	require.Equal(t, "editor", body.Memberships[0].Role)
}

func TestMeReturnsAnEmptyMembershipListForANewUser(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/me", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"memberships":[]`,
		"a user with no teams must get [], not null")
}
