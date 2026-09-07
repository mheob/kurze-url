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

type fakeDomainResolver struct {
	domain authz.ResolvedDomain
	err    error
}

func (f fakeDomainResolver) Domain(context.Context, uuid.UUID) (authz.ResolvedDomain, error) {
	return f.domain, f.err
}

type domainAdminInput struct {
	authz.DomainAdminScope
}

type domainViewerInput struct {
	authz.DomainViewerScope
}

// domainScopeCase wires one request through a registered operation whose
// input embeds either DomainAdminScope or DomainViewerScope (chosen by
// admin), so the scope is exercised exactly as Huma runs it.
func domainScopeCase(
	t *testing.T, admin bool, domains authz.DomainResolver, members authz.Resolver,
	userID uuid.UUID, domainID string,
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
		if domains != nil {
			inner = authz.WithDomainResolver(inner, domains)
		}
		next(huma.WithContext(ctx, inner))
	})

	if admin {
		huma.Register(api, huma.Operation{
			OperationID: "probe-admin",
			Method:      http.MethodGet,
			Path:        "/domains/{domain_id}",
		}, func(_ context.Context, _ *domainAdminInput) (*struct{}, error) {
			return &struct{}{}, nil
		})
	} else {
		huma.Register(api, huma.Operation{
			OperationID: "probe-viewer",
			Method:      http.MethodGet,
			Path:        "/domains/{domain_id}",
		}, func(_ context.Context, _ *domainViewerInput) (*struct{}, error) {
			return &struct{}{}, nil
		})
	}

	return api.Get("/domains/" + domainID)
}

func TestDomainAdminScope(t *testing.T) {
	t.Run("a stranger gets 404, not 403", func(t *testing.T) {
		domainID, userID := uuid.New(), uuid.New()

		resp := domainScopeCase(t, true,
			fakeDomainResolver{domain: authz.ResolvedDomain{ID: domainID, TeamID: uuid.New()}},
			fakeMembershipResolver{err: authz.ErrNotMember},
			userID, domainID.String())

		require.Equal(t, http.StatusNotFound, resp.Code,
			"a non-member must not be able to probe domain IDs for existence")
	})

	t.Run("a member below admin gets 403", func(t *testing.T) {
		domainID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

		resp := domainScopeCase(t, true,
			fakeDomainResolver{domain: authz.ResolvedDomain{ID: domainID, TeamID: teamID}},
			fakeMembershipResolver{membership: authz.Membership{
				TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
			}},
			userID, domainID.String())

		require.Equal(t, http.StatusForbidden, resp.Code,
			"an editor already knows the domain exists, so hiding it behind a 404 would be theatre")
	})

	t.Run("an admin is allowed through", func(t *testing.T) {
		domainID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

		resp := domainScopeCase(t, true,
			fakeDomainResolver{domain: authz.ResolvedDomain{ID: domainID, TeamID: teamID}},
			fakeMembershipResolver{membership: authz.Membership{
				TeamID: teamID, UserID: userID, Role: authz.RoleAdmin,
			}},
			userID, domainID.String())

		require.Less(t, resp.Code, 400, "body: %s", resp.Body.String())
	})

	t.Run("the shared domain is not administrable", func(t *testing.T) {
		domainID, userID := uuid.New(), uuid.New()

		// A resolver reporting ErrDomainNotFound is the shared-hostname case:
		// domain.team_id is null there, and QueryDomainResolver.Domain turns
		// that null into ErrDomainNotFound rather than the zero UUID, because
		// no membership check could ever match a team no domain belongs to.
		resp := domainScopeCase(t, true,
			fakeDomainResolver{err: authz.ErrDomainNotFound},
			fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
			userID, domainID.String())

		require.Equal(t, http.StatusNotFound, resp.Code,
			"a null-team domain must read as not-found, the same as one that does not exist")
	})

	t.Run("a malformed domain_id is 422, not 404", func(t *testing.T) {
		// The membership resolver is configured to fail (ErrNotMember, which
		// would otherwise produce 404) so a resolver-produced error is
		// actually in play here. Without that, this test would pass whether
		// or not the domain_id re-parse guard exists at all: with both fakes
		// succeeding, nothing competes with Huma's own path-binder 422, and
		// the binder's status stands unopposed regardless of the guard.
		resp := domainScopeCase(t, true,
			fakeDomainResolver{domain: authz.ResolvedDomain{}},
			fakeMembershipResolver{err: authz.ErrNotMember},
			uuid.New(), "not-a-uuid")

		require.Equal(t, http.StatusUnprocessableEntity, resp.Code,
			"a malformed ID is a malformed request, not a missing domain")
	})
}

func TestDomainViewerScope(t *testing.T) {
	t.Run("a viewer passes, the same viewer fails DomainAdminScope", func(t *testing.T) {
		domainID, teamID, userID := uuid.New(), uuid.New(), uuid.New()
		domains := fakeDomainResolver{domain: authz.ResolvedDomain{ID: domainID, TeamID: teamID}}
		members := fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}}

		viewerResp := domainScopeCase(t, false, domains, members, userID, domainID.String())
		require.Less(t, viewerResp.Code, 400, "body: %s", viewerResp.Body.String())

		adminResp := domainScopeCase(t, true, domains, members, userID, domainID.String())
		require.Equal(t, http.StatusForbidden, adminResp.Code,
			"DomainViewerScope and DomainAdminScope must be genuinely distinct, not one copied over the other")
	})
}
