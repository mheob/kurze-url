package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Logout ends the CLI's own session at Supabase, with
// POST /auth/v1/logout?scope=local and the access token as the bearer.
//
// Local scope ends the session this token belongs to and no other: not the
// browser's, not another machine's. Measured against the local stack, the
// CLI's refresh fails afterwards while a browser's session still refreshes
// (see the spec's "Verified" table), so no separate revocation endpoint is
// needed. The access token must still be valid, which is the caller's to
// arrange: a rejected one cannot end its own session.
//
// Any 2xx is success. Anything else is an error carrying the status and
// nothing else, as the response may say more than the CLI should print. A
// nil hc gets a client with a 30 second timeout.
func Logout(ctx context.Context, hc *http.Client, supabaseURL, apikey, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, supabaseBase(supabaseURL)+"/auth/v1/logout?scope=local", nil)
	if err != nil {
		return fmt.Errorf("build the logout request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := apiKeyClient(hc, apikey).Do(req)
	if err != nil {
		return fmt.Errorf("end the session at the authorization server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Reading the body lets the connection be reused; a cap keeps a hostile
	// server from making this read forever.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the authorization server answered HTTP %d (%s) to the logout", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}
