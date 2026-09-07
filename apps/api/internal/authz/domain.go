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

// ErrDomainNotFound means no domain has that ID — as does a domain belonging
// to a team the caller is not in, or one with no team at all (the shared
// hostname, see ResolvedDomain). All three are indistinguishable from
// outside and answered with 404, so domain IDs cannot be probed.
var ErrDomainNotFound = errors.New("authz: domain does not exist")

// DomainPath carries the domain ID every domain-scoped operation takes in
// its path. Exported and embedded by value for the same reason TeamPath is:
// reflection cannot reliably set fields promoted through an unexported
// embedded struct.
type DomainPath struct {
	DomainID uuid.UUID `path:"domain_id" doc:"The domain this request operates on."`
}

// ResolvedDomain is what the scope loaded on the way to its decision.
type ResolvedDomain struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// DomainResolver loads the tenancy facts about a domain. Implemented by
// QueryDomainResolver against Postgres, and by fakes in tests.
type DomainResolver interface {
	Domain(ctx context.Context, domainID uuid.UUID) (ResolvedDomain, error)
}

type domainResolverKey struct{}

// WithDomainResolver returns a context carrying the domain resolver. The /v1
// auth middleware installs it once per request, beside the others.
func WithDomainResolver(ctx context.Context, r DomainResolver) context.Context {
	return context.WithValue(ctx, domainResolverKey{}, r)
}

func domainResolverFromContext(ctx context.Context) (DomainResolver, bool) {
	r, ok := ctx.Value(domainResolverKey{}).(DomainResolver)
	return r, ok
}

// QueryDomainResolver is the production DomainResolver: one primary-key
// lookup per domain-scoped request.
type QueryDomainResolver struct {
	queries *db.Queries
}

// NewQueryDomainResolver builds a DomainResolver backed by queries.
func NewQueryDomainResolver(queries *db.Queries) QueryDomainResolver {
	return QueryDomainResolver{queries: queries}
}

// Domain implements DomainResolver.
func (r QueryDomainResolver) Domain(ctx context.Context, domainID uuid.UUID) (ResolvedDomain, error) {
	row, err := r.queries.GetDomainScope(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedDomain{}, ErrDomainNotFound
	}
	if err != nil {
		return ResolvedDomain{}, fmt.Errorf("authz: load domain scope: %w", err)
	}
	return resolvedDomainFrom(row)
}

// resolvedDomainFrom maps a loaded domain row to the resolver's result. A
// null team_id is the instance's shared hostname. It belongs to no team, so
// no membership can authorize it, and no team may delete the hostname every
// other team's links are on. Reported as not-found rather than as a separate
// error: from outside, an unadministrable domain and a nonexistent one are
// the same thing.
func resolvedDomainFrom(row db.GetDomainScopeRow) (ResolvedDomain, error) {
	if row.TeamID == nil {
		return ResolvedDomain{}, ErrDomainNotFound
	}
	return ResolvedDomain{ID: row.ID, TeamID: *row.TeamID}, nil
}

// DomainAdminScope is embedded by domain operations. Admin, not editor:
// links, folders and tags are content, and a domain is the namespace that
// content lives in — losing it takes every link along. That belongs with
// member management.
type DomainAdminScope struct {
	DomainPath
	member Membership
	domain ResolvedDomain
}

// DomainViewerScope is the read-only sibling, for GET /v1/domains/{domain_id}.
// Separate rather than one scope with a role parameter, because the role a
// route requires belongs in its type where a reviewer reads it.
type DomainViewerScope struct {
	DomainPath
	member Membership
	domain ResolvedDomain
}

// Resolve loads the domain and checks the caller's membership before the
// handler runs.
func (s *DomainAdminScope) Resolve(ctx huma.Context) []error {
	return resolveDomainScope(ctx, s.DomainID, RoleAdmin, &s.member, &s.domain)
}

// Resolve loads the domain and checks the caller's membership before the
// handler runs.
func (s *DomainViewerScope) Resolve(ctx huma.Context) []error {
	return resolveDomainScope(ctx, s.DomainID, RoleViewer, &s.member, &s.domain)
}

// Member returns the membership Resolve loaded.
func (s *DomainAdminScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded.
func (s *DomainViewerScope) Member() Membership { return s.member }

// Domain returns the domain Resolve loaded.
func (s *DomainAdminScope) Domain() ResolvedDomain { return s.domain }

// Domain returns the domain Resolve loaded.
func (s *DomainViewerScope) Domain() ResolvedDomain { return s.domain }

// resolveDomainScope turns a domain ID into an authorization decision: who is
// calling, which team owns the domain, and whether that caller's role in
// that team is enough. The team is discovered here rather than taken from
// the path, which is the whole reason this scope exists.
func resolveDomainScope(
	ctx huma.Context, domainID uuid.UUID, required Role, member *Membership, out *ResolvedDomain,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed domain_id would otherwise be reported as a plain 404 — the
	// wrong defect. Same guard, same reason, as the one in resolveTagScope.
	if raw := ctx.Param("domain_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("domain_id must be a valid UUID")}
		}
	}

	resolver, ok := domainResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this domain.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Domain(ctx.Context(), domainID)
	switch {
	case errors.Is(err, ErrDomainNotFound):
		return []error{huma.Error404NotFound("domain not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the domain")}
	}

	// A non-member gets the same 404 a missing domain gets. An insufficient
	// role gets 403: that caller already knows the domain exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "domain not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
