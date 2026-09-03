package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
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

func (d Deps) registerMembers(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/members",
		Summary:     "List a team's members",
		Tags:        []string{"Members"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listMembers)
}

func (d Deps) listMembers(ctx context.Context, in *ListMembersInput) (*ListMembersOutput, error) {
	rows, err := d.Queries.ListTeamMembers(ctx, db.ListTeamMembersParams{
		TeamID: in.TeamID,
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

	return &ListMembersOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

// derefString flattens a nullable column. auth.users.email is nullable because
// Supabase allows phone-only accounts.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
