// Package supabase talks to Supabase's Admin API. It exists for exactly one
// call — sending a team invitation email — because that is the only thing this
// backend cannot do in SQL. Identity reads go through internal/db instead.
package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrUserExists means the address already has an account, so the caller
	// should add the membership directly instead of inviting.
	ErrUserExists = errors.New("supabase: a user with that email already exists")

	// ErrNotConfigured means the base URL or the service-role key is missing.
	ErrNotConfigured = errors.New("supabase: admin api is not configured")
)

// Client is a minimal Admin API client.
type Client struct {
	baseURL        string
	serviceRoleKey string
	http           *http.Client
}

// NewClient builds a client for {baseURL}/invite. baseURL is the project's
// auth base — the same value as the JWT issuer.
func NewClient(baseURL, serviceRoleKey string) (*Client, error) {
	if baseURL == "" || serviceRoleKey == "" {
		return nil, ErrNotConfigured
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		serviceRoleKey: serviceRoleKey,
		http:           &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// InviteUser creates an unconfirmed auth.users row and sends the invitation
// email, returning the new user's ID. data becomes the invite's user metadata,
// which is how the team and role travel with the invitation.
func (c *Client) InviteUser(
	ctx context.Context, email string, data map[string]any,
) (uuid.UUID, error) {
	payload := map[string]any{"email": email}
	if len(data) > 0 {
		payload["data"] = data
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: encode invite: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/invite",
		bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: build invite request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Supabase wants the service-role key in both places.
	req.Header.Set("apikey", c.serviceRoleKey)
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)

	res, err := c.http.Do(req)
	if err != nil {
		// %w on the transport error is safe: net/http redacts nothing, but the
		// key is a header, never part of the URL.
		return uuid.Nil, fmt.Errorf("supabase: send invite: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Cap the read: an upstream that streams garbage must not exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: read invite response: %w", err)
	}

	if res.StatusCode >= http.StatusBadRequest {
		if isExistingUser(raw) {
			return uuid.Nil, ErrUserExists
		}
		// The body is echoed, the key is not — errors reach logs and Sentry.
		return uuid.Nil, fmt.Errorf("supabase: invite failed with status %d: %s",
			res.StatusCode, truncate(string(raw), maxErrorBodyLen))
	}

	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return uuid.Nil, fmt.Errorf("supabase: decode invite response: %w", err)
	}

	id, err := uuid.Parse(decoded.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: invite returned an unusable user id: %w", err)
	}
	return id, nil
}

// isExistingUser recognises the "already registered" answer. Supabase has
// changed both the status code and the wording across versions, so match on
// the stable error_code first and the message as a fallback.
func isExistingUser(body []byte) bool {
	var decoded struct {
		ErrorCode string `json:"error_code"`
		Msg       string `json:"msg"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		if decoded.ErrorCode == "email_exists" || decoded.ErrorCode == "user_already_exists" {
			return true
		}
		for _, msg := range []string{decoded.Msg, decoded.Message} {
			if strings.Contains(strings.ToLower(msg), "already been registered") ||
				strings.Contains(strings.ToLower(msg), "already exists") {
				return true
			}
		}
	}
	return false
}

// maxErrorBodyLen caps how much of an upstream error body reaches a wrapped
// error — enough to diagnose, not enough to smuggle an unbounded payload
// into logs or Sentry.
const maxErrorBodyLen = 200

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
