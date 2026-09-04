package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Folder is a folder as the API reports it. parent_folder_id is deliberately
// absent: the API never writes it, so publishing it would advertise a
// capability that does not exist.
type Folder struct {
	ID        uuid.UUID `json:"id"`
	TeamID    uuid.UUID `json:"team_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateFolderInput declares its authorization in its type: EditorScope
// resolves and checks the caller's role before this handler's body runs.
type CreateFolderInput struct {
	authz.EditorScope
	Body struct {
		Name string `json:"name" maxLength:"60" doc:"Trimmed on input. Must not be empty."`
	}
}

// FolderOutput wraps a single folder resource.
type FolderOutput struct {
	Status int
	Body   Folder
}

// ListFoldersInput takes no filters: a list capped at 100 rows and ordered by
// name does not need them.
type ListFoldersInput struct {
	authz.ViewerScope
	PageParams
}

// ListFoldersOutput wraps a paginated list of folders.
type ListFoldersOutput struct {
	Body Page[Folder]
}

// UpdateFolderInput declares its authorization in its type: FolderEditorScope
// resolves which team owns the folder and requires at least the editor role.
type UpdateFolderInput struct {
	authz.FolderEditorScope
	Body struct {
		Name string `json:"name" maxLength:"60"`
	}
}

// DeleteFolderInput declares its authorization in its type.
type DeleteFolderInput struct {
	authz.FolderEditorScope
}

// DeleteFolderOutput carries no body: a successful delete is 204 No Content.
type DeleteFolderOutput struct {
	Status int
}

func (d Deps) registerFolders(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-folder",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/folders",
		Summary:       "Create a folder",
		Tags:          []string{"Folders"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createFolder)

	huma.Register(api, huma.Operation{
		OperationID: "list-folders",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/folders",
		Summary:     "List a team's folders",
		Tags:        []string{"Folders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listFolders)

	huma.Register(api, huma.Operation{
		OperationID: "update-folder",
		Method:      http.MethodPatch,
		Path:        "/v1/folders/{folder_id}",
		Summary:     "Rename a folder",
		Tags:        []string{"Folders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateFolder)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-folder",
		Method:        http.MethodDelete,
		Path:          "/v1/folders/{folder_id}",
		Summary:       "Delete a folder",
		Tags:          []string{"Folders"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.deleteFolder)
}

// errFolderCapReached travels out of the transaction so the cap becomes a 422
// rather than a 500. It never reaches the client. Its tag counterpart lives in
// tags.go, declared alongside the code that actually uses it — an unused
// sibling declared here would just be dead code.
var errFolderCapReached = errors.New("api: folder cap reached")

func (d Deps) createFolder(ctx context.Context, in *CreateFolderInput) (*FolderOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	var created db.CreateFolderRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Check-then-act inside the transaction. Two concurrent creates can
		// still both pass and leave the team one row over the cap; that is
		// accepted, because the cap protects a 500 MB budget rather than an
		// invariant, and closing the race would need an advisory lock on every
		// create.
		count, err := q.CountFoldersForTeam(ctx, member.TeamID)
		if err != nil {
			return err
		}
		if count >= maxFoldersPerTeam {
			return errFolderCapReached
		}

		row, err := q.CreateFolder(ctx, db.CreateFolderParams{
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
			Action:      audit.ActionFolderCreated,
			EntityType:  audit.EntityFolder,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, errFolderCapReached):
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("a team may have at most %d folders", maxFoldersPerTeam))
	case err != nil:
		d.Log.Error("create folder", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not create the folder")
	}

	return &FolderOutput{Status: http.StatusCreated, Body: folderResponse(created)}, nil
}

func (d Deps) listFolders(ctx context.Context, in *ListFoldersInput) (*ListFoldersOutput, error) {
	member := in.Member()

	// There is no RLS: this filters by team_id even though the ViewerScope
	// already authorized the caller for member.TeamID, because that filter is
	// what a reviewer can see and the permission-matrix test cannot catch a
	// missing one.
	rows, err := d.Queries.ListFoldersForTeam(ctx, db.ListFoldersForTeamParams{
		TeamID: member.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list folders", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not list folders")
	}

	items := make([]Folder, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Folder{
			ID: row.ID, TeamID: row.TeamID, Name: row.Name, CreatedAt: row.CreatedAt,
		})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountFoldersForTeam(ctx, member.TeamID)
		if err != nil {
			d.Log.Error("count folders", "error", err, "team_id", member.TeamID)
			return nil, huma.Error500InternalServerError("could not list folders")
		}
	}

	return &ListFoldersOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) updateFolder(ctx context.Context, in *UpdateFolderInput) (*FolderOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	// folder is bound to a local because in.Folder() returns a value: reading
	// its field is fine, but taking its address (as CountLinksInFolderParams
	// needs in deleteFolder below) is not, since a method result is not
	// addressable.
	folder := in.Folder()

	var updated db.UpdateFolderRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// The scope already authorized this caller. The team filter is here
		// anyway: it is what a reviewer can see, and the matrix test cannot
		// see a missing one.
		row, err := q.UpdateFolder(ctx, db.UpdateFolderParams{
			ID:     folder.ID,
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
			Action:      audit.ActionFolderUpdated,
			EntityType:  audit.EntityFolder,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("folder not found")
	case err != nil:
		d.Log.Error("update folder", "error", err, "folder_id", in.FolderID)
		return nil, huma.Error500InternalServerError("could not update the folder")
	}

	// db.UpdateFolderRow and db.CreateFolderRow happen to share the same
	// fields (ID, TeamID, Name, CreatedAt) in the same order, so this
	// conversion compiles; if sqlc's shapes for the two queries ever diverge,
	// this line stops compiling too, which is the point.
	return &FolderOutput{Status: http.StatusOK, Body: folderResponse(db.CreateFolderRow(updated))}, nil
}

func (d Deps) deleteFolder(ctx context.Context, in *DeleteFolderInput) (*DeleteFolderOutput, error) {
	member := in.Member()

	// folder is bound to a local because &in.Folder().ID does not compile: the
	// method returns a value, and a value's field is not addressable.
	folder := in.Folder()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Counted before the delete, so the number is unambiguously the
		// pre-delete count. The links themselves are unfiled by the
		// on delete set null foreign key, not by application code.
		unfiled, err := q.CountLinksInFolder(ctx, db.CountLinksInFolderParams{
			FolderID: &folder.ID,
			TeamID:   member.TeamID,
		})
		if err != nil {
			return err
		}

		row, err := q.DeleteFolder(ctx, db.DeleteFolderParams{
			ID: folder.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionFolderDeleted,
			EntityType:  audit.EntityFolder,
			EntityID:    row.ID,
			// The name is recorded because entity_id now points at a deleted
			// row: without it the entry says only that some folder went.
			Metadata: map[string]any{"name": row.Name, "links_unfiled": unfiled},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("folder not found")
	case err != nil:
		d.Log.Error("delete folder", "error", err, "folder_id", in.FolderID)
		return nil, huma.Error500InternalServerError("could not delete the folder")
	}

	return &DeleteFolderOutput{Status: http.StatusNoContent}, nil
}

func folderResponse(row db.CreateFolderRow) Folder {
	return Folder{ID: row.ID, TeamID: row.TeamID, Name: row.Name, CreatedAt: row.CreatedAt}
}
