// Package apiclient is the CLI's typed client over the part of the API's /v1
// surface it uses.
//
// It is written by hand rather than generated. The CLI is a separate Go
// module that imports nothing from the backend, and seven operations do not
// justify a generator in the build. What a generator would give, a guarantee
// that the client matches the API, comes from contract_test.go instead, which
// checks every path, query parameter and JSON name here against
// apps/api/openapi.json.
//
// Tokens arrive through TokenSource and are never stored, logged or put in
// an error here. The client retries in exactly one case, a 401, because a
// 401 means the API did nothing; after a network error, a timeout or a 5xx
// it cannot know whether a POST was applied, so it never sends one twice.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultTimeout bounds a request when the caller brings no HTTP client.
	// Without it a stalled connection would hang the command forever.
	defaultTimeout = 30 * time.Second

	// pageSize is the largest page the API serves (its per_page cap), so
	// listing everything takes as few requests as possible.
	pageSize = 100

	// maxBody caps what is read from one response. The API's largest answer
	// is a 2048-pixel PNG; the cap only exists so that a base URL pointed at
	// the wrong server cannot fill the machine's memory.
	maxBody = 16 << 20
)

// TokenSource hands the client its credentials without the client knowing
// where they live (the keychain, a file, a test).
type TokenSource interface {
	// AccessToken returns the current access token.
	AccessToken(ctx context.Context) (string, error)
	// Refresh obtains a new access token, after the API refused the current
	// one, and returns it.
	Refresh(ctx context.Context) (string, error)
}

// Options configures New.
type Options struct {
	// BaseURL is the API's origin, such as https://api.kurze-url.app.
	BaseURL string
	// HTTPClient sends the requests. Nil means a client with a 30 second
	// timeout.
	HTTPClient *http.Client
	// Tokens supplies the bearer token. It is required.
	Tokens TokenSource
	// VercelBypass is sent as x-vercel-protection-bypass when non-empty, for
	// preview deployments behind Vercel's deployment protection.
	VercelBypass string
	// UserAgent is sent as User-Agent when non-empty; otherwise net/http's
	// default applies.
	UserAgent string
}

// Client talks to the API. It is safe for concurrent use as long as its
// TokenSource is.
type Client struct {
	base      string
	http      *http.Client
	tokens    TokenSource
	bypass    string
	userAgent string
}

// New returns a client for the API at o.BaseURL.
func New(o Options) *Client {
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		// A trailing slash would turn "/v1/me" into "//v1/me".
		base:      strings.TrimRight(o.BaseURL, "/"),
		http:      hc,
		tokens:    o.Tokens,
		bypass:    o.VercelBypass,
		userAgent: o.UserAgent,
	}
}

// Me returns the signed-in user and their teams.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	err := c.getJSON(ctx, PathMe, nil, &me)
	return me, err
}

// Folders returns every folder of a team.
func (c *Client) Folders(ctx context.Context, teamID string) ([]Folder, error) {
	return listAll[Folder](ctx, c, PathTeamFolders, teamID)
}

// Domains returns every domain of a team.
func (c *Client) Domains(ctx context.Context, teamID string) ([]Domain, error) {
	return listAll[Domain](ctx, c, PathTeamDomains, teamID)
}

// CreateLink creates a link in a team and returns it. A failed call is never
// repeated, whatever the failure, because a request that died on the wire
// may still have created the link and a second one would create another.
func (c *Client) CreateLink(ctx context.Context, teamID string, r CreateLinkRequest) (Link, error) {
	path, err := expand(PathTeamLinks, paramTeamID, teamID)
	if err != nil {
		return Link{}, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return Link{}, fmt.Errorf("encode the link request: %w", err)
	}
	var link Link
	err = c.doJSON(ctx, call{method: http.MethodPost, path: path, body: body}, &link)
	return link, err
}

// ListLinks returns one page of a team's links. Unlike Folders it does not
// walk the pages: a team can hold thousands of links, and the caller decides
// how many it wants.
func (c *Client) ListLinks(ctx context.Context, teamID string, q ListLinksQuery) (Page[Link], error) {
	path, err := expand(PathTeamLinks, paramTeamID, teamID)
	if err != nil {
		return Page[Link]{}, err
	}
	query := url.Values{}
	setString(query, QueryQ, q.Q)
	setString(query, QueryFolderID, q.FolderID)
	setString(query, QuerySort, q.Sort)
	setPositive(query, QueryPage, q.Page)
	setPositive(query, QueryPerPage, q.PerPage)
	var page Page[Link]
	err = c.getJSON(ctx, path, query, &page)
	return page, err
}

// LinkStats returns a link's statistics. from and to are YYYY-MM-DD in UTC;
// either may be empty, and the API then applies its own window.
func (c *Client) LinkStats(ctx context.Context, linkID, from, to string) (LinkStats, error) {
	path, err := expand(PathLinkStats, paramLinkID, linkID)
	if err != nil {
		return LinkStats{}, err
	}
	query := url.Values{}
	setString(query, QueryFrom, from)
	setString(query, QueryTo, to)
	var stats LinkStats
	err = c.getJSON(ctx, path, query, &stats)
	return stats, err
}

// LinkQR returns a link's QR code as the raw bytes of an SVG or PNG image,
// whichever q.Format asked for. The API renders it; the CLI never generates
// one, so what is printed or saved is what the web app would download.
func (c *Client) LinkQR(ctx context.Context, linkID string, q QRQuery) ([]byte, error) {
	path, err := expand(PathLinkQR, paramLinkID, linkID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	setString(query, QueryFormat, q.Format)
	setPositive(query, QuerySize, q.Size)
	setString(query, QueryFG, q.FG)
	setString(query, QueryBG, q.BG)
	return c.do(ctx, call{method: http.MethodGet, path: path, query: query, accept: "image/*"})
}

// listAll reads every page of a team-scoped list endpoint. It stops once
// total_count items are in hand or a page comes back empty, whichever is
// first: the count ends a normal run without a request spent on an empty
// page, and the empty page stops a loop whose count a misbehaving server
// never lets the items reach.
func listAll[T any](ctx context.Context, c *Client, template, teamID string) ([]T, error) {
	path, err := expand(template, paramTeamID, teamID)
	if err != nil {
		return nil, err
	}
	// Never nil, so a command printing the result as JSON prints [] and not
	// null for a team with nothing in it.
	all := []T{}
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set(QueryPage, strconv.Itoa(page))
		query.Set(QueryPerPage, strconv.Itoa(pageSize))
		var got Page[T]
		if err := c.getJSON(ctx, path, query, &got); err != nil {
			return nil, err
		}
		all = append(all, got.Items...)
		if len(got.Items) == 0 || len(all) >= got.TotalCount {
			return all, nil
		}
	}
}

// call is one request, described independently of the attempt that sends it,
// so the retry after a 401 can build it again from the same bytes.
type call struct {
	method string
	// path is the template with its parameters already filled in.
	path  string
	query url.Values
	// body is the JSON to send, or nil for none. It is bytes and not a
	// reader because a reader is spent after the first attempt.
	body   []byte
	accept string
}

// getJSON runs a GET and decodes the JSON answer into out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.doJSON(ctx, call{method: http.MethodGet, path: path, query: query}, out)
}

// doJSON runs k and decodes the JSON answer into out.
func (c *Client) doJSON(ctx context.Context, k call, out any) error {
	body, err := c.do(ctx, k)
	if err != nil {
		return err
	}
	// A 2xx is not proof the API answered: Vercel serves a cancelled preview
	// deployment as an HTML page with a 200. The decode error says so, and
	// nothing from the body goes into it.
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode the API's answer to %s %s: %w", k.method, k.path, err)
	}
	return nil
}

// do sends k and returns the body of a 2xx answer. Any other status comes
// back as a *ProblemError.
//
// A 401 is the one answer that is repeated: the token was refused, so the API
// did nothing, and sending the request again with a refreshed token is safe
// even for a POST. The second answer is final, so a token the API refuses
// twice is reported and not refreshed in a loop.
func (c *Client) do(ctx context.Context, k call) ([]byte, error) {
	if c.tokens == nil {
		return nil, errors.New("the API client has no token source")
	}
	if k.accept == "" {
		k.accept = "application/json"
	}

	token, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get the access token: %w", err)
	}
	status, body, err := c.send(ctx, k, token)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		token, err = c.tokens.Refresh(ctx)
		if err != nil {
			return nil, fmt.Errorf("refresh the access token: %w", err)
		}
		status, body, err = c.send(ctx, k, token)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status > 299 {
		return nil, decodeProblem(status, body)
	}
	return body, nil
}

// send makes one attempt and returns the status and the whole body. A
// transport failure is returned as is, wrapped: the *url.Error inside names
// the URL, which carries no token (the token travels in a header), and
// errors.Is still finds context.DeadlineExceeded and the like through it.
func (c *Client) send(ctx context.Context, k call, token string) (int, []byte, error) {
	target := c.base + k.path
	if len(k.query) > 0 {
		target += "?" + k.query.Encode()
	}
	var reader io.Reader
	if k.body != nil {
		reader = bytes.NewReader(k.body)
	}
	req, err := http.NewRequestWithContext(ctx, k.method, target, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", k.accept)
	if k.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.bypass != "" {
		req.Header.Set("x-vercel-protection-bypass", c.bypass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send the request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, nil, fmt.Errorf("read the response: %w", err)
	}
	if len(body) > maxBody {
		return 0, nil, fmt.Errorf("read the response: more than %d bytes", maxBody)
	}
	return resp.StatusCode, body, nil
}

// expand fills one {name} placeholder of a path template with value, escaped
// into a single path segment. An empty value is refused: it would send
// /v1/teams//links, which is a different route and not an error the API
// reports as one.
func expand(template, name, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("the %s must not be empty", name)
	}
	return strings.Replace(template, "{"+name+"}", url.PathEscape(value), 1), nil
}

// setString adds a query parameter unless its value is empty.
func setString(q url.Values, name, value string) {
	if value != "" {
		q.Set(name, value)
	}
}

// setPositive adds a numeric query parameter unless its value is zero or
// negative, which is how a caller says "not set".
func setPositive(q url.Values, name string, value int) {
	if value > 0 {
		q.Set(name, strconv.Itoa(value))
	}
}
