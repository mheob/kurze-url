package audit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// TestLogDenylistCatchesSmugglingAttempts exercises the behaviour that
// distinguishes this package's metadata denylist from the brief's own flat,
// top-level, exact-match sample: recursion into nested maps and arrays of
// them, and segment matching that survives snake_case, camelCase, acronym
// runs, and simple plurals. TestLogRefusesPasswordishMetadata (in
// audit_test.go) only uses keys a naive exact-match check already catches,
// so it cannot tell this implementation apart from that baseline — these
// cases can.
func TestLogDenylistCatchesSmugglingAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	rejected := map[string]map[string]any{
		"nested map":              {"user": map[string]any{"password": "x"}},
		"map inside an array":     {"changes": []any{map[string]any{"secret": "x"}}},
		"compound snake_case key": {"new_password_hash": "x"},
		"compound camelCase key":  {"newPasswordHash": "x"},
		// userIPAddress must split as user/IP/Address, not user/I/P/Address —
		// otherwise the "ip" segment never appears and the key slips through.
		"camelCase key with a leading acronym run": {"userIPAddress": "x"},
		// authTokenID must split as auth/Token/ID; this exercises the same
		// acronym-run boundary landing in the middle of a longer compound.
		"camelCase key with a trailing acronym run": {"authTokenID": "x"},
		// credentials is the plural of the forbidden word "credential" and
		// must be caught without "credentials" being listed verbatim.
		"plural of a forbidden word": {"credentials": "x"},
	}
	for name, metadata := range rejected {
		t.Run(name, func(t *testing.T) {
			err := db.InTx(ctx, pool, func(q *db.Queries) error {
				return audit.Log(ctx, q, audit.Entry{
					TeamID:      teamID,
					ActorUserID: userID,
					Action:      audit.ActionTeamRenamed,
					EntityType:  audit.EntityTeam,
					EntityID:    teamID,
					Metadata:    metadata,
				})
			})
			require.ErrorIs(t, err, audit.ErrForbiddenMetadata, "metadata %#v", metadata)
		})
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from audit_log where team_id = $1`, teamID).Scan(&count))
	require.Zero(t, count, "every rejected entry above must leave no row behind")
}

// TestLogDenylistAllowsLegitimateKeys proves the recursive, segment-based,
// plural-tolerant check does not simply refuse everything: real taxonomy
// fields, keys that merely contain the letters "ip" as a substring of a
// longer word (rather than as a whole word segment), and plurals of
// legitimate (non-forbidden) words must all pass untouched. "roles",
// "tags" and "recipients" specifically probe the trailing-"s" plural rule
// added to catch "credentials": stripping their "s" yields "role", "tag"
// and "recipient", none of which are forbidden, so the plural rule must not
// start rejecting them.
func TestLogDenylistAllowsLegitimateKeys(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	metadata := map[string]any{
		"from":        "Alte Verein",
		"to":          "Neue Verein",
		"email":       "member@example.org",
		"old_role":    "editor",
		"new_role":    "admin",
		"name":        "Neue Verein",
		"description": "a description mentioning a recipient's tip",
		"recipient":   "someone",
		"roles":       []any{"editor", "admin"},
		"tags":        []any{"newsletter"},
		"recipients":  []any{"member@example.org"},
	}

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
			Metadata:    metadata,
		})
	}), "legitimate keys must not be rejected, including ones that merely contain \"ip\" as a substring "+
		"and plurals of non-forbidden words")
}
