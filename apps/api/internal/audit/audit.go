// Package audit owns the audit_log write path and the action taxonomy. Every
// mutating endpoint records exactly one entry, in the same transaction as the
// mutation itself, so the log cannot disagree with the data.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Action is an audit_log.action value: one per mutating endpoint, shaped
// "entity.verb". Plan 3 adds domain.*, folder.*, tag.* and link.* here
// alongside the endpoints that emit them.
type Action string

// The taxonomy this plan defines. Plan 3 adds more alongside the endpoints
// that emit them.
const (
	ActionTeamCreated       Action = "team.created"
	ActionTeamRenamed       Action = "team.renamed"
	ActionMemberInvited     Action = "team_member.invited"
	ActionMemberAdded       Action = "team_member.added"
	ActionMemberRoleChanged Action = "team_member.role_changed"
	ActionMemberRemoved     Action = "team_member.removed"
)

// Entity types match the table names.
const (
	EntityTeam       = "team"
	EntityTeamMember = "team_member"
)

var (
	// ErrUnknownAction guards against typos: an action outside the taxonomy is
	// a bug, and a silently-written unknown value makes the log unqueryable.
	ErrUnknownAction = errors.New("audit: action is not part of the taxonomy")

	// ErrForbiddenMetadata enforces the schema comment on audit_log.metadata:
	// it never carries a plaintext password, a password hash, or an IP address.
	ErrForbiddenMetadata = errors.New("audit: metadata may not carry secrets or IP addresses")
)

var knownActions = map[Action]struct{}{
	ActionTeamCreated:       {},
	ActionTeamRenamed:       {},
	ActionMemberInvited:     {},
	ActionMemberAdded:       {},
	ActionMemberRoleChanged: {},
	ActionMemberRemoved:     {},
}

// forbiddenMetadataKeys are the canonical, lowercase words a metadata key may
// never carry. checkMetadata matches these case-insensitively, against the
// whole key and against each "_"-separated segment of it, at every nesting
// level — so "Password", "password_hash", "new_password_hash", and a
// password buried inside a nested object are all caught, not just an exact
// top-level "password". Segment-based matching (rather than a raw substring
// check) deliberately keeps "ip" from also matching innocuous words that
// merely contain those two letters, such as "recipient" or "description".
var forbiddenMetadataKeys = map[string]struct{}{
	"password": {},
	"hash":     {},
	"secret":   {},
	"token":    {},
	"ip":       {},
}

// Entry is one audit record. Every field is required except Metadata.
type Entry struct {
	TeamID      uuid.UUID
	ActorUserID uuid.UUID
	Action      Action
	EntityType  string
	EntityID    uuid.UUID
	Metadata    map[string]any
}

// Log writes the entry through q. Pass the *db.Queries that db.InTx handed to
// the callback, never the pool-backed one, or the entry will not share the
// mutation's transaction.
func Log(ctx context.Context, q *db.Queries, e Entry) error {
	if _, ok := knownActions[e.Action]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}
	if err := checkMetadata(e.Metadata); err != nil {
		return err
	}

	raw := []byte(`{}`)
	if len(e.Metadata) > 0 {
		encoded, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("audit: encode metadata: %w", err)
		}
		raw = encoded
	}

	teamID, actorUserID, entityID := e.TeamID, e.ActorUserID, e.EntityID
	if err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: &actorUserID,
		Action:      string(e.Action),
		EntityType:  e.EntityType,
		EntityID:    &entityID,
		Metadata:    raw,
	}); err != nil {
		return fmt.Errorf("audit: insert entry: %w", err)
	}
	return nil
}

// checkMetadata walks metadata recursively — into nested objects and arrays
// of them — so a forbidden key cannot be smuggled past the check by burying
// it under an unrelated top-level key.
func checkMetadata(metadata map[string]any) error {
	for key, value := range metadata {
		if isForbiddenKey(key) {
			return fmt.Errorf("%w: %q", ErrForbiddenMetadata, key)
		}
		if err := checkMetadataValue(value); err != nil {
			return err
		}
	}
	return nil
}

// checkMetadataValue descends into a metadata value looking for nested keys
// to check. Only maps (objects) and slices (arrays) can carry keys; every
// other JSON-ish value (string, number, bool, nil) is a leaf.
func checkMetadataValue(value any) error {
	switch v := value.(type) {
	case map[string]any:
		return checkMetadata(v)
	case []any:
		for _, item := range v {
			if err := checkMetadataValue(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// isForbiddenKey reports whether key is, or contains as a "_"-separated
// segment, one of forbiddenMetadataKeys — matched case-insensitively.
func isForbiddenKey(key string) bool {
	lower := strings.ToLower(key)
	if _, ok := forbiddenMetadataKeys[lower]; ok {
		return true
	}
	for _, segment := range strings.Split(lower, "_") {
		if _, ok := forbiddenMetadataKeys[segment]; ok {
			return true
		}
	}
	return false
}
