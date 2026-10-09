package scanning

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	searchEndpoint = "https://safebrowsing.googleapis.com/v5/hashes:search"

	// maxPrefixesPerRequest keeps one request's URL short. hashes.search
	// itself accepts up to 1000 prefixes, but this is a GET: 1000 padded
	// base64 prefixes are about 26 KB of query string, past the 8 KB many
	// front ends refuse with 414. 250 keeps a request near 6.5 KB. One link
	// needs at most 60, 30 for each of the two readings lookupExpressions
	// takes of it; the sweep's batch needs a few thousand, which this splits.
	maxPrefixesPerRequest = 250

	// requestTimeout bounds one request. The callers bound the whole check
	// tighter where they must: two seconds on the redirect path, the sweep's
	// 25-second budget.
	requestTimeout = 5 * time.Second

	// maxResponseBytes caps what is read from Google. A response for 250
	// prefixes is a few kilobytes even when every one of them is listed.
	maxResponseBytes = 1 << 20

	// apiKeyHeader carries the key, never the URL: Go's *url.Error prints the
	// request URL into its text, and error text reaches the logs and Sentry.
	apiKeyHeader = "X-Goog-Api-Key"

	prefixSize = 4
)

// Client is the hashes.search implementation of Checker. Safe for concurrent
// use; build one per process.
type Client struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// NewClient builds a Client for one API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   apiKey,
		endpoint: searchEndpoint,
		http: &http.Client{
			Timeout: requestTimeout,
			// A redirect from Google's API is an error to report, not a
			// place to send the key.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type searchResponse struct {
	FullHashes []struct {
		FullHash        string `json:"fullHash"`
		FullHashDetails []struct {
			ThreatType string   `json:"threatType"`
			Attributes []string `json:"attributes"`
		} `json:"fullHashDetails"`
	} `json:"fullHashes"`
	CacheDuration string `json:"cacheDuration"`
}

// Check looks every URL up in as few requests as the prefix limit allows.
// Each URL's own expressions are hashed here (see lookupExpressions for why
// there are two sets of them), the prefixes of all of them are de-duplicated
// and sent, and a URL is reported for a threat type only when one of its own
// full hashes is among those Google returns: Google answers a prefix with
// every listed hash under it, so a returned hash proves nothing about a URL
// that merely shares the prefix.
func (c *Client) Check(ctx context.Context, urls []string) (map[string]Result, error) {
	results := make(map[string]Result, len(urls))

	own := make(map[string][][sha256.Size]byte, len(urls))
	seen := map[[prefixSize]byte]bool{}
	var prefixes []string
	for _, raw := range urls {
		if _, done := own[raw]; done {
			continue
		}
		expressions := lookupExpressions(raw)
		if len(expressions) == 0 {
			// No verdict for this one; see Checker.
			continue
		}
		hashes := fullHashes(expressions)
		own[raw] = hashes
		for _, hash := range hashes {
			prefix := [prefixSize]byte(hash[:prefixSize])
			if !seen[prefix] {
				seen[prefix] = true
				prefixes = append(prefixes, base64.StdEncoding.EncodeToString(prefix[:]))
			}
		}
	}
	if len(prefixes) == 0 {
		return results, nil
	}

	listed := map[[sha256.Size]byte][]string{}
	var validFor time.Duration
	for start := 0; start < len(prefixes); start += maxPrefixesPerRequest {
		response, err := c.search(ctx, prefixes[start:min(start+maxPrefixesPerRequest, len(prefixes))])
		if err != nil {
			return nil, err
		}
		// The shortest validity of all the requests: a URL's prefixes may
		// have been spread across several of them.
		if duration := parseCacheDuration(response.CacheDuration); start == 0 || duration < validFor {
			validFor = duration
		}
		for _, full := range response.FullHashes {
			hash, ok := decodeFullHash(full.FullHash)
			if !ok {
				continue
			}
			for _, detail := range full.FullHashDetails {
				if detail.ThreatType != "" && enforceable(detail.Attributes) {
					listed[hash] = append(listed[hash], detail.ThreatType)
				}
			}
		}
	}

	for raw, hashes := range own {
		var threats []string
		for _, hash := range hashes {
			threats = append(threats, listed[hash]...)
		}
		results[raw] = Result{ThreatTypes: uniqueSorted(threats), ValidFor: validFor}
	}
	return results, nil
}

// lookupExpressions is every expression raw is looked up under: the union of
// two readings of it, because they can disagree about the host. Google's rules
// unescape before they split, so they read "https://x%2F@bad.com/login" as
// the host "x". net/url, which destination.Validate accepted the URL with,
// reads bad.com, and so does the browser the redirect sends there. Matching
// Google's reading alone would let a listed host hide behind an escaped
// userinfo; matching net/url's alone would miss the spellings Google's rules
// exist to catch. Empty when neither reading finds a host: the URL then gets
// no verdict, which is why destination.Validate refuses such a URL through
// HasLookupHost.
func lookupExpressions(raw string) []string {
	var expressions []string
	if google, err := canonicalize(raw); err == nil {
		expressions = google.expressions()
	}
	if parsed, err := netURLReading(raw); err == nil {
		for _, expression := range parsed.expressions() {
			if !slices.Contains(expressions, expression) {
				expressions = append(expressions, expression)
			}
		}
	}
	return expressions
}

// HasLookupHost reports whether Check can judge raw at all: whether either
// reading lookupExpressions takes of it finds a host. net/url reads a host in
// "https://./" and in the ideographic and full-width dots ("https://。/"),
// while Google's rules map those dots to "." and then strip every dot, leaving
// nothing. A link to such a URL would never get a verdict, so it would stay at
// the head of the sweep's never-checked list forever, and enough of them
// would fill every batch. destination.Validate refuses it with this, so the
// rule is the canonicalizer's own and cannot drift from it.
func HasLookupHost(raw string) bool {
	return len(lookupExpressions(raw)) > 0
}

// netURLReading canonicalizes raw with net/url deciding where the host ends.
// The URL is rebuilt from the host net/url found and the path and query it
// kept escaped, leaving out the userinfo that let the two readings part, and
// only then put through Google's rules. u.Host keeps any port and an IPv6
// literal's brackets, which Hostname would drop; canonicalize drops the port
// itself. A missing scheme is read as http, as canonicalize reads it: the
// scheme is in no expression, but the rebuilt URL needs one for its host to
// be read as a host.
func netURLReading(raw string) (canonicalURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return canonicalURL{}, err
	}
	if u.Host == "" {
		return canonicalURL{}, ErrNoHost
	}
	rebuilt := cmp.Or(u.Scheme, "http") + "://" + u.Host + u.EscapedPath()
	if u.RawQuery != "" || u.ForceQuery {
		rebuilt += "?" + u.RawQuery
	}
	return canonicalize(rebuilt)
}

func (c *Client) search(ctx context.Context, prefixes []string) (searchResponse, error) {
	query := url.Values{"hashPrefixes": prefixes}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return searchResponse{}, fmt.Errorf("scanning: build hashes.search request: %w", err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error prints the request URL, and the query carries every
		// prefix: several kilobytes of base64 at 250 of them, in every log
		// line and Sentry event, saying nothing the endpoint does not. The
		// error itself is kept, so a timeout still reads as one.
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			requestErr.URL = c.endpoint
		}
		return searchResponse{}, fmt.Errorf("scanning: hashes.search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return searchResponse{}, refusal(resp)
	}

	body, err := readCapped(resp.Body)
	if err != nil {
		return searchResponse{}, err
	}
	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return searchResponse{}, fmt.Errorf("scanning: decode hashes.search response: %w", err)
	}
	return parsed, nil
}

// refusal turns a non-200 answer into an error carrying its status, never its
// body: a body is Google's to word, and error text reaches the logs and
// Sentry. The status decides before anything is read, so a refusal too large
// to read is still the refusal it is. Only a 403's body is read at all,
// because only there does the body say whether the quota is spent.
func refusal(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusForbidden && isResourceExhausted(resp.Body) {
		return fmt.Errorf("%w: hashes.search answered %d", ErrQuotaExceeded, resp.StatusCode)
	}
	return fmt.Errorf("scanning: hashes.search answered %d", resp.StatusCode)
}

// isResourceExhausted recognises the other shape a quota refusal takes in
// Google's APIs: a 403 whose google.rpc.Status is RESOURCE_EXHAUSTED. A body
// that cannot be read, or is too large to, does not say so.
func isResourceExhausted(body io.Reader) bool {
	raw, err := readCapped(body)
	if err != nil {
		return false
	}
	var problem struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	return json.Unmarshal(raw, &problem) == nil && problem.Error.Status == "RESOURCE_EXHAUSTED"
}

// readCapped reads a response body of at most maxResponseBytes and refuses a
// longer one.
func readCapped(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("scanning: read hashes.search response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("scanning: hashes.search response exceeds %d bytes", maxResponseBytes)
	}
	return raw, nil
}

// enforceable reports whether a detail may block a top-level redirect.
// CANARY means "do not enforce"; FRAME_ONLY means "enforce only on frames",
// and a redirect is not a frame.
func enforceable(attributes []string) bool {
	for _, attribute := range attributes {
		if attribute == "CANARY" || attribute == "FRAME_ONLY" {
			return false
		}
	}
	return true
}

// parseCacheDuration reads a protobuf Duration ("300s", "1.5s"). Anything
// missing, unreadable or negative is zero: a verdict that may not be relied
// on at all.
func parseCacheDuration(value string) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration < 0 {
		return 0
	}
	return duration
}

// decodeFullHash accepts every base64 alphabet protobuf's JSON mapping allows.
func decodeFullHash(encoded string) ([sha256.Size]byte, bool) {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		raw, err := encoding.DecodeString(encoded)
		if err == nil && len(raw) == sha256.Size {
			return [sha256.Size]byte(raw), true
		}
	}
	return [sha256.Size]byte{}, false
}

func fullHashes(expressions []string) [][sha256.Size]byte {
	out := make([][sha256.Size]byte, 0, len(expressions))
	for _, expression := range expressions {
		out = append(out, sha256.Sum256([]byte(expression)))
	}
	return out
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}
