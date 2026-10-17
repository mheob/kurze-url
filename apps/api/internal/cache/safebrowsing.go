package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// threatConfirmationPrefix keys the short-lived record that Google confirmed a
// flagged link's threats. Google's terms forbid blocking on a verdict older
// than thirty minutes, so link.state = 'flagged' alone never shows the block
// page: the redirect path shows it only while this key exists, and asks Google
// again when it does not. The value is the confirmed threat types,
// comma-separated, which the block page needs to say what was found.
//
// Only flagged links ever read it, so an active link's redirect costs nothing
// new. A flagged link costs one GET per redirect, plus one SET per re-check.
const threatConfirmationPrefix = "sb:confirmed:"

// errUnusableConfirmation refuses a confirmation that would outlive the terms'
// window or name no threat.
var errUnusableConfirmation = errors.New(
	"cache: a threat confirmation needs at least one threat type and a positive ttl")

// ConfirmThreats records that Google confirmed threatTypes for linkID just
// now, for ttl. One Redis command (SET with an expiry).
//
// A ttl that is not positive is refused rather than written: go-redis reads a
// zero expiration as "keep forever", and a confirmation that never expires is
// a block on data the terms call stale. The caller decides what not writing
// means; see api.confirmThreats.
func (c *Client) ConfirmThreats(
	ctx context.Context, linkID string, threatTypes []string, ttl time.Duration,
) error {
	if ttl <= 0 || len(threatTypes) == 0 {
		return errUnusableConfirmation
	}
	value := strings.Join(threatTypes, ",")
	if err := c.rdb.Set(ctx, c.Key(threatConfirmationPrefix+linkID), value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: confirm threats: %w", err)
	}
	return nil
}

// ThreatConfirmation reads a link's confirmation back. One Redis command
// (GET). ok is false when there is none, and also when the stored value names
// no threat type, which only a hand-written key could produce and which must
// not block anything.
func (c *Client) ThreatConfirmation(ctx context.Context, linkID string) ([]string, bool, error) {
	raw, err := c.rdb.Get(ctx, c.Key(threatConfirmationPrefix+linkID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache: read threat confirmation: %w", err)
	}

	var threats []string
	for _, threat := range strings.Split(raw, ",") {
		if threat != "" {
			threats = append(threats, threat)
		}
	}
	return threats, len(threats) > 0, nil
}

// ClearThreatConfirmation drops a link's confirmation. One Redis command
// (DEL).
func (c *Client) ClearThreatConfirmation(ctx context.Context, linkID string) error {
	if err := c.rdb.Del(ctx, c.Key(threatConfirmationPrefix+linkID)).Err(); err != nil {
		return fmt.Errorf("cache: clear threat confirmation: %w", err)
	}
	return nil
}
