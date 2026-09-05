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
	"unicode"

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

	// One row per PATCH, not one per changed field. doc 05 sketches a
	// link.destination_changed action, but a single PATCH can change several
	// fields atomically, and splitting that into several rows would
	// misrepresent one request as several. Which fields moved lives in
	// metadata.changed.
	ActionLinkCreated Action = "link.created"
	ActionLinkUpdated Action = "link.updated"
	ActionLinkDeleted Action = "link.deleted"

	// Folder and tag changes made through a link write do not get their own
	// action: they are part of that write's link.updated row, with the
	// affected fields in metadata.changed. The rule above — one row per PATCH,
	// not one per changed field — governs those too.
	ActionFolderCreated Action = "folder.created"
	ActionFolderUpdated Action = "folder.updated"
	ActionFolderDeleted Action = "folder.deleted"

	ActionTagCreated Action = "tag.created"
	ActionTagUpdated Action = "tag.updated"
	ActionTagDeleted Action = "tag.deleted"
)

// Entity types match the table names.
const (
	EntityTeam       = "team"
	EntityTeamMember = "team_member"
	EntityLink       = "link"
	EntityFolder     = "folder"
	EntityTag        = "tag"
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
	ActionLinkCreated:       {},
	ActionLinkUpdated:       {},
	ActionLinkDeleted:       {},
	ActionFolderCreated:     {},
	ActionFolderUpdated:     {},
	ActionFolderDeleted:     {},
	ActionTagCreated:        {},
	ActionTagUpdated:        {},
	ActionTagDeleted:        {},
}

// forbiddenMetadataKeys are the canonical, lowercase, singular words a
// metadata key may never carry. isForbiddenKey matches these
// case-insensitively against each word segment of a key (see keySegments),
// tolerating a trailing-"s" plural on the segment (see isForbiddenSegment),
// at every nesting level — so "Password", "password_hash",
// "new_password_hash", "newPasswordHash", "userIPAddress", "credentials",
// and a password buried inside a nested object or an array of objects are
// all caught, not just an exact top-level "password". Segment-based
// matching (rather than a raw substring check) deliberately keeps "ip" from
// also matching innocuous words that merely contain those two letters, such
// as "recipient" or "description".
var forbiddenMetadataKeys = map[string]struct{}{
	"password":   {},
	"pwd":        {},
	"credential": {},
	"hash":       {},
	"secret":     {},
	"token":      {},
	"ip":         {},
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

// CheckAction reports whether an action is part of the taxonomy. Log applies
// the same check; this exists so callers and tests can ask without writing.
func CheckAction(a Action) error {
	if _, ok := knownActions[a]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, a)
	}
	return nil
}

// Log writes the entry through q. Pass the *db.Queries that db.InTx handed to
// the callback, never the pool-backed one, or the entry will not share the
// mutation's transaction.
func Log(ctx context.Context, q *db.Queries, e Entry) error {
	if err := CheckAction(e.Action); err != nil {
		return err
	}
	if err := checkMetadata(e.Metadata); err != nil {
		return err
	}

	raw := `{}`
	if len(e.Metadata) > 0 {
		encoded, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("audit: encode metadata: %w", err)
		}
		raw = string(encoded)
	}

	teamID, actorUserID, entityID := e.TeamID, e.ActorUserID, e.EntityID
	if err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: &actorUserID,
		Action:      string(e.Action),
		EntityType:  e.EntityType,
		EntityID:    &entityID,
		Metadata:    &raw,
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

// isForbiddenKey reports whether key contains, as one of its word segments,
// one of forbiddenMetadataKeys — matched case-insensitively, and tolerant of
// a simple trailing-"s" plural. See keySegments for how a key is split into
// segments.
func isForbiddenKey(key string) bool {
	for _, segment := range keySegments(key) {
		if isForbiddenSegment(segment) {
			return true
		}
	}
	return false
}

// isForbiddenSegment reports whether segment (already lowercased by
// keySegments) is itself a forbidden word, or becomes one after stripping a
// single trailing "s". This catches the common plural of a forbidden word
// (e.g. "credentials", "tokens") without having to enumerate every plural in
// forbiddenMetadataKeys.
func isForbiddenSegment(segment string) bool {
	if _, ok := forbiddenMetadataKeys[segment]; ok {
		return true
	}
	if singular, ok := strings.CutSuffix(segment, "s"); ok {
		if _, ok := forbiddenMetadataKeys[singular]; ok {
			return true
		}
	}
	return false
}

// keySegments splits a metadata key into its lowercase word segments,
// breaking on:
//
//   - "_" (snake_case);
//   - a lower-or-digit-to-upper case boundary, i.e. the start of a
//     capitalized word in camelCase/PascalCase ("newPassword" -> "new" +
//     "Password");
//   - an upper-to-upper-then-lower boundary, i.e. the end of a run of
//     capitals that form an acronym immediately followed by a new
//     capitalized word ("userIPAddress" -> "user" + "IP" + "Address", not
//     "user" + "IPA" + "ddress" — the boundary lands one character before
//     the next word starts, so the acronym keeps all of its own letters).
//
// This normalizes "new_password_hash", "newPasswordHash",
// "NewPasswordHash" and "userIPAddress" to segment sets containing
// "password"/"hash" or "ip" respectively, so isForbiddenKey cannot be
// bypassed simply by writing a compound key in camelCase, PascalCase, or
// with an acronym run, instead of the schema's own snake_case.
func keySegments(key string) []string {
	var normalized strings.Builder
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case r == '_':
			normalized.WriteByte('_')
			continue
		case i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			// lower/digit -> upper: the start of a new capitalized word.
			normalized.WriteByte('_')
		case i > 0 && unicode.IsUpper(r) && unicode.IsUpper(runes[i-1]) &&
			i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			// upper -> upper, but the next rune is lowercase: r is the first
			// letter of the *next* word, not the last letter of the acronym
			// run, so the boundary goes before r.
			normalized.WriteByte('_')
		}
		normalized.WriteRune(unicode.ToLower(r))
	}
	return strings.Split(normalized.String(), "_")
}
