package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrHostnameClaimed means the configured shared hostname is already some
// team's verified custom domain. Starting anyway would let every team create
// links on a hostname that team owns, so startup fails instead.
var ErrHostnameClaimed = errors.New("api: the shared hostname is already a team's custom domain")

// SharedDomain is the instance's own short hostname, resolved once at boot.
// Holding it on Deps means creating a link without an explicit domain_id
// costs no extra query.
type SharedDomain struct {
	ID       uuid.UUID
	Hostname string
}

// ProvisionSharedDomain makes sure the configured shared hostname exists as a
// verified, team-less domain row, and returns it. It is idempotent and runs on
// every boot.
func ProvisionSharedDomain(
	ctx context.Context, queries *db.Queries, hostname string,
) (SharedDomain, error) {
	host := strings.ToLower(strings.TrimSpace(hostname))
	if host == "" {
		return SharedDomain{}, errors.New("api: the shared domain hostname is empty")
	}

	row, err := queries.UpsertSharedDomain(ctx, host)
	if errors.Is(err, pgx.ErrNoRows) {
		return SharedDomain{}, fmt.Errorf("%w: %s", ErrHostnameClaimed, host)
	}
	if err != nil {
		return SharedDomain{}, fmt.Errorf("api: provision the shared domain: %w", err)
	}

	return SharedDomain{ID: row.ID, Hostname: row.Hostname}, nil
}
