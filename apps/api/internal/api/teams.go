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
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	Role      string    `json:"role"`
}

// CreateTeamInput is the body of POST /v1/teams.
type CreateTeamInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200" doc:"The Verein's display name."`
		Slug string `json:"slug" minLength:"3" maxLength:"40" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" doc:"Immutable identifier used in the app's URLs, e.g. \"sv-gruenwald\"."`
	}
}

// reservedTeamSlugs may not be taken, because the frontend has static route
// segments that would shadow them. TanStack Router matches a static segment
// before a dynamic one, so a team holding one of these would have its pages
// permanently answered by another screen. The create form itself has already
// moved out of /teams/, so this list is the guard for the *next* static child
// route someone adds there — without it, adding a route is silently also a
// decision to strip an existing team of its URL.
var reservedTeamSlugs = map[string]struct{}{
	"new": {}, "create": {}, "settings": {}, "admin": {}, "api": {},
	"login": {}, "logout": {}, "me": {}, "invite": {}, "teams": {},
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

	if _, reserved := reservedTeamSlugs[in.Body.Slug]; reserved {
		return nil, huma.Error422UnprocessableEntity("that slug is reserved",
			&huma.ErrorDetail{
				Location: "body.slug",
				Message:  "this slug is reserved; choose another",
				Value:    in.Body.Slug,
			})
	}

	var created db.CreateTeamRow
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: in.Body.Name, Slug: in.Body.Slug})
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
			Metadata:    map[string]any{"name": team.Name, "slug": team.Slug},
		})
	})
	switch {
	case isUniqueViolation(err):
		return nil, huma.Error409Conflict("a team with that slug already exists",
			&huma.ErrorDetail{
				Location: "body.slug",
				Message:  "this slug is already taken",
				Value:    in.Body.Slug,
			})
	case err != nil:
		d.Log.Error("create team", "error", err)
		return nil, huma.Error500InternalServerError("could not create the team")
	}

	return &TeamOutput{Body: Team{
		ID:        created.ID,
		Name:      created.Name,
		Slug:      created.Slug,
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
			Slug:      row.Slug,
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
		Slug:      team.Slug,
		CreatedAt: team.CreatedAt,
		Role:      member.Role.String(),
	}}, nil
}

func (d Deps) updateTeam(ctx context.Context, in *UpdateTeamInput) (*TeamOutput, error) {
	member := in.Member()

	// GetTeam and RenameTeam return distinct row types (their own selects
	// don't line up column-for-column with db.Team), so the row that survives
	// to the response is captured field by field rather than as one shared
	// struct.
	var id uuid.UUID
	var name, slug string
	var createdAt time.Time

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		before, err := q.GetTeam(ctx, member.TeamID)
		if err != nil {
			return err
		}

		if before.Name == in.Body.Name {
			id, name, slug, createdAt = before.ID, before.Name, before.Slug, before.CreatedAt
			return nil // Nothing changed; do not write a misleading audit entry.
		}

		after, err := q.RenameTeam(ctx, db.RenameTeamParams{ID: member.TeamID, Name: in.Body.Name})
		if err != nil {
			return err
		}
		id, name, slug, createdAt = after.ID, after.Name, after.Slug, after.CreatedAt

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
		ID:        id,
		Name:      name,
		Slug:      slug,
		CreatedAt: createdAt,
		Role:      member.Role.String(),
	}}, nil
}
