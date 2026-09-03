// Package authz owns tenancy authorization: the role ordering and the scope
// types that every team-scoped operation embeds. Postgres enforces nothing
// about tenancy — there is no RLS and the API connects with a service role —
// so this package is the only thing standing between one team's data and
// another's.
package authz

import (
	"errors"
	"fmt"
)

// Role mirrors the team_member.role check constraint.
type Role string

// The four schema roles, ordered from least to most privileged.
const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// ErrInvalidRole is returned by ParseRole for anything outside the four
// schema roles.
var ErrInvalidRole = errors.New("authz: invalid role")

// rank orders the roles. A role absent from this map ranks 0 and therefore
// satisfies no requirement, which is the safe direction to fail.
var rank = map[Role]int{
	RoleViewer: 1,
	RoleEditor: 2,
	RoleAdmin:  3,
	RoleOwner:  4,
}

// ParseRole converts a request or database value into a Role.
func ParseRole(s string) (Role, error) {
	role := Role(s)
	if _, ok := rank[role]; !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, s)
	}
	return role, nil
}

// AtLeast reports whether r is required or higher.
func (r Role) AtLeast(required Role) bool {
	return rank[r] >= rank[required] && rank[r] > 0
}

func (r Role) String() string { return string(r) }
