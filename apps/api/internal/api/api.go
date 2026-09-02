// Package api owns the HTTP shape of both surfaces: the Huma-registered /v1
// JSON API and the public, unversioned redirect surface. It issues no SQL and
// no Redis commands of its own — those belong to internal/db and internal/cache.
package api

import (
	"log/slog"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Deps is everything the handlers need, constructed once in cmd/api.
type Deps struct {
	Config   config.Config
	Queries  *db.Queries
	Cache    *cache.Client
	Recorder *analytics.Recorder
	Log      *slog.Logger

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
