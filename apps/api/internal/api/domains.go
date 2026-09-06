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
	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

// Domain is a domain as the API reports it. VerificationToken and Records are
// included on every read, not just on creation: the screen that shows a
// Verein what to put in DNS has to be able to show it again tomorrow.
type Domain struct {
	ID                 uuid.UUID  `json:"id"`
	TeamID             uuid.UUID  `json:"team_id"`
	Hostname           string     `json:"hostname"`
	VerificationStatus string     `json:"verification_status"`
	VerificationToken  string     `json:"verification_token"`
	VerifiedAt         *time.Time `json:"verified_at"`
	Records            DNSRecords `json:"records"`
}

// DNSRecords are the two entries the claiming team must create.
type DNSRecords struct {
	TXT   DNSRecord `json:"txt"`
	CNAME DNSRecord `json:"cname"`
}

// DNSRecord is one entry, in the shape a DNS provider's form asks for.
type DNSRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CreateDomainInput declares its authorization in its type: AdminScope
// resolves and checks the caller's role before this handler's body runs.
// Admin, not editor: links, folders and tags are content, and a domain is
// the namespace that content lives in — losing it takes every link along.
// That belongs with member management.
type CreateDomainInput struct {
	authz.AdminScope
	Body struct {
		Hostname string `json:"hostname" maxLength:"253" doc:"A subdomain you control, e.g. links.verein.de. Not an apex."`
	}
}

// DomainOutput wraps a single domain resource.
type DomainOutput struct {
	Status int
	Body   Domain
}

// ListDomainsInput takes no filters: a list capped at 100 rows and ordered by
// hostname does not need them.
type ListDomainsInput struct {
	authz.ViewerScope
	PageParams
}

// ListDomainsOutput wraps a paginated list of domains.
type ListDomainsOutput struct {
	Body Page[Domain]
}

// GetDomainInput declares its authorization in its type: DomainViewerScope
// resolves which team owns the domain and requires at least the viewer role.
type GetDomainInput struct {
	authz.DomainViewerScope
}

func (d Deps) registerDomains(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-domain",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/domains",
		Summary:       "Claim a hostname for a team",
		Tags:          []string{"Domains"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createDomain)

	huma.Register(api, huma.Operation{
		OperationID: "list-domains",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/domains",
		Summary:     "List a team's domains",
		Tags:        []string{"Domains"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listDomains)

	huma.Register(api, huma.Operation{
		OperationID: "get-domain",
		Method:      http.MethodGet,
		Path:        "/v1/domains/{domain_id}",
		Summary:     "Get a domain",
		Tags:        []string{"Domains"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.getDomain)
}

func (d Deps) createDomain(ctx context.Context, in *CreateDomainInput) (*DomainOutput, error) {
	member := in.Member()

	if err := d.allowDomainClaim(ctx, member.UserID); err != nil {
		return nil, err
	}

	// NormalizeHostname's only failure modes are ErrApex, ErrReserved and
	// ErrMalformed, each wrapped with %w around a message that already names
	// which one fired and why — so every one of them maps to the same 422,
	// carrying that message unchanged.
	hostname, err := domainverify.NormalizeHostname(in.Body.Hostname, d.selfHostnames())
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	token, err := domainverify.GenerateToken()
	if err != nil {
		d.Log.Error("generate domain verification token", "error", err)
		return nil, huma.Error500InternalServerError("could not claim the domain")
	}

	var created db.Domain
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		row, err := q.CreateDomainClaim(ctx, db.CreateDomainClaimParams{
			TeamID:            member.TeamID,
			Hostname:          hostname,
			VerificationToken: &token,
		})
		if err != nil {
			return err
		}
		created = row

		// hostname, never the token: audit_log.metadata rejects any key whose
		// word segments include "token", and the token is not a secret to
		// begin with (it is published in public DNS) — hostname is what a
		// reviewer of this log actually wants to see.
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionDomainClaimed,
			EntityType:  audit.EntityDomain,
			EntityID:    row.ID,
			Metadata:    map[string]any{"hostname": row.Hostname},
		})
	})
	if err != nil {
		d.Log.Error("create domain claim", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not claim the domain")
	}

	return &DomainOutput{Status: http.StatusCreated, Body: domainResponse(created, d.Config.DomainDNSTarget)}, nil
}

// allowDomainClaim applies the per-user domain-claim limit, the same shape as
// allowLinkCreate: the subject is the authenticated user, not an IP, and a
// Redis outage must not stop a Verein claiming a domain.
func (d Deps) allowDomainClaim(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.DomainClaimRateLimitPerHour <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:domain-claim:"+userID.String(), d.Config.DomainClaimRateLimitPerHour, time.Hour)
	if err != nil {
		d.Log.Error("domain claim rate limit check failed", "error", err)
		return nil
	}
	if !ok {
		return huma.Error429TooManyRequests("too many domains claimed; try again later")
	}
	return nil
}

func (d Deps) listDomains(ctx context.Context, in *ListDomainsInput) (*ListDomainsOutput, error) {
	member := in.Member()

	// There is no RLS: this filters by team_id even though the ViewerScope
	// already authorized the caller for member.TeamID, because that filter is
	// what a reviewer can see and the permission-matrix test cannot catch a
	// missing one.
	rows, err := d.Queries.ListDomainsForTeam(ctx, db.ListDomainsForTeamParams{
		TeamID: member.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list domains", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not list domains")
	}

	items := make([]Domain, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, domainResponse(domainFromListRow(row), d.Config.DomainDNSTarget))
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountDomainsForTeam(ctx, member.TeamID)
		if err != nil {
			d.Log.Error("count domains", "error", err, "team_id", member.TeamID)
			return nil, huma.Error500InternalServerError("could not list domains")
		}
	}

	return &ListDomainsOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) getDomain(ctx context.Context, in *GetDomainInput) (*DomainOutput, error) {
	member := in.Member()

	// The scope already authorized this caller. The team filter is here
	// anyway: it is what a reviewer can see, and the matrix test cannot see a
	// missing one.
	row, err := d.Queries.GetDomainForTeam(ctx, db.GetDomainForTeamParams{
		ID: in.Domain().ID, TeamID: member.TeamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("domain not found")
	}
	if err != nil {
		d.Log.Error("get domain", "error", err, "domain_id", in.DomainID)
		return nil, huma.Error500InternalServerError("could not load the domain")
	}

	return &DomainOutput{Status: http.StatusOK, Body: domainResponse(row, d.Config.DomainDNSTarget)}, nil
}

// domainFromListRow adapts ListDomainsForTeam's row shape (which carries the
// extra total_count column) to db.Domain, so domainResponse has one input
// shape to build from regardless of which query produced it.
func domainFromListRow(row db.ListDomainsForTeamRow) db.Domain {
	return db.Domain{
		ID:                 row.ID,
		TeamID:             row.TeamID,
		Hostname:           row.Hostname,
		VerificationStatus: row.VerificationStatus,
		VercelDomainRef:    row.VercelDomainRef,
		CreatedAt:          row.CreatedAt,
		VerifiedAt:         row.VerifiedAt,
		VerificationToken:  row.VerificationToken,
	}
}

// domainResponse builds the API shape, including the two DNS records a
// claiming team must create. row.TeamID is nullable at the column level —
// null means the instance's shared hostname — so it is guarded here rather
// than dereferenced directly: no path in this package reaches this function
// with a null team, but a nil dereference would panic the whole request
// instead of failing it.
func domainResponse(row db.Domain, dnsTarget string) Domain {
	token := ""
	if row.VerificationToken != nil {
		token = *row.VerificationToken
	}
	teamID := uuid.UUID{}
	if row.TeamID != nil {
		teamID = *row.TeamID
	}
	return Domain{
		ID:                 row.ID,
		TeamID:             teamID,
		Hostname:           row.Hostname,
		VerificationStatus: row.VerificationStatus,
		VerificationToken:  token,
		VerifiedAt:         row.VerifiedAt,
		Records: DNSRecords{
			TXT:   DNSRecord{Name: domainverify.ChallengeName(row.Hostname), Value: token},
			CNAME: DNSRecord{Name: row.Hostname, Value: dnsTarget},
		},
	}
}
