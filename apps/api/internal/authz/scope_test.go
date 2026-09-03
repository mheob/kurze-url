package authz_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// stubResolver answers with a fixed role, or ErrNotMember when role is empty.
type stubResolver struct {
	role authz.Role
	err  error
}

func (s stubResolver) Membership(_ context.Context, teamID, userID uuid.UUID) (authz.Membership, error) {
	if s.err != nil {
		return authz.Membership{}, s.err
	}
	return authz.Membership{TeamID: teamID, UserID: userID, Role: s.role}, nil
}

// scopeTestServer mounts one operation whose input embeds an AdminScope and
// which echoes the resolved role, so a test can assert both the status code
// and that the handler saw the membership.
func scopeTestServer(t *testing.T, resolver authz.Resolver, claims *auth.Claims) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "1.0.0"))

	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if claims != nil {
			inner = auth.WithClaims(inner, *claims)
		}
		if resolver != nil {
			inner = authz.WithResolver(inner, resolver)
		}
		next(huma.WithContext(ctx, inner))
	})

	type input struct {
		authz.AdminScope
	}
	type output struct {
		Body struct {
			Role string `json:"role"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "scope-probe",
		Method:      http.MethodGet,
		Path:        "/teams/{team_id}/probe",
	}, func(_ context.Context, in *input) (*output, error) {
		out := &output{}
		out.Body.Role = in.Member().Role.String()
		return out, nil
	})

	return mux
}

func get(t *testing.T, handler http.Handler, teamID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/teams/"+teamID.String()+"/probe", nil))
	return rec
}

func TestScopeAllowsASufficientRoleAndExposesTheMembership(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New(), Email: "member@example.org"}
	handler := scopeTestServer(t, stubResolver{role: authz.RoleOwner}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusOK, rec.Code)
	// huma.DefaultConfig injects a "$schema" field into every struct response
	// body (see huma's SchemaLinkTransformer), so this checks for the role
	// field rather than an exact JSON match — same convention plan 1 uses in
	// internal/api/v1_test.go for the same reason.
	require.Contains(t, rec.Body.String(), `"role":"owner"`,
		"the handler must see the membership Resolve loaded, not query it again")
}

func TestScopeRejectsAnInsufficientRoleWith403(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{role: authz.RoleEditor}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}

func TestScopeHidesTheTeamFromANonMemberWith404(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{err: authz.ErrNotMember}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to tell an existing team from a missing one")
}

func TestScopeRejectsAnUnauthenticatedCallerWith401(t *testing.T) {
	handler := scopeTestServer(t, stubResolver{role: authz.RoleOwner}, nil)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestScopeFailsClosedWhenNoResolverIsInstalled(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, nil, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a misconfigured server must refuse the request, never serve it unauthorized")
}

func TestScopeSurfacesAResolverFailureAs500(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{err: errors.New("database is down")}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
