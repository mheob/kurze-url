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

// ErrTagNotFound means no tag has that ID. It is answered with 404 — as is a
// tag belonging to a team the caller is not in, so the two are
// indistinguishable from outside and tag IDs cannot be probed.
var ErrTagNotFound = errors.New("authz: tag does not exist")

// TagPath carries the tag ID every tag-scoped operation takes in its path.
// Exported and embedded by value for the same reason TeamPath is: reflection
// cannot reliably set fields promoted through an unexported embedded struct.
type TagPath struct {
	TagID uuid.UUID `path:"tag_id" doc:"The tag this request operates on."`
}

// ResolvedTag is what the scope loaded on the way to its decision.
type ResolvedTag struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// TagResolver loads the tenancy facts about a tag. Implemented by
// QueryTagResolver against Postgres, and by fakes in tests.
type TagResolver interface {
	Tag(ctx context.Context, tagID uuid.UUID) (ResolvedTag, error)
}

type tagResolverKey struct{}

// WithTagResolver returns a context carrying the tag resolver. The /v1 auth
// middleware installs it once per request, beside the others.
func WithTagResolver(ctx context.Context, r TagResolver) context.Context {
	return context.WithValue(ctx, tagResolverKey{}, r)
}

func tagResolverFromContext(ctx context.Context) (TagResolver, bool) {
	r, ok := ctx.Value(tagResolverKey{}).(TagResolver)
	return r, ok
}

// QueryTagResolver is the production TagResolver: one primary-key lookup per
// tag-scoped request.
type QueryTagResolver struct {
	queries *db.Queries
}

// NewQueryTagResolver builds a TagResolver backed by queries.
func NewQueryTagResolver(queries *db.Queries) QueryTagResolver {
	return QueryTagResolver{queries: queries}
}

// Tag implements TagResolver.
func (r QueryTagResolver) Tag(ctx context.Context, tagID uuid.UUID) (ResolvedTag, error) {
	row, err := r.queries.GetTagScope(ctx, tagID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedTag{}, ErrTagNotFound
	}
	if err != nil {
		return ResolvedTag{}, fmt.Errorf("authz: load tag scope: %w", err)
	}
	return ResolvedTag{ID: row.ID, TeamID: row.TeamID}, nil
}

// TagEditorScope is embedded by tag operations requiring at least editor.
// There is no TagViewerScope, for the same reason there is no
// FolderViewerScope: tags have no read-one endpoint — reads go through the
// team-scoped list, which uses ViewerScope — so a viewer scope here would be a
// resolver with no caller.
type TagEditorScope struct {
	TagPath
	member Membership
	tag    ResolvedTag
}

// Resolve loads the tag and checks the caller's membership before the
// handler runs.
func (s *TagEditorScope) Resolve(ctx huma.Context) []error {
	return resolveTagScope(ctx, s.TagID, RoleEditor, &s.member, &s.tag)
}

// Member returns the membership Resolve loaded.
func (s *TagEditorScope) Member() Membership { return s.member }

// Tag returns the tag Resolve loaded.
func (s *TagEditorScope) Tag() ResolvedTag { return s.tag }

// resolveTagScope turns a tag ID into an authorization decision: who is
// calling, which team owns the tag, and whether that caller's role in that
// team is enough. The team is discovered here rather than taken from the path,
// which is the whole reason this scope exists.
func resolveTagScope(
	ctx huma.Context, tagID uuid.UUID, required Role, member *Membership, out *ResolvedTag,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed tag_id would otherwise be reported as a plain 404 — the wrong
	// defect. Same guard, same reason, as the one in resolveFolderScope.
	if raw := ctx.Param("tag_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("tag_id must be a valid UUID")}
		}
	}

	resolver, ok := tagResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this tag.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Tag(ctx.Context(), tagID)
	switch {
	case errors.Is(err, ErrTagNotFound):
		return []error{huma.Error404NotFound("tag not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the tag")}
	}

	// A non-member gets the same 404 a missing tag gets. An insufficient role
	// gets 403: that caller already knows the tag exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "tag not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
