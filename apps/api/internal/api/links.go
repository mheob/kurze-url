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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/destination"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	slugpkg "github.com/mheob/kurze-url/apps/api/internal/slug"
)

// generatedSlugAttempts bounds the retry loop. At 32^8 combinations, five
// collisions in a row means something is wrong that a sixth draw will not fix.
const generatedSlugAttempts = 5

// Link is a link as the API reports it. password_hash never appears here in
// any form; HasPassword is the only projection of it that leaves the database.
type Link struct {
	ID               uuid.UUID  `json:"id"`
	TeamID           uuid.UUID  `json:"team_id"`
	DomainID         uuid.UUID  `json:"domain_id"`
	Hostname         string     `json:"hostname"`
	Slug             string     `json:"slug"`
	ShortURL         string     `json:"short_url"`
	DestinationURL   string     `json:"destination_url"`
	RedirectType     int        `json:"redirect_type"`
	State            string     `json:"state"`
	ExpiresAt        *time.Time `json:"expires_at"`
	HasPassword      bool       `json:"has_password"`
	AnalyticsEnabled bool       `json:"analytics_enabled"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// linkRow is the shape every link query returns. sqlc generates a distinct Go
// type per query even when the columns are identical, so the converters below
// funnel all of them through one place — and one place is where short_url gets
// composed.
type linkRow struct {
	ID               uuid.UUID
	TeamID           uuid.UUID
	DomainID         uuid.UUID
	Hostname         string
	Slug             string
	DestinationURL   string
	RedirectType     int16
	State            string
	ExpiresAt        *time.Time
	HasPassword      bool
	AnalyticsEnabled bool
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (d Deps) linkResponse(r linkRow) Link {
	return Link{
		ID:               r.ID,
		TeamID:           r.TeamID,
		DomainID:         r.DomainID,
		Hostname:         r.Hostname,
		Slug:             r.Slug,
		ShortURL:         fmt.Sprintf("%s://%s/%s", d.Config.ShortURLScheme, r.Hostname, r.Slug),
		DestinationURL:   r.DestinationURL,
		RedirectType:     int(r.RedirectType),
		State:            r.State,
		ExpiresAt:        r.ExpiresAt,
		HasPassword:      r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled,
		CreatedBy:        r.CreatedBy,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func rowFromCreate(r db.CreateLinkRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromGet(r db.GetLinkForAPIRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromUpdate(r db.UpdateLinkRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromList(r db.ListLinksForTeamRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// selfHostnames is the set of hostnames a destination may not point at,
// because doing so is a redirect loop.
func (d Deps) selfHostnames() []string {
	return []string{d.Config.SharedDomainHostname, d.Config.APIHostname}
}

// invalidateLink drops a link's redirect-cache entry after its transaction has
// committed. Best-effort by design: the database is the source of truth and
// LinkCacheTTL bounds the staleness, so a Redis failure here is worth an error
// log, not a failed request whose write already landed.
func (d Deps) invalidateLink(ctx context.Context, hostname, slug string) {
	if d.Cache == nil {
		return
	}
	if err := d.Cache.InvalidateLink(ctx, link.CacheKey(hostname, slug)); err != nil {
		d.Log.Error("invalidate redirect cache",
			"error", err, "hostname", hostname, "slug", slug)
	}
}

// resolveLinkDomain answers which domain a new link goes on. No domain_id
// means the shared one, which costs no query because it was resolved at boot.
func (d Deps) resolveLinkDomain(
	ctx context.Context, teamID uuid.UUID, requested *uuid.UUID,
) (uuid.UUID, string, error) {
	if requested == nil {
		if d.SharedDomain.ID == uuid.Nil {
			return uuid.Nil, "", huma.Error500InternalServerError("no shared domain is configured")
		}
		return d.SharedDomain.ID, d.SharedDomain.Hostname, nil
	}

	row, err := d.Queries.GetLinkableDomain(ctx, db.GetLinkableDomainParams{
		ID: *requested, TeamID: teamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// One message for "unverified", "another team's" and "no such domain".
		// Telling them apart would confirm the existence of a hostname the
		// caller does not own.
		return uuid.Nil, "", huma.Error422UnprocessableEntity("that domain is not available to this team")
	}
	if err != nil {
		d.Log.Error("resolve link domain", "error", err)
		return uuid.Nil, "", huma.Error500InternalServerError("could not resolve the domain")
	}
	return row.ID, row.Hostname, nil
}

// isUniqueViolation reports whether err is Postgres' 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// CreateLinkInput declares its authorization in its type: EditorScope resolves
// and checks the caller's role before this handler's body runs.
type CreateLinkInput struct {
	authz.EditorScope
	Body struct {
		DestinationURL   string     `json:"destination_url" maxLength:"2048" doc:"Where the link points. https:// only."`
		Slug             string     `json:"slug,omitempty" maxLength:"64" doc:"Optional custom alias. Lowercased on input; generated when omitted."`
		DomainID         *uuid.UUID `json:"domain_id,omitempty" doc:"Optional. Defaults to the instance's shared domain."`
		RedirectType     int        `json:"redirect_type,omitempty" enum:"301,302" default:"302" doc:"301 is cached by browsers: clicks go uncounted and destination changes stop taking effect."`
		ExpiresAt        *time.Time `json:"expires_at,omitempty" doc:"Must be in the future."`
		AnalyticsEnabled *bool      `json:"analytics_enabled,omitempty" doc:"Defaults to true."`
	}
}

type LinkOutput struct {
	Status int
	Body   Link
}

func (d Deps) registerLinks(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-link",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/links",
		Summary:       "Create a link",
		Tags:          []string{"Links"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createLink)
}

func (d Deps) createLink(ctx context.Context, in *CreateLinkInput) (*LinkOutput, error) {
	member := in.Member()

	if err := d.allowLinkCreate(ctx, member.UserID); err != nil {
		return nil, err
	}

	if err := destination.Validate(in.Body.DestinationURL, d.selfHostnames()); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	if in.Body.ExpiresAt != nil && !in.Body.ExpiresAt.After(d.now()) {
		return nil, huma.Error422UnprocessableEntity("expires_at must be in the future")
	}

	// hostname is unused here: CreateLink's own query joins domain and returns
	// it on created.Hostname, which is what invalidateLink and the response
	// actually use.
	domainID, _, err := d.resolveLinkDomain(ctx, member.TeamID, in.Body.DomainID)
	if err != nil {
		return nil, err
	}

	custom := slugpkg.Normalize(in.Body.Slug)
	if custom != "" {
		if err := slugpkg.Validate(custom); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
	}

	redirectType := in.Body.RedirectType
	if redirectType == 0 {
		redirectType = http.StatusFound
	}
	analyticsEnabled := true
	if in.Body.AnalyticsEnabled != nil {
		analyticsEnabled = *in.Body.AnalyticsEnabled
	}

	var created db.CreateLinkRow
	for attempt := range generatedSlugAttempts {
		candidate := custom
		if candidate == "" {
			candidate, err = slugpkg.Generate()
			if err != nil {
				d.Log.Error("generate slug", "error", err)
				return nil, huma.Error500InternalServerError("could not create the link")
			}
		}

		err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
			row, err := q.CreateLink(ctx, db.CreateLinkParams{
				DomainID:         domainID,
				TeamID:           member.TeamID,
				Slug:             candidate,
				DestinationURL:   in.Body.DestinationURL,
				RedirectType:     int16(redirectType),
				ExpiresAt:        in.Body.ExpiresAt,
				AnalyticsEnabled: analyticsEnabled,
				CreatedBy:        member.UserID,
			})
			if err != nil {
				return err
			}
			created = row

			return audit.Log(ctx, q, audit.Entry{
				TeamID:      member.TeamID,
				ActorUserID: member.UserID,
				Action:      audit.ActionLinkCreated,
				EntityType:  audit.EntityLink,
				EntityID:    row.ID,
				Metadata: map[string]any{
					"slug":            row.Slug,
					"hostname":        row.Hostname,
					"destination_url": row.DestinationURL,
					"redirect_type":   int(row.RedirectType),
				},
			})
		})

		switch {
		case err == nil:
			// Creating a link must clear the redirect cache, not only changing
			// one: a probe of this slug before it existed may have stored the
			// not-found sentinel under exactly this key.
			d.invalidateLink(ctx, created.Hostname, created.Slug)
			return &LinkOutput{Status: http.StatusCreated, Body: d.linkResponse(rowFromCreate(created))}, nil

		case isUniqueViolation(err) && custom != "":
			// The caller asked for this exact slug, so there is nothing to
			// retry. On the shared hostname this does disclose that some other
			// team holds it — inherent to one shared namespace.
			return nil, huma.Error409Conflict("that slug is already taken on this domain")

		case isUniqueViolation(err):
			continue

		default:
			d.Log.Error("create link", "error", err, "attempt", attempt)
			return nil, huma.Error500InternalServerError("could not create the link")
		}
	}

	d.Log.Error("exhausted slug generation attempts", "attempts", generatedSlugAttempts)
	return nil, huma.Error500InternalServerError("could not create the link")
}

// allowLinkCreate applies the per-user creation limit. The subject is the
// authenticated user, so no IP is involved. A Redis failure degrades open: an
// outage in the rate limiter must not stop a Verein publishing a link.
func (d Deps) allowLinkCreate(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.LinkCreateRateLimitPerMin <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:link-create:"+userID.String(), d.Config.LinkCreateRateLimitPerMin, time.Minute)
	if err != nil {
		d.Log.Error("link create rate limit check failed", "error", err)
		return nil
	}
	if !ok {
		return huma.Error429TooManyRequests("too many links created; try again shortly")
	}
	return nil
}
