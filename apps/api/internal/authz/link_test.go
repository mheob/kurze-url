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

type fakeLinkResolver struct {
	link authz.ResolvedLink
	err  error
}

func (f fakeLinkResolver) Link(context.Context, uuid.UUID) (authz.ResolvedLink, error) {
	return f.link, f.err
}

type fakeMembershipResolver struct {
	membership authz.Membership
	err        error
}

func (f fakeMembershipResolver) Membership(
	context.Context, uuid.UUID, uuid.UUID,
) (authz.Membership, error) {
	return f.membership, f.err
}

type linkEditorInput struct {
	authz.LinkEditorScope
}

// linkScopeCase wires one request through a registered operation whose input
// embeds LinkEditorScope, so the scope is exercised exactly as Huma runs it.
func linkScopeCase(
	t *testing.T, links authz.LinkResolver, members authz.Resolver, userID uuid.UUID, linkID string,
) *httptest.ResponseRecorder { // humatest.TestAPI.Get returns this
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
		if links != nil {
			inner = authz.WithLinkResolver(inner, links)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/links/{link_id}",
	}, func(_ context.Context, _ *linkEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/links/" + linkID)
}

func TestLinkScopeAllowsAnEditor(t *testing.T) {
	linkID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, linkID.String())

	require.Less(t, rec.Code, 400, "body: %s", rec.Body.String())
}

func TestLinkScopeRefusesAViewerWith403(t *testing.T) {
	linkID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, linkID.String())

	require.Equal(t, http.StatusForbidden, rec.Code,
		"a member who may see the link but not change it learns nothing new from a 403")
}

func TestLinkScopeHidesAnotherTeamsLinkWith404(t *testing.T) {
	linkID, userID := uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, linkID.String())

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to probe link IDs for existence")
	require.NotContains(t, rec.Body.String(), "team",
		"the 404 on a link route must be worded about the link")
}

func TestLinkScopeReturns404ForAMissingLink(t *testing.T) {
	rec := linkScopeCase(t,
		fakeLinkResolver{err: authz.ErrLinkNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLinkScopeReturns422ForAMalformedID(t *testing.T) {
	// The membership resolver is configured to fail (ErrNotMember, which
	// would otherwise produce 404) so a resolver-produced error is actually
	// in play here. Without that, this test would pass whether or not the
	// link_id re-parse guard exists at all: with both fakes succeeding,
	// nothing competes with Huma's own path-binder 422, and the binder's
	// status stands unopposed regardless of the guard.
	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a malformed ID is a malformed request, not a missing link")
}

func TestLinkScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	rec := linkScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"without a resolver there is no way to know whose link this is; refusing is the only safe answer")
}

func TestLinkScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, uuid.New().String())

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
