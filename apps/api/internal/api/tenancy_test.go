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
	"strings"
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
	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
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

// stubDomainVerifier lets a test dictate the outcome of a domain
// verification check without a real DNS lookup or TLS handshake to a third
// party. It is assigned into Deps.DomainVerifier as a pointer, so a test can
// mutate f.domainVerifier.reason after the fixture is built and have the
// already-registered handler see the change — unlike a Config field, this
// needs no f.rebuildRouter() call. calls counts every invocation of Check, so
// a test can pin the already-verified short-circuit: it must stay at 1 even
// after a later probe would have failed.
type stubDomainVerifier struct {
	reason domainverify.Reason
	err    error
	calls  int
}

func (s *stubDomainVerifier) Check(context.Context, string, string) (domainverify.Reason, error) {
	s.calls++
	return s.reason, s.err
}

// tenancyFixture is one team with one member per role, a stranger who belongs
// to no team, a real JWKS-backed verifier and a wired /v1 router.
type tenancyFixture struct {
	deps           api.Deps
	pool           *pgxpool.Pool
	key            *ecdsa.PrivateKey
	router         http.Handler
	teamID         uuid.UUID
	members        map[authz.Role]testUser
	stranger       testUser
	invites        *fakeInviter
	domainVerifier *stubDomainVerifier

	sharedDomainID uuid.UUID
	teamDomainID   uuid.UUID
	teamHostname   string
	linkID         uuid.UUID
	folderID       uuid.UUID
	tagID          uuid.UUID

	// emptyDomainID is a second team-owned, verified domain with no link on
	// it — unlike teamDomainID, which always carries the "fixture" link.
	// TestRolePermissionMatrix's delete-domain case needs a domain it can
	// actually delete; deleting teamDomainID would 409 for every role at or
	// above admin, since it has a link.
	emptyDomainID uuid.UUID

	// otherTeamID and otherAdmin are a second, independent team with a single
	// admin member. Domain claims are deliberately not unique per hostname —
	// several teams may hold one on the same hostname at once — so proving
	// that needs two real teams in one fixture, not the two-full-fixtures
	// pattern organization_isolation_test.go uses for "does team A leak into
	// team B's list" cases.
	otherTeamID uuid.UUID
	otherAdmin  testUser
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

	var emptyDomainID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		teamID, "empty-"+suffix+".test").Scan(&emptyDomainID))

	var folderID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into folder (team_id, name) values ($1, 'fixture') returning id`,
		teamID).Scan(&folderID))

	// Named "fixture", deliberately not "Matrix": the create-tag matrix case
	// posts a tag named "Matrix" against a fresh fixture for every role, and a
	// seeded tag of the same name would make that create collide on the
	// unique-name index (409) rather than succeed — a false read as an
	// authorization failure instead of the cap/uniqueness bug it would be.
	var tagID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into tag (team_id, name) values ($1, 'fixture') returning id`,
		teamID).Scan(&tagID))

	var otherTeamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ($1) returning id`, "Anderer Verein "+suffix).Scan(&otherTeamID))
	otherAdmin := seedAuthUser(ctx, t, pool, "other-admin-"+suffix+"@verein.test")
	// Registered after seedAuthUser(otherAdmin) so LIFO deletes the team
	// first, same ordering as the members/teamID block above: the team_id
	// cascade removes team_member regardless of whether auth.users still
	// exists, so this does not depend on team_member.user_id also cascading.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, otherTeamID)
	})
	_, err := pool.Exec(ctx,
		`insert into team_member (team_id, user_id, role) values ($1, $2, 'admin')`,
		otherTeamID, otherAdmin.id)
	require.NoError(t, err)

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
	domainVerifierStub := &stubDomainVerifier{reason: domainverify.ReasonNone}

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
		domainVerifier: domainVerifierStub,
		sharedDomainID: sharedDomainID,
		teamDomainID:   teamDomainID,
		teamHostname:   teamHostname,
		emptyDomainID:  emptyDomainID,
		linkID:         linkID,
		folderID:       folderID,
		tagID:          tagID,
		otherTeamID:    otherTeamID,
		otherAdmin:     otherAdmin,
		deps: api.Deps{
			Config:         cfg,
			SharedDomain:   api.SharedDomain{ID: sharedDomainID, Hostname: sharedHostname},
			Queries:        db.New(pool),
			Pool:           pool,
			Cache:          redis,
			Verifier:       verifier,
			DomainVerifier: domainVerifierStub,
			Admin:          invites,
			Recorder:       recorder,
			Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
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

// doRaw is do's raw-body counterpart, for a test that must send a body that
// is not built from a Go map, so it can transmit exact JSON text — e.g.
// `{"folder_id": null}` written out literally, rather than relying on
// map[string]any{"folder_id": nil} (which does marshal to the same bytes, but
// leaves the literal the test cares about implicit in a Go value instead of
// visible in the test itself).
func (f *tenancyFixture) doRaw(
	t *testing.T, as testUser, method, path, rawBody string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
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

func TestMeReportsMaintainerStatus(t *testing.T) {
	f := newTenancyFixture(t)

	// The fixture makes the owner the instance maintainer and nobody else, the
	// same arrangement TestCreateTeamIsRefusedForANonMaintainer relies on. Both
	// directions are asserted from one fixture: a flag that is always true, or
	// always false, would satisfy either half on its own.
	for _, want := range []struct {
		as           testUser
		isMaintainer bool
	}{
		{as: f.members[authz.RoleOwner], isMaintainer: true},
		{as: f.members[authz.RoleAdmin], isMaintainer: false},
	} {
		rec := f.do(t, want.as, http.MethodGet, "/v1/me", nil)

		require.Equal(t, http.StatusOK, rec.Code)
		body := decode[struct {
			IsMaintainer bool `json:"is_maintainer"`
		}](t, rec)
		require.Equal(t, want.isMaintainer, body.IsMaintainer,
			"POST /v1/teams gates on exactly this, so the frontend has to be told the same answer")
	}
}
