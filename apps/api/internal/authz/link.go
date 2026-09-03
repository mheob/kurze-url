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

// ErrLinkNotFound means no link has that ID. It is answered with 404 — as is a
// link belonging to a team the caller is not in, so the two are
// indistinguishable from outside and link IDs cannot be probed.
var ErrLinkNotFound = errors.New("authz: link does not exist")

// LinkPath carries the link ID every link-scoped operation takes in its path.
// Exported and embedded by value for the same reason TeamPath is: reflection
// cannot reliably set fields promoted through an unexported embedded struct.
type LinkPath struct {
	LinkID uuid.UUID `path:"link_id" doc:"The link this request operates on."`
}

// ResolvedLink is what the scope loaded on the way to its decision. Handlers
// read it instead of querying the link a second time.
type ResolvedLink struct {
	ID       uuid.UUID
	TeamID   uuid.UUID
	DomainID uuid.UUID
	Hostname string
	Slug     string
}

// LinkResolver loads the tenancy facts about a link. Implemented by
// QueryLinkResolver against Postgres, and by fakes in tests.
type LinkResolver interface {
	Link(ctx context.Context, linkID uuid.UUID) (ResolvedLink, error)
}

type linkResolverKey struct{}

// WithLinkResolver returns a context carrying the link resolver. The /v1 auth
// middleware installs it once per request, beside the membership resolver.
func WithLinkResolver(ctx context.Context, r LinkResolver) context.Context {
	return context.WithValue(ctx, linkResolverKey{}, r)
}

func linkResolverFromContext(ctx context.Context) (LinkResolver, bool) {
	r, ok := ctx.Value(linkResolverKey{}).(LinkResolver)
	return r, ok
}

// QueryLinkResolver is the production LinkResolver: one primary-key lookup
// joined to domain, per link-scoped request.
type QueryLinkResolver struct {
	queries *db.Queries
}

// NewQueryLinkResolver builds a LinkResolver backed by queries.
func NewQueryLinkResolver(queries *db.Queries) QueryLinkResolver {
	return QueryLinkResolver{queries: queries}
}

// Link implements LinkResolver.
func (r QueryLinkResolver) Link(ctx context.Context, linkID uuid.UUID) (ResolvedLink, error) {
	row, err := r.queries.GetLinkScope(ctx, linkID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedLink{}, ErrLinkNotFound
	}
	if err != nil {
		return ResolvedLink{}, fmt.Errorf("authz: load link scope: %w", err)
	}
	return ResolvedLink{
		ID:       row.ID,
		TeamID:   row.TeamID,
		DomainID: row.DomainID,
		Hostname: row.Hostname,
		Slug:     row.Slug,
	}, nil
}

// LinkViewerScope is embedded by link operations any team member may call.
type LinkViewerScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

// LinkEditorScope is embedded by link operations requiring at least editor.
type LinkEditorScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

// Resolve loads the link and checks the caller's membership before the handler runs.
func (s *LinkViewerScope) Resolve(ctx huma.Context) []error {
	return resolveLinkScope(ctx, s.LinkID, RoleViewer, &s.member, &s.link)
}

// Resolve loads the link and checks the caller's membership before the handler runs.
func (s *LinkEditorScope) Resolve(ctx huma.Context) []error {
	return resolveLinkScope(ctx, s.LinkID, RoleEditor, &s.member, &s.link)
}

// Member returns the membership Resolve loaded.
func (s *LinkViewerScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded.
func (s *LinkEditorScope) Member() Membership { return s.member }

// Link returns the link Resolve loaded.
func (s *LinkViewerScope) Link() ResolvedLink { return s.link }

// Link returns the link Resolve loaded.
func (s *LinkEditorScope) Link() ResolvedLink { return s.link }

// resolveLinkScope turns a link ID into an authorization decision: who is
// calling, which team owns the link, and whether that caller's role in that
// team is enough. The team is discovered here rather than taken from the path,
// which is the whole reason this scope exists.
func resolveLinkScope(
	ctx huma.Context, linkID uuid.UUID, required Role, member *Membership, out *ResolvedLink,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed link_id would otherwise be reported as a plain 404 — the wrong
	// defect — so the raw value is re-checked here. Same guard, same reason, as
	// the team_id one in resolveScope.
	if raw := ctx.Param("link_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("link_id must be a valid UUID")}
		}
	}

	resolver, ok := linkResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this link.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Link(ctx.Context(), linkID)
	switch {
	case errors.Is(err, ErrLinkNotFound):
		return []error{huma.Error404NotFound("link not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the link")}
	}

	// A non-member gets the same 404 a missing link gets. An insufficient role
	// gets 403: that caller already knows the link exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "link not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
