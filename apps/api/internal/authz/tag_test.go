package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

type fakeTagResolver struct {
	tag authz.ResolvedTag
	err error
}

func (f fakeTagResolver) Tag(context.Context, uuid.UUID) (authz.ResolvedTag, error) {
	return f.tag, f.err
}

type tagEditorInput struct {
	authz.TagEditorScope
}

// tagScopeCase wires one request through a registered operation whose input
// embeds TagEditorScope, so the scope is exercised exactly as Huma runs it.
func tagScopeCase(
	t *testing.T, tags authz.TagResolver, members authz.Resolver,
	userID uuid.UUID, tagID string,
) *httptest.ResponseRecorder {
	t.Helper()

	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if userID != uuid.Nil {
			inner = auth.WithClaims(inner, auth.Claims{UserID: userID})
		}
		if members != nil {
			inner = authz.WithResolver(inner, members)
		}
		if tags != nil {
			inner = authz.WithTagResolver(inner, tags)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/tags/{tag_id}",
	}, func(_ context.Context, _ *tagEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/tags/" + tagID)
}

func TestTagScopeAllowsAnEditor(t *testing.T) {
	teamID, tagID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, tagID.String())

	// The probe handler returns an empty struct, which Huma answers with 204,
	// so assert "not an error" the way link_test.go does rather than an exact
	// 200.
	require.Less(t, resp.Code, 400, "body: %s", resp.Body.String())
}

func TestTagScopeRefusesAViewerWith403(t *testing.T) {
	teamID, tagID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, tagID.String())

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func TestTagScopeHidesAnotherTeamsTagWith404(t *testing.T) {
	tagID, userID := uuid.New(), uuid.New()

	foreign := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, tagID.String())

	missing := tagScopeCase(t,
		fakeTagResolver{err: authz.ErrTagNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		userID, tagID.String())

	require.Equal(t, http.StatusNotFound, foreign.Code)
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, missing.Body.String(), foreign.Body.String(),
		"a foreign tag and a missing one must be byte-identical")
}

func TestTagScopeReturns422ForAMalformedID(t *testing.T) {
	// The membership resolver is configured to fail (ErrNotMember, which
	// would otherwise produce 404) so a resolver-produced error is actually
	// in play here. Without that, this test would pass whether or not the
	// tag_id re-parse guard exists at all: with both fakes succeeding,
	// nothing competes with Huma's own path-binder 422, and the binder's
	// status stands unopposed regardless of the guard.
	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code,
		"a malformed ID is a malformed request, not a missing tag")
}

func TestTagScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	tagID := uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: uuid.New()}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, tagID.String())

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestTagScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	resp := tagScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusInternalServerError, resp.Code)
}
