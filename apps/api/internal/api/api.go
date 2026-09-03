// Package api owns the HTTP shape of both surfaces: the Huma-registered /v1
// JSON API and the public, unversioned redirect surface. It issues no SQL and
// no Redis commands of its own — those belong to internal/db and internal/cache.
package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Inviter is the slice of Supabase's Admin API this package needs. It is
// declared here, next to its consumer, so handler tests can fake it without
// touching HTTP.
type Inviter interface {
	InviteUser(ctx context.Context, email string, data map[string]any) (uuid.UUID, error)
}

// Deps is everything the handlers need, constructed once in cmd/api.
type Deps struct {
	Config config.Config
	// SharedDomain is the instance's own short hostname. A link created with
	// no explicit domain_id lands here.
	SharedDomain SharedDomain
	Queries      *db.Queries
	Cache        *cache.Client
	Recorder     *analytics.Recorder
	Verifier     *auth.Verifier
	Log          *slog.Logger

	// Pool backs db.InTx. Queries above is pool-backed too, but a transaction
	// needs the pool itself.
	Pool *pgxpool.Pool

	// Admin sends team invitation emails. Nil disables the invite branch of
	// POST /v1/teams/{team_id}/members, which then refuses unknown addresses.
	Admin Inviter

	// Now is injectable so tests can pin expiry behaviour. Defaults to
	// time.Now when nil.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
