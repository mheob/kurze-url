package authz

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// TestResolvedDomainFrom exercises the row-to-domain mapping directly. This
// is the branch the task-6 follow-up found untested: the fake-driven
// TestDomainAdminScope in domain_test.go injects ErrDomainNotFound straight
// into fakeDomainResolver and never calls resolvedDomainFrom, so a broken
// null-team_id check there was invisible to the rest of the suite.
func TestResolvedDomainFrom(t *testing.T) {
	t.Run("a non-nil team_id resolves", func(t *testing.T) {
		domainID, teamID := uuid.New(), uuid.New()

		got, err := resolvedDomainFrom(db.GetDomainScopeRow{ID: domainID, TeamID: &teamID})

		require.NoError(t, err)
		require.Equal(t, ResolvedDomain{ID: domainID, TeamID: teamID}, got)
	})

	t.Run("a nil team_id is the shared hostname and is not found", func(t *testing.T) {
		// domain.team_id is null for the instance's shared hostname. Treating
		// that as ErrDomainNotFound, not the zero UUID, is what keeps the
		// shared domain from being reachable — and deletable — through
		// /v1/domains/{id} by any team that happens to share its zero-value ID.
		_, err := resolvedDomainFrom(db.GetDomainScopeRow{ID: uuid.New(), TeamID: nil})

		require.ErrorIs(t, err, ErrDomainNotFound)
	})
}
