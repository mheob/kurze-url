package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// AuditRetentionYears is how long an audit entry is kept: two years, decided
// by the maintainer on 2026-10-03. The log exists so that a Verein can still
// show who changed what long after the fact — a board asking in spring who
// moved last summer's registration link — without the instance keeping its
// administrative history, member addresses in invitation metadata included,
// forever.
//
// It is deliberately not RetentionDays. That window is a privacy-minimised
// rollup of strangers' clicks and is as short as the product allows; this one
// is a governance record whose whole purpose is to outlast the events it
// describes. Folding the two into one constant would make one of them wrong.
//
// auditRetentionFloor is the only place this becomes a boundary; nothing else
// may restate the arithmetic. The retention job deletes before it, and
// GET /v1/teams/{team_id}/audit-log serves from it forward and reports its day
// as retained_since, which is where the web filter reads it from instead of
// restating the period in TypeScript. The operation's description interpolates
// this constant and no doc tag states the period, so the published contract
// cannot keep naming an old period after the constant changes.
// TestAuditRetentionFloorIsTheAuditLogsFloor holds the job and the endpoint
// together.
const AuditRetentionYears = 2

// auditRetentionFloor is the oldest instant the audit log keeps: the start of
// the current UTC day, as dayOf defines a day, two calendar years back. The
// retention job deletes every entry created before it, and the audit log
// endpoint never serves one: between two of the job's daily runs the floor
// has already moved past entries the job has not yet deleted, and showing
// them would let a reader see what the policy says is gone.
//
// AddDate normalises a date that does not exist, so on 29 February the floor
// two years back is 1 March rather than 28 February. That keeps one day less,
// never one more, which is the acceptable direction for a deletion floor;
// TestAuditRetentionFloorOnALeapDay pins it so nobody "fixes" it into the
// other direction.
func auditRetentionFloor(now time.Time) time.Time {
	return dayOf(now).AddDate(-AuditRetentionYears, 0, 0)
}

// auditLogFrom is the lower bound the audit log query actually runs with: the
// caller's from, raised to the retention floor when it is earlier or absent
// (the zero time is earlier than any floor). It raises rather than refuses
// because a bookmarked filter outlives the floor that moves under it every
// day, and an error for "older than we keep" would break a link that still
// has a sensible answer.
//
// to needs no counterpart. A to before the floor leaves from > to, which the
// query answers with no rows, and an empty page is the honest answer to a
// window whose entries are all gone.
func auditLogFrom(requested, now time.Time) time.Time {
	floor := auditRetentionFloor(now)
	if requested.Before(floor) {
		return floor
	}
	return requested
}

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
	From        time.Time `query:"from" doc:"Only entries at or after this instant (RFC 3339). Entries older than the retention floor are deleted and never returned, so an earlier or absent from is raised to the floor; retained_since in the response names its day."`
	To          time.Time `query:"to" doc:"Only entries at or before this instant (RFC 3339). A to before the retention floor matches nothing and answers an empty page, not an error."`
}

// AuditLogPage is a page of the audit log plus the day it reaches back to.
//
// Page is embedded without a JSON name, so Huma splices its four fields into
// this object the way encoding/json does (the same mechanism StatDay relies
// on for StatCounts), and the generated schema is the usual envelope with one
// more required property rather than an envelope nested inside another.
type AuditLogPage struct {
	Page[AuditEntry]
	RetainedSince string `json:"retained_since" doc:"The first day, as YYYY-MM-DD in UTC, this log still holds entries for. Entries before this day have been deleted automatically, and a from filter earlier than this day is raised to it."`
}

// ListAuditLogOutput is the body of GET /v1/teams/{team_id}/audit-log.
type ListAuditLogOutput struct {
	Body AuditLogPage
}

func (d Deps) registerAuditLog(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/audit-log",
		Summary:     "Read a team's audit log",
		Description: fmt.Sprintf("Administrative history, so it is restricted to admins and owners. "+
			"Entries are kept for %d years and deleted automatically after that; "+
			"retained_since says how far back this log reaches.", AuditRetentionYears),
		Tags:     []string{"Audit"},
		Security: []map[string][]string{{"bearerAuth": {}}},
	}, d.listAuditLog)
}

func (d Deps) listAuditLog(
	ctx context.Context, in *ListAuditLogInput,
) (*ListAuditLogOutput, error) {
	member := in.Member()

	// One reading of the clock, so the floor the query is clamped to and the
	// retained_since reported beside the result cannot straddle midnight.
	now := d.now()
	from := auditLogFrom(in.From, now)

	params := db.ListAuditLogParams{
		TeamID:       member.TeamID,
		From:         &from,
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
		// metadata is nullable at the schema level, though every writer
		// (internal/audit.Log) always supplies at least "{}"; nil is only
		// possible for a row written by something other than that path, and
		// json.RawMessage(nil) marshals as null, which is fine here too.
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

	// The count runs with the same clamped from as the page above, so a page
	// past the end reports the total of what the endpoint will actually show.
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

	return &ListAuditLogOutput{Body: AuditLogPage{
		Page:          NewPage(items, in.PageParams, total),
		RetainedSince: auditRetentionFloor(now).Format(dayLayout),
	}}, nil
}
