package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/supabase"
)

// Member is a team membership as the API exposes it. Email comes from
// auth.users; there is no display name, because public.profile is an open
// question, not a decision.
type Member struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ListMembersInput is GET /v1/teams/{team_id}/members. Any team member may
// call it — listing members is a viewer-level right.
type ListMembersInput struct {
	authz.ViewerScope
	PageParams
}

// ListMembersOutput is the body of GET /v1/teams/{team_id}/members.
type ListMembersOutput struct {
	Body Page[Member]
}

// AddMemberInput is POST /v1/teams/{team_id}/members. Inviting or adding a
// member requires at least the admin role.
type AddMemberInput struct {
	authz.AdminScope
	Body struct {
		Email string `json:"email" format:"email" doc:"The person's email address."`
		Role  string `json:"role" enum:"viewer,editor,admin,owner" doc:"The role to grant."`
	}
}

// MemberOutput is the body shared by every single-member operation.
type MemberOutput struct {
	Body Member
}

// UpdateMemberInput is PATCH /v1/teams/{team_id}/members/{user_id}. Changing
// a role requires at least the admin role; the handler enforces the tighter
// owner-only rules itself.
type UpdateMemberInput struct {
	authz.AdminScope
	UserID uuid.UUID `path:"user_id" doc:"The member to change."`
	Body   struct {
		Role string `json:"role" enum:"viewer,editor,admin,owner"`
	}
}

// RemoveMemberInput is DELETE /v1/teams/{team_id}/members/{user_id}. Removing
// a member requires at least the admin role; the handler refuses an admin
// removing an owner and refuses removing the team's last owner.
type RemoveMemberInput struct {
	authz.AdminScope
	UserID uuid.UUID `path:"user_id" doc:"The member to remove."`
}

func (d Deps) registerMembers(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/members",
		Summary:     "List a team's members",
		Tags:        []string{"Members"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listMembers)

	huma.Register(api, huma.Operation{
		OperationID:   "add-team-member",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/members",
		Summary:       "Invite or add a member",
		Description:   "An address without an account is invited by email; an address that already has one is added directly and sees the team on next login.",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.addMember)

	huma.Register(api, huma.Operation{
		OperationID:   "update-team-member",
		Method:        http.MethodPatch,
		Path:          "/v1/teams/{team_id}/members/{user_id}",
		Summary:       "Change a member's role",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.updateMember)

	huma.Register(api, huma.Operation{
		OperationID:   "remove-team-member",
		Method:        http.MethodDelete,
		Path:          "/v1/teams/{team_id}/members/{user_id}",
		Summary:       "Remove a member",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.removeMember)
}

func (d Deps) listMembers(ctx context.Context, in *ListMembersInput) (*ListMembersOutput, error) {
	member := in.Member()

	rows, err := d.Queries.ListTeamMembers(ctx, db.ListTeamMembersParams{
		TeamID: member.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list team members", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not list the team's members")
	}

	items := make([]Member, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Member{
			UserID:    row.UserID,
			Email:     derefString(row.Email),
			Role:      row.Role,
			CreatedAt: row.CreatedAt,
		})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountTeamMembers(ctx, member.TeamID)
		if err != nil {
			d.Log.Error("count team members", "error", err, "team_id", in.TeamID)
			return nil, huma.Error500InternalServerError("could not list the team's members")
		}
	}

	return &ListMembersOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

// addMember is POST /v1/teams/{team_id}/members. An address without an
// account is invited by email; an address that already has one is added to
// the team directly, with no email — that person simply sees the new team
// on next login. There is no notification for that second path; that is a
// known gap, not a bug, because there is no notification system yet.
func (d Deps) addMember(ctx context.Context, in *AddMemberInput) (*MemberOutput, error) {
	actor := in.Member()

	role, err := authz.ParseRole(in.Body.Role)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("unknown role")
	}
	// Only an owner may create another owner. The scope has already proved the
	// caller is at least an admin.
	if role == authz.RoleOwner && !actor.Role.AtLeast(authz.RoleOwner) {
		return nil, huma.Error403Forbidden("granting the owner role requires the owner role")
	}

	// Invitations spend real email quota, so cap them per team.
	allowed, _, err := d.Cache.Allow(ctx, "rl:invite:"+in.TeamID.String(),
		d.Config.InviteRateLimitPerHour, time.Hour)
	if err != nil {
		d.Log.Error("invite rate limit", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not check the invitation rate limit")
	}
	if !allowed {
		return nil, huma.Error429TooManyRequests("too many invitations for this team; try again later")
	}

	userID, invited, err := d.resolveInvitee(ctx, in)
	if err != nil {
		return nil, err
	}

	if _, err := d.Queries.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: actor.TeamID,
		UserID: userID,
	}); err == nil {
		return nil, huma.Error409Conflict("that person is already a member of this team")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		d.Log.Error("check existing membership", "error", err)
		return nil, huma.Error500InternalServerError("could not add the member")
	}

	action := audit.ActionMemberAdded
	if invited {
		action = audit.ActionMemberInvited
	}

	var createdAt time.Time
	if err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		created, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{
			TeamID: actor.TeamID,
			UserID: userID,
			Role:   role.String(),
		})
		if err != nil {
			return err
		}
		createdAt = created

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      actor.TeamID,
			ActorUserID: actor.UserID,
			Action:      action,
			EntityType:  audit.EntityTeamMember,
			EntityID:    userID,
			Metadata:    map[string]any{"email": in.Body.Email, "role": role.String()},
		})
	}); err != nil {
		d.Log.Error("add team member", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not add the member")
	}

	return &MemberOutput{Body: Member{
		UserID:    userID,
		Email:     in.Body.Email,
		Role:      role.String(),
		CreatedAt: createdAt,
	}}, nil
}

// resolveInvitee returns the user ID to add and whether an invitation email
// was sent. The invite is deliberately performed BEFORE the transaction: an
// email cannot be rolled back, so a failure here must leave no membership.
func (d Deps) resolveInvitee(ctx context.Context, in *AddMemberInput) (uuid.UUID, bool, error) {
	userID, err := d.Queries.GetUserIDByEmail(ctx, in.Body.Email)
	switch {
	case err == nil:
		return userID, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		d.Log.Error("look up invitee", "error", err)
		return uuid.Nil, false, huma.Error500InternalServerError("could not look up that address")
	}

	if d.Admin == nil {
		return uuid.Nil, false, huma.Error503ServiceUnavailable(
			"invitations are not configured on this instance")
	}

	invitedID, err := d.Admin.InviteUser(ctx, in.Body.Email, map[string]any{
		"team_id": in.TeamID.String(),
		"role":    in.Body.Role,
	})
	switch {
	case errors.Is(err, supabase.ErrUserExists):
		// Raced with another signup, or Supabase knows an account the SQL
		// lookup missed. Re-read and fall through to the direct-add path.
		existing, lookupErr := d.Queries.GetUserIDByEmail(ctx, in.Body.Email)
		if lookupErr != nil {
			d.Log.Error("invitee exists but could not be looked up", "error", lookupErr)
			return uuid.Nil, false, huma.Error502BadGateway("could not resolve that address")
		}
		return existing, false, nil
	case err != nil:
		d.Log.Error("send invitation", "error", err, "team_id", in.TeamID)
		return uuid.Nil, false, huma.Error502BadGateway("could not send the invitation")
	}

	return invitedID, true, nil
}

// updateMember is PATCH /v1/teams/{team_id}/members/{user_id}. It enforces
// the three rules that keep the role hierarchy from collapsing: granting or
// revoking the owner role requires the owner role, and the last owner can
// never be demoted. The membership lookup, the last-owner lock and the role
// update all run inside one transaction, so a non-member target, a refused
// hierarchy rule, or a would-be-ownerless team never reaches the database
// with a partial write.
func (d Deps) updateMember(ctx context.Context, in *UpdateMemberInput) (*struct{}, error) {
	actor := in.Member()

	newRole, err := authz.ParseRole(in.Body.Role)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("unknown role")
	}

	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		current, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
			TeamID: actor.TeamID,
			UserID: in.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return huma.Error404NotFound("that person is not a member of this team")
		}
		if err != nil {
			return err
		}

		currentRole, err := authz.ParseRole(current.Role)
		if err != nil {
			return err
		}

		// Anything involving the owner role — granting it or taking it away —
		// is an owner's decision, not an admin's.
		if (currentRole == authz.RoleOwner || newRole == authz.RoleOwner) &&
			!actor.Role.AtLeast(authz.RoleOwner) {
			return huma.Error403Forbidden("changing the owner role requires the owner role")
		}

		if currentRole == authz.RoleOwner && newRole != authz.RoleOwner {
			if err := d.refuseLastOwner(ctx, q, actor.TeamID); err != nil {
				return err
			}
		}

		if currentRole == newRole {
			return nil // Nothing changed; do not write a misleading audit entry.
		}

		if err := q.UpdateTeamMemberRole(ctx, db.UpdateTeamMemberRoleParams{
			TeamID: actor.TeamID,
			UserID: in.UserID,
			Role:   newRole.String(),
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      actor.TeamID,
			ActorUserID: actor.UserID,
			Action:      audit.ActionMemberRoleChanged,
			EntityType:  audit.EntityTeamMember,
			EntityID:    in.UserID,
			Metadata:    map[string]any{"from": currentRole.String(), "to": newRole.String()},
		})
	})
	if err != nil {
		return nil, d.mutationError(err, "could not change the member's role",
			"update team member role", in.TeamID)
	}

	return nil, nil
}

// removeMember is DELETE /v1/teams/{team_id}/members/{user_id}. An admin may
// remove anyone but an owner, and the last owner can never be removed at
// all. The membership lookup, the last-owner lock and the delete all run
// inside one transaction, for the same reason as updateMember.
func (d Deps) removeMember(ctx context.Context, in *RemoveMemberInput) (*struct{}, error) {
	actor := in.Member()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		current, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
			TeamID: actor.TeamID,
			UserID: in.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return huma.Error404NotFound("that person is not a member of this team")
		}
		if err != nil {
			return err
		}

		currentRole, err := authz.ParseRole(current.Role)
		if err != nil {
			return err
		}

		if currentRole == authz.RoleOwner {
			if !actor.Role.AtLeast(authz.RoleOwner) {
				return huma.Error403Forbidden("removing an owner requires the owner role")
			}
			if err := d.refuseLastOwner(ctx, q, actor.TeamID); err != nil {
				return err
			}
		}

		if err := q.DeleteTeamMember(ctx, db.DeleteTeamMemberParams{
			TeamID: actor.TeamID,
			UserID: in.UserID,
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      actor.TeamID,
			ActorUserID: actor.UserID,
			Action:      audit.ActionMemberRemoved,
			EntityType:  audit.EntityTeamMember,
			EntityID:    in.UserID,
			Metadata:    map[string]any{"role": currentRole.String()},
		})
	})
	if err != nil {
		return nil, d.mutationError(err, "could not remove the member",
			"remove team member", in.TeamID)
	}

	return nil, nil
}

// refuseLastOwner locks the team's owner rows and refuses if only one is
// left. The lock is what makes this safe: without it, two concurrent
// demotions or removals both read "two owners" and both succeed, leaving the
// team ownerless. It must be called from inside the same transaction as the
// mutation it guards.
func (d Deps) refuseLastOwner(ctx context.Context, q *db.Queries, teamID uuid.UUID) error {
	owners, err := q.LockTeamOwners(ctx, teamID)
	if err != nil {
		return err
	}
	if len(owners) <= 1 {
		return huma.Error403Forbidden("a team must always have at least one owner")
	}
	return nil
}

// mutationError passes a deliberate huma status error through unchanged and
// turns anything else into a logged 500, so a handler's InTx callback can
// return either kind.
func (d Deps) mutationError(err error, message, operation string, teamID uuid.UUID) error {
	var status huma.StatusError
	if errors.As(err, &status) {
		return err
	}
	d.Log.Error(operation, "error", err, "team_id", teamID)
	return huma.Error500InternalServerError(message)
}

// derefString flattens a nullable column. auth.users.email is nullable because
// Supabase allows phone-only accounts.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
