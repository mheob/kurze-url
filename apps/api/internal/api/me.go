package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// TeamMembership is one entry in GET /v1/me — it drives the frontend's team
// switcher, which needs the team's name, its slug, and the caller's role in
// it. The slug is the field the whole frontend's slug-to-id resolution
// depends on: every `/teams/{teamSlug}/...` route looks up its `team_id`
// here rather than trusting the URL directly.
type TeamMembership struct {
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
	Slug   string    `json:"slug"`
	Role   string    `json:"role"`
}

// MeOutput is the body of GET /v1/me.
//
// IsMaintainer mirrors the check POST /v1/teams enforces (`createTeam`, via
// Config.IsMaintainer). The frontend cannot derive it: MAINTAINER_USER_IDS is
// deploy-time configuration on this service, invisible to the browser. Without
// it the web app can only offer team creation to everyone and let the 403
// arrive after the form is filled in, which reads as a broken feature rather
// than as one that was never for you. It tells the caller nothing they could
// not learn by posting once.
type MeOutput struct {
	Body struct {
		UserID       uuid.UUID        `json:"user_id"`
		Email        string           `json:"email"`
		IsMaintainer bool             `json:"is_maintainer"`
		Memberships  []TeamMembership `json:"memberships"`
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
				Slug:   row.TeamSlug,
				Role:   row.Role,
			})
		}

		out := &MeOutput{}
		out.Body.UserID = claims.UserID
		out.Body.Email = claims.Email
		out.Body.IsMaintainer = d.Config.IsMaintainer(claims.UserID)
		out.Body.Memberships = memberships
		return out, nil
	})
}
