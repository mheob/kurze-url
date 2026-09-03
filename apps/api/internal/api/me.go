package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// TeamMembership is one entry in GET /v1/me — it drives the frontend's team
// switcher, which needs the team's name and the caller's role in it.
type TeamMembership struct {
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
	Role   string    `json:"role"`
}

// MeOutput is the body of GET /v1/me.
type MeOutput struct {
	Body struct {
		UserID      uuid.UUID        `json:"user_id"`
		Email       string           `json:"email"`
		Memberships []TeamMembership `json:"memberships"`
	}
}

func (d Deps) registerMe(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-me",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "The authenticated user and their teams",
		Tags:        []string{"Session"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		claims, ok := UserFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		rows, err := d.Queries.ListMembershipsForUser(ctx, claims.UserID)
		if err != nil {
			d.Log.Error("list memberships", "error", err)
			return nil, huma.Error500InternalServerError("could not load team memberships")
		}

		memberships := make([]TeamMembership, 0, len(rows))
		for _, row := range rows {
			memberships = append(memberships, TeamMembership{
				TeamID: row.TeamID,
				Name:   row.TeamName,
				Role:   row.Role,
			})
		}

		out := &MeOutput{}
		out.Body.UserID = claims.UserID
		out.Body.Email = claims.Email
		out.Body.Memberships = memberships
		return out, nil
	})
}
