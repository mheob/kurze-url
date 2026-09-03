package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

// ErrNotMember means the caller has no team_member row for the team. It is
// answered with 404, not 403: a team's existence is not disclosed to people
// outside it, so team IDs cannot be enumerated.
var ErrNotMember = errors.New("authz: caller is not a member of the team")

// Membership is the caller's relationship to one team.
type Membership struct {
	TeamID uuid.UUID
	UserID uuid.UUID
	Role   Role
}

// Resolver loads a caller's membership. Implemented by QueryResolver against
// Postgres, and by fakes in tests.
type Resolver interface {
	Membership(ctx context.Context, teamID, userID uuid.UUID) (Membership, error)
}

type resolverKey struct{}

// WithResolver returns a context carrying the membership resolver. The /v1
// auth middleware installs it once per request.
func WithResolver(ctx context.Context, r Resolver) context.Context {
	return context.WithValue(ctx, resolverKey{}, r)
}

func resolverFromContext(ctx context.Context) (Resolver, bool) {
	r, ok := ctx.Value(resolverKey{}).(Resolver)
	return r, ok
}

// TeamPath carries the team ID every team-scoped operation takes in its path.
// It is exported and embedded by value in each scope because reflection cannot
// reliably set fields promoted through an unexported embedded struct.
type TeamPath struct {
	TeamID uuid.UUID `path:"team_id" doc:"The team this request operates on."`
}

// The four scopes below are what an operation embeds to declare the role it
// requires. Huma calls Resolve before the handler body runs, so a handler can
// never execute without the check having passed. Embedding one of these is the
// only way a team-scoped operation is allowed to reach team data;
// TestEveryOperationIsAccountedFor in internal/api fails the build if a new
// operation is registered that is neither authenticated-with-a-matrix-row
// nor named as deliberately public, which is the closest thing to a
// build-time guarantee that it embeds one of these scopes.

// ViewerScope is embedded by operations that any team member may call.
type ViewerScope struct {
	TeamPath
	member Membership
}

// EditorScope is embedded by operations that require at least the editor role.
type EditorScope struct {
	TeamPath
	member Membership
}

// AdminScope is embedded by operations that require at least the admin role.
type AdminScope struct {
	TeamPath
	member Membership
}

// OwnerScope is embedded by operations that require the owner role.
type OwnerScope struct {
	TeamPath
	member Membership
}

// Resolve loads and checks the caller's membership before the handler runs.
func (s *ViewerScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleViewer, &s.member)
}

// Resolve loads and checks the caller's membership before the handler runs.
func (s *EditorScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleEditor, &s.member)
}

// Resolve loads and checks the caller's membership before the handler runs.
func (s *AdminScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleAdmin, &s.member)
}

// Resolve loads and checks the caller's membership before the handler runs.
func (s *OwnerScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleOwner, &s.member)
}

// Member returns the membership Resolve loaded. Handlers read it instead of
// querying team_member a second time.
func (s *ViewerScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded. Handlers read it instead of
// querying team_member a second time.
func (s *EditorScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded. Handlers read it instead of
// querying team_member a second time.
func (s *AdminScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded. Handlers read it instead of
// querying team_member a second time.
func (s *OwnerScope) Member() Membership { return s.member }

// resolveScope is the team-path entry point: it guards the path parameter,
// then defers the actual decision to resolveMembership. The parameter is
// named "required" rather than "min" because "min" shadows a Go builtin.
func resolveScope(ctx huma.Context, teamID uuid.UUID, required Role, out *Membership) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every Resolver unconditionally, even when its own parameter
	// binding already failed — and if that binding failed, TeamID above is
	// left at its zero value (uuid.UUID.UnmarshalText leaves the receiver
	// untouched on error), not the malformed input. Worse, huma picks the
	// *last* error's status code when several are present, so this
	// resolver's error would otherwise silently overrule the binder's 422
	// and report a malformed team_id as a plain 404. Re-checking the raw
	// path value here keeps a malformed ID a 422 (its actual defect)
	// instead of masking it as "team not found".
	if raw := ctx.Param("team_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("team_id must be a valid UUID")}
		}
	}

	return resolveMembership(ctx, teamID, required, "team not found", out)
}

// resolveMembership is the whole authorization decision, shared by the
// team-path scopes and the entity scopes. notFound is the message used when
// the caller is not a member: the wording differs per route so a 404 never
// says "team not found" on a link route.
//
// It checks for verified claims itself. Callers check too — earlier, so that an
// unauthenticated request with a malformed path parameter is a 401 rather than
// a 422 — but correctness does not depend on their doing so.
//
// The membership travels out through out rather than the context because
// Resolve returns only errors — it cannot replace the context the handler
// receives — but it can mutate the input struct it is part of.
func resolveMembership(
	ctx huma.Context, teamID uuid.UUID, required Role, notFound string, out *Membership,
) []error {
	// The precondition — callers check claims first — is enforced here rather
	// than documented. Without it a caller that forgot would look up membership
	// for uuid.Nil, get ErrNotMember and answer 404: the wrong defect, and one
	// that hides an authentication bug behind a plausible-looking response.
	userID, ok := claimsUserID(ctx)
	if !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	resolver, found := resolverFromContext(ctx.Context())
	if !found {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know whether this caller belongs to the team.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	membership, err := resolver.Membership(ctx.Context(), teamID, userID)
	switch {
	case errors.Is(err, ErrNotMember):
		return []error{huma.Error404NotFound(notFound)}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve team membership")}
	}

	if !membership.Role.AtLeast(required) {
		return []error{huma.Error403Forbidden(
			fmt.Sprintf("this action requires the %s role or higher", required))}
	}

	*out = membership
	return nil
}

// claimsUserID reads the caller's user ID from the verified claims, reporting
// whether any were present. Both resolveScope's callers and the entity scopes
// check claims themselves before they get here; the ok flag is what lets
// resolveMembership enforce that rather than trust it.
func claimsUserID(ctx huma.Context) (uuid.UUID, bool) {
	claims, ok := auth.ClaimsFromContext(ctx.Context())
	if !ok {
		return uuid.Nil, false
	}
	return claims.UserID, true
}
