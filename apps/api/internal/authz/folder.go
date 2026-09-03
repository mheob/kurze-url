package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrFolderNotFound means no folder has that ID. It is answered with 404 — as
// is a folder belonging to a team the caller is not in, so the two are
// indistinguishable from outside and folder IDs cannot be probed.
var ErrFolderNotFound = errors.New("authz: folder does not exist")

// FolderPath carries the folder ID every folder-scoped operation takes in its
// path. Exported and embedded by value for the same reason TeamPath is:
// reflection cannot reliably set fields promoted through an unexported
// embedded struct.
type FolderPath struct {
	FolderID uuid.UUID `path:"folder_id" doc:"The folder this request operates on."`
}

// ResolvedFolder is what the scope loaded on the way to its decision.
type ResolvedFolder struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// FolderResolver loads the tenancy facts about a folder. Implemented by
// QueryFolderResolver against Postgres, and by fakes in tests.
type FolderResolver interface {
	Folder(ctx context.Context, folderID uuid.UUID) (ResolvedFolder, error)
}

type folderResolverKey struct{}

// WithFolderResolver returns a context carrying the folder resolver. The /v1
// auth middleware installs it once per request, beside the others.
func WithFolderResolver(ctx context.Context, r FolderResolver) context.Context {
	return context.WithValue(ctx, folderResolverKey{}, r)
}

func folderResolverFromContext(ctx context.Context) (FolderResolver, bool) {
	r, ok := ctx.Value(folderResolverKey{}).(FolderResolver)
	return r, ok
}

// QueryFolderResolver is the production FolderResolver: one primary-key lookup
// per folder-scoped request.
type QueryFolderResolver struct {
	queries *db.Queries
}

// NewQueryFolderResolver builds a FolderResolver backed by queries.
func NewQueryFolderResolver(queries *db.Queries) QueryFolderResolver {
	return QueryFolderResolver{queries: queries}
}

// Folder implements FolderResolver.
func (r QueryFolderResolver) Folder(
	ctx context.Context, folderID uuid.UUID,
) (ResolvedFolder, error) {
	row, err := r.queries.GetFolderScope(ctx, folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedFolder{}, ErrFolderNotFound
	}
	if err != nil {
		return ResolvedFolder{}, fmt.Errorf("authz: load folder scope: %w", err)
	}
	return ResolvedFolder{ID: row.ID, TeamID: row.TeamID}, nil
}

// FolderEditorScope is embedded by folder operations requiring at least
// editor. There is no FolderViewerScope: folders have no read-one endpoint —
// reads go through the team-scoped list, which uses ViewerScope — so a viewer
// scope here would be a resolver with no caller.
type FolderEditorScope struct {
	FolderPath
	member Membership
	folder ResolvedFolder
}

// Resolve loads the folder and checks the caller's membership before the
// handler runs.
func (s *FolderEditorScope) Resolve(ctx huma.Context) []error {
	return resolveFolderScope(ctx, s.FolderID, RoleEditor, &s.member, &s.folder)
}

// Member returns the membership Resolve loaded.
func (s *FolderEditorScope) Member() Membership { return s.member }

// Folder returns the folder Resolve loaded.
func (s *FolderEditorScope) Folder() ResolvedFolder { return s.folder }

// resolveFolderScope turns a folder ID into an authorization decision: who is
// calling, which team owns the folder, and whether that caller's role in that
// team is enough. The team is discovered here rather than taken from the path,
// which is the whole reason this scope exists.
func resolveFolderScope(
	ctx huma.Context, folderID uuid.UUID, required Role, member *Membership, out *ResolvedFolder,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed folder_id would otherwise be reported as a plain 404 — the
	// wrong defect. Same guard, same reason, as the one in resolveLinkScope.
	if raw := ctx.Param("folder_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("folder_id must be a valid UUID")}
		}
	}

	resolver, ok := folderResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this folder.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Folder(ctx.Context(), folderID)
	switch {
	case errors.Is(err, ErrFolderNotFound):
		return []error{huma.Error404NotFound("folder not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the folder")}
	}

	// A non-member gets the same 404 a missing folder gets. An insufficient
	// role gets 403: that caller already knows the folder exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "folder not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
