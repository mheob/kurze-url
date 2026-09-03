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

type fakeFolderResolver struct {
	folder authz.ResolvedFolder
	err    error
}

func (f fakeFolderResolver) Folder(context.Context, uuid.UUID) (authz.ResolvedFolder, error) {
	return f.folder, f.err
}

type folderEditorInput struct {
	authz.FolderEditorScope
}

// folderScopeCase wires one request through a registered operation whose input
// embeds FolderEditorScope, so the scope is exercised exactly as Huma runs it.
func folderScopeCase(
	t *testing.T, folders authz.FolderResolver, members authz.Resolver,
	userID uuid.UUID, folderID string,
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
		if folders != nil {
			inner = authz.WithFolderResolver(inner, folders)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/folders/{folder_id}",
	}, func(_ context.Context, _ *folderEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/folders/" + folderID)
}

func TestFolderScopeAllowsAnEditor(t *testing.T) {
	teamID, folderID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, folderID.String())

	// The probe handler returns an empty struct with no Body field, which Huma
	// answers 204 No Content, not 200 — same as linkScopeCase's equivalent
	// assertion, which checks "success" rather than an exact status.
	require.Less(t, resp.Code, 400, "body: %s", resp.Body.String())
}

func TestFolderScopeRefusesAViewerWith403(t *testing.T) {
	// A member of the owning team whose role is too low gets 403, not 404:
	// that caller already knows the folder exists.
	teamID, folderID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, folderID.String())

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func TestFolderScopeHidesAnotherTeamsFolderWith404(t *testing.T) {
	// A folder owned by another team and a folder that does not exist must be
	// indistinguishable, or folder IDs become probeable.
	folderID, userID := uuid.New(), uuid.New()

	foreign := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, folderID.String())

	missing := folderScopeCase(t,
		fakeFolderResolver{err: authz.ErrFolderNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		userID, folderID.String())

	require.Equal(t, http.StatusNotFound, foreign.Code)
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, missing.Body.String(), foreign.Body.String(),
		"a foreign folder and a missing one must be byte-identical")
}

func TestFolderScopeReturns422ForAMalformedID(t *testing.T) {
	// The membership resolver is configured to fail (ErrNotMember, which
	// would otherwise produce 404) so a resolver-produced error is actually
	// in play here. Without that, this test would pass whether or not the
	// folder_id re-parse guard exists at all: with both fakes succeeding,
	// nothing competes with Huma's own path-binder 422, and the binder's
	// status stands unopposed regardless of the guard.
	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code,
		"a malformed ID is a malformed request, not a missing folder")
}

func TestFolderScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	folderID := uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: uuid.New()}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, folderID.String())

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestFolderScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	folderID := uuid.New()

	resp := folderScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), folderID.String())

	require.Equal(t, http.StatusInternalServerError, resp.Code)
}
