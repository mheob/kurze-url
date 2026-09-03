package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// QueryResolver is the production Resolver: one indexed primary-key lookup
// against team_member per team-scoped request.
type QueryResolver struct {
	queries *db.Queries
}

// NewQueryResolver builds a Resolver backed by queries.
func NewQueryResolver(queries *db.Queries) QueryResolver {
	return QueryResolver{queries: queries}
}

// Membership implements Resolver.
func (r QueryResolver) Membership(
	ctx context.Context, teamID, userID uuid.UUID,
) (Membership, error) {
	row, err := r.queries.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: teamID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotMember
	}
	if err != nil {
		return Membership{}, fmt.Errorf("authz: load membership: %w", err)
	}

	role, err := ParseRole(row.Role)
	if err != nil {
		// The check constraint should make this impossible; if it happens,
		// refusing is safer than guessing a role.
		return Membership{}, fmt.Errorf("authz: team %s member %s: %w", teamID, userID, err)
	}

	return Membership{TeamID: row.TeamID, UserID: row.UserID, Role: role}, nil
}
