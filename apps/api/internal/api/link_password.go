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
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// SetLinkPasswordInput declares its authorization in its type: LinkEditorScope
// resolves which team owns the link and requires at least the editor role, the
// same scope PATCH uses. A password is a property of a link, and whoever may
// change where a link points may also decide who reaches it.
//
// Password carries no minLength/maxLength: Huma would then reject an
// out-of-range value with its own error shape, and two of the policy's five
// reasons would be unreachable over HTTP. ValidatePassword owns all five, so
// every rejection has one shape for the frontend to read.
type SetLinkPasswordInput struct {
	authz.LinkEditorScope
	Body struct {
		Password string `json:"password" doc:"8 to 128 characters. Must not repeat one character, and must not be derived from the link's short path, its destination, or the Verein's name."`
	}
}

// setLinkPassword is PUT /v1/links/{link_id}/password.
//
// The link and the team are read BEFORE the transaction, and the hash is
// computed before it too. Argon2id holds 19 MiB for tens of milliseconds, and
// doing that inside a transaction would pin a pooled Postgres connection for
// the duration on a free tier where connections are the scarce thing. The cost
// is that the slug this validates against could change under us between the
// read and the write; the consequence of that race is a password judged
// against a slug one edit stale, which is not worth a longer transaction.
func (d Deps) setLinkPassword(ctx context.Context, in *SetLinkPasswordInput) (*LinkOutput, error) {
	member := in.Member()

	if err := d.allowPasswordSet(ctx, member.UserID); err != nil {
		return nil, err
	}

	before, err := d.Queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
		ID: in.Link().ID, TeamID: member.TeamID,
	})
	if err != nil {
		return nil, d.linkPasswordReadError(err, in.Link().ID)
	}

	team, err := d.Queries.GetTeam(ctx, member.TeamID)
	if err != nil {
		return nil, d.linkPasswordReadError(err, in.Link().ID)
	}

	if err := auth.ValidatePassword(in.Body.Password, auth.PolicyContext{
		LinkSlug:       before.Slug,
		DestinationURL: before.DestinationURL,
		TeamName:       team.Name,
		TeamSlug:       team.Slug,
	}); err != nil {
		return nil, passwordRejected(err)
	}

	hash, err := auth.HashPassword(in.Body.Password)
	if err != nil {
		d.Log.Error("hash link password", "error", err, "link_id", before.ID)
		return nil, huma.Error500InternalServerError("could not set the password")
	}

	// The set-vs-changed decision is read again here, inside the transaction,
	// immediately before the write it feeds. The pre-transaction `before` read
	// above is still needed for ValidatePassword's context (slug, destination),
	// but GetTeam, ValidatePassword and HashPassword run tens of milliseconds
	// between that read and this one, and two near-simultaneous requests could
	// otherwise both observe HasPassword=false and both write password_set.
	var updated linkRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		current, err := q.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
			ID: before.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}

		action := audit.ActionPasswordSet
		if current.HasPassword {
			action = audit.ActionPasswordChanged
		}

		row, err := q.SetLinkPassword(ctx, db.SetLinkPasswordParams{
			ID: before.ID, TeamID: member.TeamID, PasswordHash: &hash,
		})
		if err != nil {
			return err
		}
		updated = rowFromSetPassword(row)

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      action,
			EntityType:  audit.EntityLink,
			EntityID:    row.ID,
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The link was deleted between the pre-transaction read and this
		// transaction — mirror updateLink's mapping rather than logging an
		// error and paging the maintainer for an expected race.
		return nil, huma.Error404NotFound("link not found")
	case err != nil:
		d.Log.Error("set link password", "error", err, "link_id", before.ID)
		return nil, huma.Error500InternalServerError("could not set the password")
	}

	// link.Cached carries HasPassword, so without this the link keeps
	// redirecting straight through for up to LinkCacheTTL. The slug does not
	// change here, so unlike updateLink there is only ever one key.
	d.invalidateLink(ctx, updated.Hostname, updated.Slug)

	// linkResponse defaults Tags to []; without attachTags a link that already
	// carries tags would report them as gone the moment its password changes,
	// the same bug getLink and updateLink's no-tag-change branch guard against.
	items := []Link{d.linkResponse(updated)}
	if err := d.attachTags(ctx, member.TeamID, items); err != nil {
		d.Log.Error("attach tags to link", "error", err, "link_id", updated.ID)
		return nil, huma.Error500InternalServerError("could not set the password")
	}

	return &LinkOutput{Status: http.StatusOK, Body: items[0]}, nil
}

func (d Deps) linkPasswordReadError(err error, linkID uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return huma.Error404NotFound("link not found")
	}
	d.Log.Error("read link for password change", "error", err, "link_id", linkID)
	return huma.Error500InternalServerError("could not set the password")
}

// passwordRejected turns a policy sentinel into the 422 the frontend reads.
// The reason travels in ErrorDetail.Value keyed by the field, the convention
// deleteDomain established and CLAUDE.md records: the prose below stays free
// to reword, and apps/web keys its message off the token instead. The token is
// the sentinel's own Error() string — see policy_test.go, which pins that.
func passwordRejected(err error) error {
	messages := map[error]string{
		auth.ErrPasswordTooShort:      "the password must be at least 8 characters",
		auth.ErrPasswordTooLong:       "the password must be at most 128 characters",
		auth.ErrPasswordTooRepetitive: "the password must use at least 4 different characters",
		auth.ErrPasswordFromContext:   "the password must not be derived from the link, its destination, or the Verein's name",
		auth.ErrPasswordTooCommon:     "that password is too common",
	}
	for sentinel, message := range messages {
		if errors.Is(err, sentinel) {
			return huma.Error422UnprocessableEntity(message,
				&huma.ErrorDetail{Location: "body.password", Value: sentinel.Error()})
		}
	}
	return huma.Error422UnprocessableEntity("that password cannot be used",
		&huma.ErrorDetail{Location: "body.password", Value: "rejected"})
}

// allowPasswordSet caps the mutation per user. It fails closed on a Redis
// error, matching the rest of the password surface — the members path makes
// the same choice — rather than logging and allowing the way allowLinkCreate
// does. The nil-Cache guard is the local-development affordance only;
// REDIS_URL is required in every deployed environment.
func (d Deps) allowPasswordSet(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.PasswordSetRateLimitPerHour <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:password-set:"+userID.String(), d.Config.PasswordSetRateLimitPerHour, time.Hour)
	if err != nil {
		d.Log.Error("password set rate limit check failed", "error", err)
		return huma.Error500InternalServerError("could not check the password rate limit")
	}
	if !ok {
		return huma.Error429TooManyRequests("too many password changes; try again later")
	}
	return nil
}
