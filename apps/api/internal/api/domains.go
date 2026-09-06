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

// domainVerifier is the slice of domainverify this package needs to decide
// whether a claimed hostname may serve a team's links: does the published
// TXT token match, and does the hostname reach this API. Declared here, next
// to its consumer — the same as Inviter in api.go — so handler tests can
// substitute a stub without a real DNS lookup or TLS handshake.
// domainverify.NewVerifier's *Verifier, wired into Deps.DomainVerifier once
// in cmd/api/main.go, is the production implementation.
type domainVerifier interface {
	Check(ctx context.Context, hostname, token string) (domainverify.Reason, error)
}

// domainVerifyTimeout bounds the whole verify-domain request. Check's own
// HTTPS probe already carries its own 5s budget, but the DNS lookup ahead of
// it inherits only the context this handler passes in — without an explicit
// bound here, a Verein whose nameserver never answers could hold this
// request open indefinitely.
const domainVerifyTimeout = 10 * time.Second

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

// VerifyDomainInput declares its authorization in its type: DomainAdminScope
// resolves which team owns the domain and requires at least the admin
// role — the same threshold create-domain uses, since verifying decides
// which team's links a hostname actually serves.
type VerifyDomainInput struct {
	authz.DomainAdminScope
}

// VerifyDomainOutput carries the domain unchanged plus a reason. A failed
// check is not an error: under maintainer-in-the-loop provisioning,
// "unreachable" is the normal state until the maintainer has added the
// hostname to the Vercel project.
type VerifyDomainOutput struct {
	Body struct {
		Domain Domain `json:"domain"`
		// Reason is empty on success (including the already-verified
		// short-circuit) and one of domainverify's Reason values otherwise —
		// "token_missing", "token_mismatch" or "unreachable" — so a Verein
		// that cannot see which half failed cannot fix it.
		Reason string `json:"reason" enum:"token_missing,token_mismatch,unreachable"`
	}
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

	huma.Register(api, huma.Operation{
		OperationID: "verify-domain",
		Method:      http.MethodPost,
		Path:        "/v1/domains/{domain_id}/verify",
		Summary:     "Check a claimed domain's DNS token and reachability",
		Tags:        []string{"Domains"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.verifyDomain)
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

// allowDomainVerify rate-limits verification attempts on both axes: the
// domain being checked and the user calling. Each call costs a DNS lookup
// and a TLS connection to a third party, and either axis alone could be
// abused past a single limit — one member retrying rapidly on one domain, or
// one caller sweeping many domains. A Redis outage must not stop a Verein
// verifying a domain, the same choice allowLinkCreate makes: log and allow
// rather than fail the request when the limiter itself errors.
func (d Deps) allowDomainVerify(ctx context.Context, domainID, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.DomainVerifyRateLimitPerHour <= 0 {
		return nil
	}

	for _, key := range []string{
		"rl:domain-verify:domain:" + domainID.String(),
		"rl:domain-verify:user:" + userID.String(),
	} {
		ok, _, err := d.Cache.Allow(ctx, key, d.Config.DomainVerifyRateLimitPerHour, time.Hour)
		if err != nil {
			d.Log.Error("domain verify rate limit check failed", "error", err)
			continue
		}
		if !ok {
			return huma.Error429TooManyRequests("too many verification attempts; try again later")
		}
	}
	return nil
}

func (d Deps) verifyDomain(ctx context.Context, in *VerifyDomainInput) (*VerifyDomainOutput, error) {
	member := in.Member()
	domain := in.Domain()

	if err := d.allowDomainVerify(ctx, domain.ID, member.UserID); err != nil {
		return nil, err
	}

	// There is no RLS: this filters by team_id even though the scope already
	// authorized the caller for member.TeamID, the same reason getDomain
	// filters below it.
	row, err := d.Queries.GetDomainForTeam(ctx, db.GetDomainForTeamParams{
		ID: domain.ID, TeamID: member.TeamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("domain not found")
	}
	if err != nil {
		d.Log.Error("get domain", "error", err, "domain_id", domain.ID)
		return nil, huma.Error500InternalServerError("could not load the domain")
	}

	if row.VerificationStatus == "verified" {
		// No point paying for a DNS lookup and a TLS handshake to learn what
		// the row already says.
		out := &VerifyDomainOutput{}
		out.Body.Domain = domainResponse(row, d.Config.DomainDNSTarget)
		return out, nil
	}

	token := ""
	if row.VerificationToken != nil {
		token = *row.VerificationToken
	}

	// Check's own DNS lookup has no timeout of its own: its HTTPS probe
	// carries its own 5s budget, but LookupTXT inherits only the context
	// passed in here.
	checkCtx, cancel := context.WithTimeout(ctx, domainVerifyTimeout)
	defer cancel()

	reason, err := d.DomainVerifier.Check(checkCtx, row.Hostname, token)
	if err != nil {
		d.Log.Error("verify domain", "error", err, "domain_id", domain.ID)
		return nil, huma.Error500InternalServerError("could not verify the domain")
	}

	if reason != domainverify.ReasonNone {
		// "Not ready yet" is the expected answer, not a failure: under
		// maintainer-in-the-loop provisioning, "unreachable" is the normal
		// state until the maintainer has added the hostname to the Vercel
		// project. A 200 with the unchanged domain and the reason lets a
		// Verein see which half to fix.
		out := &VerifyDomainOutput{}
		out.Body.Domain = domainResponse(row, d.Config.DomainDNSTarget)
		out.Body.Reason = string(reason)
		return out, nil
	}

	var verified db.Domain
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		v, err := q.MarkDomainVerified(ctx, db.MarkDomainVerifiedParams{
			ID: domain.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}
		verified = v

		if err := q.FailCompetingClaims(ctx, db.FailCompetingClaimsParams{
			Hostname: v.Hostname, KeepID: v.ID,
		}); err != nil {
			return err
		}

		// hostname, never the token: audit_log.metadata rejects any key
		// whose word segments include "token".
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionDomainVerified,
			EntityType:  audit.EntityDomain,
			EntityID:    v.ID,
			Metadata:    map[string]any{"hostname": v.Hostname},
		})
	})

	switch {
	case isUniqueViolation(err):
		// Another team verified this hostname first. Both proved they
		// control the zone — which can happen legitimately during a
		// handover — and the index makes the second one lose rather than
		// producing two verified rows the redirect path would have to
		// choose between.
		return nil, huma.Error409Conflict("another team has already verified this hostname")
	case err != nil:
		d.Log.Error("verify domain", "error", err, "domain_id", domain.ID)
		return nil, huma.Error500InternalServerError("could not verify the domain")
	}

	out := &VerifyDomainOutput{}
	out.Body.Domain = domainResponse(verified, d.Config.DomainDNSTarget)
	return out, nil
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
