package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// defaultTimeout bounds a call when the caller brings no HTTP client.
	// Without it a stalled connection would hang the command forever.
	defaultTimeout = 30 * time.Second

	// maxMetadata caps what discovery reads. The document is a few hundred
	// bytes; the cap only keeps a base URL pointed at the wrong server from
	// filling the machine's memory.
	maxMetadata = 1 << 20

	// discoveryPath is where Supabase serves its authorization server
	// metadata. The RFC 8414 location, /.well-known/oauth-authorization-server
	// followed by the issuer's path, answers 404 there (see the spec's
	// "Verified" table).
	discoveryPath = "/auth/v1/.well-known/oauth-authorization-server"
)

// ErrNoS256 means the authorization server does not list S256 among its PKCE
// methods. The CLI never sends plain, which gives a captured authorization
// code nothing to stand in for the verifier, so it stops instead.
var ErrNoS256 = errors.New("the authorization server does not support PKCE with S256")

// Endpoints are the two URLs of the authorization server the CLI talks to.
type Endpoints struct {
	// Authorization is where the browser is sent.
	Authorization string
	// Token is where the code is exchanged and the refresh token spent.
	Token string
}

// Discover reads the authorization server's endpoints from the project's
// metadata document, so the CLI follows wherever Supabase moves them. It
// fails with ErrNoS256 when S256 is not among the supported PKCE methods.
//
// supabaseURL is the project URL. apikey is the project's publishable key,
// which every Supabase auth call carries. A nil hc gets a client with a
// 30 second timeout.
func Discover(ctx context.Context, hc *http.Client, supabaseURL, apikey string) (Endpoints, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, supabaseBase(supabaseURL)+discoveryPath, nil)
	if err != nil {
		return Endpoints{}, fmt.Errorf("build the discovery request: %w", err)
	}
	resp, err := apiKeyClient(hc, apikey).Do(req)
	if err != nil {
		return Endpoints{}, fmt.Errorf("fetch the authorization server's metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Endpoints{}, fmt.Errorf("the authorization server's metadata answered HTTP %d", resp.StatusCode)
	}

	var doc struct {
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadata)).Decode(&doc); err != nil {
		return Endpoints{}, fmt.Errorf("decode the authorization server's metadata: %w", err)
	}
	if !slices.Contains(doc.CodeChallengeMethods, "S256") {
		return Endpoints{}, ErrNoS256
	}
	if !isHTTPURL(doc.AuthorizationEndpoint) {
		return Endpoints{}, errors.New("the authorization server's metadata has no usable authorization_endpoint")
	}
	if !isHTTPURL(doc.TokenEndpoint) {
		return Endpoints{}, errors.New("the authorization server's metadata has no usable token_endpoint")
	}
	return Endpoints{Authorization: doc.AuthorizationEndpoint, Token: doc.TokenEndpoint}, nil
}

// isHTTPURL reports whether raw is an absolute http or https URL with a
// host. The endpoints come from a document the CLI fetched, and the token
// endpoint receives the refresh token, so an odd scheme is refused here and
// not discovered when a request fails.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// supabaseBase trims a trailing slash, so appending "/auth/v1/…" never
// produces a double one.
func supabaseBase(supabaseURL string) string {
	return strings.TrimRight(supabaseURL, "/")
}

// apiKeyClient returns a copy of hc that sends the project's publishable key
// as the apikey header on every request, redirects included, and refuses to
// follow a redirect.
//
// The hosted gateway may require the key (the local one does not, see the
// spec's "Verified" table), and it is public by design, so sending it on
// every call costs nothing. Redirects are refused because the token
// endpoint's request body is a refresh token or a code verifier: a 307 or 308
// would send it to whatever the Location names. The caller's own client is
// left as it was, and a CheckRedirect it sets is respected.
//
// A nil hc stands for a client with a 30 second timeout. A client the caller
// supplies keeps its own timeout, which may be none.
func apiKeyClient(hc *http.Client, apikey string) *http.Client {
	var c http.Client
	if hc == nil {
		c.Timeout = defaultTimeout
	} else {
		c = *hc
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = apiKeyTransport{base: base, apikey: apikey}
	if c.CheckRedirect == nil {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &c
}

// apiKeyTransport adds the apikey header to every request it carries.
type apiKeyTransport struct {
	base   http.RoundTripper
	apikey string
}

// RoundTrip clones the request before touching its headers, because a
// RoundTripper must not modify the request it is given.
func (t apiKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("apikey", t.apikey)
	return t.base.RoundTrip(r)
}
