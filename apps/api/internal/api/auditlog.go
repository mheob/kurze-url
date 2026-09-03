package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// AuditEntry is one audit_log row. Metadata is passed through verbatim: the
// writer already guarantees it carries no secret and no IP address.
type AuditEntry struct {
	ID          int64           `json:"id"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *uuid.UUID      `json:"entity_id,omitempty"`
	ActorUserID *uuid.UUID      `json:"actor_user_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ListAuditLogInput is the query of GET /v1/teams/{team_id}/audit-log.
// Reading the audit log requires admin: it is administrative history, not a
// viewer-level right like the team or member reads.
type ListAuditLogInput struct {
	authz.AdminScope
	PageParams
	EntityType  string    `query:"entity_type" enum:"team,team_member,domain,folder,tag,link" doc:"Restrict to one entity type."`
	Action      string    `query:"action" maxLength:"64" doc:"Restrict to one action, e.g. team_member.removed."`
	ActorUserID string    `query:"actor_user_id" doc:"Restrict to one actor, as a UUID."`
	From        time.Time `query:"from" doc:"Only entries at or after this instant (RFC 3339)."`
	To          time.Time `query:"to" doc:"Only entries at or before this instant (RFC 3339)."`
}

// ListAuditLogOutput is the body of GET /v1/teams/{team_id}/audit-log.
type ListAuditLogOutput struct {
	Body Page[AuditEntry]
}

func (d Deps) registerAuditLog(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/audit-log",
		Summary:     "Read a team's audit log",
		Description: "Administrative history, so it is restricted to admins and owners.",
		Tags:        []string{"Audit"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listAuditLog)
}

func (d Deps) listAuditLog(
	ctx context.Context, in *ListAuditLogInput,
) (*ListAuditLogOutput, error) {
	member := in.Member()

	params := db.ListAuditLogParams{
		TeamID:       member.TeamID,
		ResultLimit:  in.Limit(),
		ResultOffset: in.Offset(),
	}

	if in.EntityType != "" {
		entityType := in.EntityType
		params.EntityType = &entityType
	}
	if in.Action != "" {
		action := in.Action
		params.Action = &action
	}
	if in.ActorUserID != "" {
		actor, err := uuid.Parse(in.ActorUserID)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("actor_user_id must be a UUID")
		}
		params.ActorUserID = &actor
	}
	if !in.From.IsZero() {
		from := in.From
		params.From = &from
	}
	if !in.To.IsZero() {
		to := in.To
		params.To = &to
	}

	rows, err := d.Queries.ListAuditLog(ctx, params)
	if err != nil {
		d.Log.Error("list audit log", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not read the audit log")
	}

	items := make([]AuditEntry, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, AuditEntry{
			ID:          row.ID,
			Action:      row.Action,
			EntityType:  row.EntityType,
			EntityID:    row.EntityID,
			ActorUserID: row.ActorUserID,
			Metadata:    json.RawMessage(row.Metadata),
			CreatedAt:   row.CreatedAt,
		})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountAuditLog(ctx, db.CountAuditLogParams{
			TeamID:      params.TeamID,
			EntityType:  params.EntityType,
			Action:      params.Action,
			ActorUserID: params.ActorUserID,
			From:        params.From,
			To:          params.To,
		})
		if err != nil {
			d.Log.Error("count audit log", "error", err, "team_id", in.TeamID)
			return nil, huma.Error500InternalServerError("could not read the audit log")
		}
	}

	return &ListAuditLogOutput{Body: NewPage(items, in.PageParams, total)}, nil
}
