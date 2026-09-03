package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Team is the API's representation of a team. Role is the *caller's* role in
// it, which is what the frontend needs to decide which controls to render.
type Team struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Role      string    `json:"role"`
}

// CreateTeamInput is the body of POST /v1/teams.
type CreateTeamInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200" doc:"The Verein's display name."`
	}
}

// TeamOutput is the response body shared by every single-team operation.
type TeamOutput struct {
	Body Team
}

// ListTeamsInput is the query of GET /v1/teams.
type ListTeamsInput struct {
	PageParams
}

// ListTeamsOutput is the body of GET /v1/teams.
type ListTeamsOutput struct {
	Body Page[Team]
}

// GetTeamInput is GET /v1/teams/{team_id}. Any team member may call it.
type GetTeamInput struct {
	authz.ViewerScope
}

// UpdateTeamInput is PATCH /v1/teams/{team_id}. Renaming requires admin.
type UpdateTeamInput struct {
	authz.AdminScope
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200"`
	}
}

func (d Deps) registerTeams(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-team",
		Method:        http.MethodPost,
		Path:          "/v1/teams",
		Summary:       "Create a team",
		Description:   "Restricted to the instance maintainers. A Verein asks the maintainer, who creates the team and invites its first owner.",
		Tags:          []string{"Teams"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createTeam)

	huma.Register(api, huma.Operation{
		OperationID: "list-teams",
		Method:      http.MethodGet,
		Path:        "/v1/teams",
		Summary:     "List the teams the caller belongs to",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listTeams)

	huma.Register(api, huma.Operation{
		OperationID: "get-team",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}",
		Summary:     "Get a team",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.getTeam)

	huma.Register(api, huma.Operation{
		OperationID: "update-team",
		Method:      http.MethodPatch,
		Path:        "/v1/teams/{team_id}",
		Summary:     "Rename a team",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateTeam)
}

func (d Deps) createTeam(ctx context.Context, in *CreateTeamInput) (*TeamOutput, error) {
	claims, ok := UserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !d.Config.IsMaintainer(claims.UserID) {
		return nil, huma.Error403Forbidden("team creation is limited to the instance maintainers")
	}

	var created db.Team
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, in.Body.Name)
		if err != nil {
			return err
		}
		created = team

		// The timestamp is unused here; the response reports the team, not the
		// membership.
		if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{
			TeamID: team.ID,
			UserID: claims.UserID,
			Role:   authz.RoleOwner.String(),
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      team.ID,
			ActorUserID: claims.UserID,
			Action:      audit.ActionTeamCreated,
			EntityType:  audit.EntityTeam,
			EntityID:    team.ID,
			Metadata:    map[string]any{"name": team.Name},
		})
	})
	if err != nil {
		d.Log.Error("create team", "error", err)
		return nil, huma.Error500InternalServerError("could not create the team")
	}

	return &TeamOutput{Body: Team{
		ID:        created.ID,
		Name:      created.Name,
		CreatedAt: created.CreatedAt,
		Role:      authz.RoleOwner.String(),
	}}, nil
}

func (d Deps) listTeams(ctx context.Context, in *ListTeamsInput) (*ListTeamsOutput, error) {
	claims, ok := UserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("not authenticated")
	}

	rows, err := d.Queries.ListTeamsForUser(ctx, db.ListTeamsForUserParams{
		UserID: claims.UserID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list teams", "error", err)
		return nil, huma.Error500InternalServerError("could not list teams")
	}

	items := make([]Team, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Team{
			ID:        row.ID,
			Name:      row.Name,
			CreatedAt: row.CreatedAt,
			Role:      row.Role,
		})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountTeamsForUser(ctx, claims.UserID)
		if err != nil {
			d.Log.Error("count teams", "error", err)
			return nil, huma.Error500InternalServerError("could not list teams")
		}
	}

	return &ListTeamsOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) getTeam(ctx context.Context, in *GetTeamInput) (*TeamOutput, error) {
	member := in.Member()

	team, err := d.Queries.GetTeam(ctx, member.TeamID)
	if err != nil {
		d.Log.Error("get team", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not load the team")
	}

	return &TeamOutput{Body: Team{
		ID:        team.ID,
		Name:      team.Name,
		CreatedAt: team.CreatedAt,
		Role:      member.Role.String(),
	}}, nil
}

func (d Deps) updateTeam(ctx context.Context, in *UpdateTeamInput) (*TeamOutput, error) {
	member := in.Member()

	var renamed db.Team
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		before, err := q.GetTeam(ctx, member.TeamID)
		if err != nil {
			return err
		}

		if before.Name == in.Body.Name {
			renamed = before
			return nil // Nothing changed; do not write a misleading audit entry.
		}

		after, err := q.RenameTeam(ctx, db.RenameTeamParams{ID: member.TeamID, Name: in.Body.Name})
		if err != nil {
			return err
		}
		renamed = after

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    member.TeamID,
			Metadata:    map[string]any{"from": before.Name, "to": after.Name},
		})
	})
	if err != nil {
		d.Log.Error("rename team", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not rename the team")
	}

	return &TeamOutput{Body: Team{
		ID:        renamed.ID,
		Name:      renamed.Name,
		CreatedAt: renamed.CreatedAt,
		Role:      member.Role.String(),
	}}, nil
}
