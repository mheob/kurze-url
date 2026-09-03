package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Tag is a tag as the API reports it. Names are unique per team
// case-insensitively but are stored and returned exactly as typed. Unlike
// Folder, there is no created_at: the schema has no such column for tag.
type Tag struct {
	ID     uuid.UUID `json:"id"`
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
}

// CreateTagInput declares its authorization in its type: EditorScope resolves
// and checks the caller's role before this handler's body runs.
type CreateTagInput struct {
	authz.EditorScope
	Body struct {
		Name string `json:"name" maxLength:"60" doc:"Trimmed on input. Unique per team, case-insensitively."`
	}
}

// TagOutput wraps a single tag resource.
type TagOutput struct {
	Status int
	Body   Tag
}

// ListTagsInput takes no filters: a list capped at 100 rows and ordered by
// name does not need them.
type ListTagsInput struct {
	authz.ViewerScope
	PageParams
}

// ListTagsOutput wraps a paginated list of tags.
type ListTagsOutput struct {
	Body Page[Tag]
}

// UpdateTagInput declares its authorization in its type: TagEditorScope
// resolves which team owns the tag and requires at least the editor role.
type UpdateTagInput struct {
	authz.TagEditorScope
	Body struct {
		Name string `json:"name" maxLength:"60"`
	}
}

// DeleteTagInput declares its authorization in its type.
type DeleteTagInput struct {
	authz.TagEditorScope
}

// DeleteTagOutput carries no body: a successful delete is 204 No Content.
type DeleteTagOutput struct {
	Status int
}

func (d Deps) registerTags(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-tag",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/tags",
		Summary:       "Create a tag",
		Tags:          []string{"Tags"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createTag)

	huma.Register(api, huma.Operation{
		OperationID: "list-tags",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/tags",
		Summary:     "List a team's tags",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listTags)

	huma.Register(api, huma.Operation{
		OperationID: "update-tag",
		Method:      http.MethodPatch,
		Path:        "/v1/tags/{tag_id}",
		Summary:     "Rename a tag",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateTag)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-tag",
		Method:        http.MethodDelete,
		Path:          "/v1/tags/{tag_id}",
		Summary:       "Delete a tag",
		Tags:          []string{"Tags"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.deleteTag)
}

// errTagCapReached travels out of the transaction so the cap becomes a 422
// rather than a 500. It never reaches the client. It is folder.go's
// errFolderCapReached's sibling, added here — alongside the code that
// actually uses it — rather than in that earlier task.
var errTagCapReached = errors.New("api: tag cap reached")

func (d Deps) createTag(ctx context.Context, in *CreateTagInput) (*TagOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	var created db.Tag
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Check-then-act inside the transaction. Two concurrent creates can
		// still both pass and leave the team one row over the cap; that is
		// accepted, because the cap protects a 500 MB budget rather than an
		// invariant, and closing the race would need an advisory lock on every
		// create.
		count, err := q.CountTagsForTeam(ctx, member.TeamID)
		if err != nil {
			return err
		}
		if count >= maxTagsPerTeam {
			return errTagCapReached
		}

		row, err := q.CreateTag(ctx, db.CreateTagParams{
			TeamID: member.TeamID,
			Name:   name,
		})
		if err != nil {
			return err
		}
		created = row

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTagCreated,
			EntityType:  audit.EntityTag,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, errTagCapReached):
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("a team may have at most %d tags", maxTagsPerTeam))
	case isUniqueViolation(err):
		// The index folds case, so this fires for "SOMMERFEST" against an
		// existing "Sommerfest" as well as for an exact repeat.
		return nil, huma.Error409Conflict("a tag with that name already exists")
	case err != nil:
		d.Log.Error("create tag", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not create the tag")
	}

	return &TagOutput{Status: http.StatusCreated, Body: tagResponse(created)}, nil
}

func (d Deps) listTags(ctx context.Context, in *ListTagsInput) (*ListTagsOutput, error) {
	member := in.Member()

	// There is no RLS: this filters by team_id even though the ViewerScope
	// already authorized the caller for member.TeamID, because that filter is
	// what a reviewer can see and the permission-matrix test cannot catch a
	// missing one.
	rows, err := d.Queries.ListTagsForTeam(ctx, db.ListTagsForTeamParams{
		TeamID: member.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list tags", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not list tags")
	}

	items := make([]Tag, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Tag{ID: row.ID, TeamID: row.TeamID, Name: row.Name})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountTagsForTeam(ctx, member.TeamID)
		if err != nil {
			d.Log.Error("count tags", "error", err, "team_id", member.TeamID)
			return nil, huma.Error500InternalServerError("could not list tags")
		}
	}

	return &ListTagsOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) updateTag(ctx context.Context, in *UpdateTagInput) (*TagOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	// tag is bound to a local because in.Tag() returns a value: reading its
	// field is fine, but taking its address is not, since a method result is
	// not addressable.
	tag := in.Tag()

	var updated db.Tag
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// The scope already authorized this caller. The team filter is here
		// anyway: it is what a reviewer can see, and the matrix test cannot
		// see a missing one.
		row, err := q.UpdateTag(ctx, db.UpdateTagParams{
			ID:     tag.ID,
			TeamID: member.TeamID,
			Name:   name,
		})
		if err != nil {
			return err
		}
		updated = row

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTagUpdated,
			EntityType:  audit.EntityTag,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case isUniqueViolation(err):
		// The index folds case, so this fires for "SOMMERFEST" against an
		// existing "Sommerfest" as well as for an exact repeat.
		return nil, huma.Error409Conflict("a tag with that name already exists")
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("tag not found")
	case err != nil:
		d.Log.Error("update tag", "error", err, "tag_id", tag.ID)
		return nil, huma.Error500InternalServerError("could not update the tag")
	}

	return &TagOutput{Status: http.StatusOK, Body: tagResponse(updated)}, nil
}

func (d Deps) deleteTag(ctx context.Context, in *DeleteTagInput) (*DeleteTagOutput, error) {
	member := in.Member()

	// tag is bound to a local because &in.Tag().ID does not compile: the
	// method returns a value, and a value's field is not addressable.
	tag := in.Tag()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		row, err := q.DeleteTag(ctx, db.DeleteTagParams{
			ID: tag.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTagDeleted,
			EntityType:  audit.EntityTag,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("tag not found")
	case err != nil:
		d.Log.Error("delete tag", "error", err, "tag_id", tag.ID)
		return nil, huma.Error500InternalServerError("could not delete the tag")
	}

	return &DeleteTagOutput{Status: http.StatusNoContent}, nil
}

func tagResponse(row db.Tag) Tag {
	return Tag{ID: row.ID, TeamID: row.TeamID, Name: row.Name}
}
