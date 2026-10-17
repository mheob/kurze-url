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
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
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

// searchResponse is a SearchHashesResponse, as
// google/security/safebrowsing/v5/safebrowsing.proto defines it, with the
// enums already turned into the names the rest of the app stores.
type searchResponse struct {
	FullHashes    []fullHash
	CacheDuration time.Duration
}

type fullHash struct {
	Hash    []byte
	Details []fullHashDetail
}

type fullHashDetail struct {
	ThreatType string
	Attributes []string
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
		// have been spread across several of them. A negative one may not be
		// relied on at all.
		if duration := max(response.CacheDuration, 0); start == 0 || duration < validFor {
			validFor = duration
		}
		for _, full := range response.FullHashes {
			if len(full.Hash) != sha256.Size {
				continue
			}
			hash := [sha256.Size]byte(full.Hash)
			for _, detail := range full.Details {
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
	// v5 answers only in binary protobuf. Asking for JSON through Google's
	// $alt=json system parameter is refused with 400 "Unsupported Output
	// Format", which production found out on 2026-10-17.
	req.Header.Set("Accept", "application/x-protobuf")

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
	parsed, err := decodeSearchResponse(body)
	if err != nil {
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
// Google's APIs: a 403 whose google.rpc.Status is RESOURCE_EXHAUSTED. v5 sends
// that Status as protobuf, where RESOURCE_EXHAUSTED is code 8; the JSON shape
// is read as well, which Google's front end answers in before the request
// reaches the API. A body that cannot be read, or is too large to, does not
// say so.
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
	if json.Unmarshal(raw, &problem) == nil {
		return problem.Error.Status == "RESOURCE_EXHAUSTED"
	}
	var code uint64
	err = eachField(raw, func(number protowire.Number, kind protowire.Type, _ []byte, value uint64) error {
		if number == 1 && kind == protowire.VarintType {
			code = value
		}
		return nil
	})
	return err == nil && code == codeResourceExhausted
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

// codeResourceExhausted is google.rpc.Code's RESOURCE_EXHAUSTED.
const codeResourceExhausted = 8

// The enum values of google/security/safebrowsing/v5/safebrowsing.proto,
// under the names the rest of the app stores.
var (
	threatTypeNames = map[uint64]string{
		1: "MALWARE",
		2: "SOCIAL_ENGINEERING",
		3: "UNWANTED_SOFTWARE",
		4: "POTENTIALLY_HARMFUL_APPLICATION",
	}
	threatAttributeNames = map[uint64]string{
		1: "CANARY",
		2: "FRAME_ONLY",
	}
)

// decodeSearchResponse reads a SearchHashesResponse. Three small messages
// and two enums are all hashes.search answers with, so they are read field by
// field rather than through generated code, and a field this reader does not
// know is skipped, as protobuf requires of every reader. A message cut off or
// otherwise malformed is an error, never an empty answer: "nothing listed"
// read out of half a message would be a verdict nobody gave.
func decodeSearchResponse(raw []byte) (searchResponse, error) {
	var out searchResponse
	err := eachField(raw, func(number protowire.Number, kind protowire.Type, payload []byte, _ uint64) error {
		if kind != protowire.BytesType {
			return nil
		}
		switch number {
		case 1:
			full, err := decodeFullHash(payload)
			if err != nil {
				return err
			}
			out.FullHashes = append(out.FullHashes, full)
		case 2:
			duration, err := decodeDuration(payload)
			if err != nil {
				return err
			}
			out.CacheDuration = duration
		}
		return nil
	})
	return out, err
}

func decodeFullHash(raw []byte) (fullHash, error) {
	var out fullHash
	err := eachField(raw, func(number protowire.Number, kind protowire.Type, payload []byte, _ uint64) error {
		if kind != protowire.BytesType {
			return nil
		}
		switch number {
		case 1:
			out.Hash = payload
		case 2:
			detail, err := decodeFullHashDetail(payload)
			if err != nil {
				return err
			}
			out.Details = append(out.Details, detail)
		}
		return nil
	})
	return out, err
}

// decodeFullHashDetail reads one threat type and its attributes. A value this
// app does not know keeps a name of its own, THREAT_TYPE_<n> or
// THREAT_ATTRIBUTE_<n>, rather than being dropped: Google tells clients to
// tolerate values they do not know, and dropping a threat type would turn a
// report into "clean". THREAT_TYPE_UNSPECIFIED is no threat at all.
func decodeFullHashDetail(raw []byte) (fullHashDetail, error) {
	var out fullHashDetail
	attribute := func(value uint64) {
		out.Attributes = append(out.Attributes, enumName(threatAttributeNames, "THREAT_ATTRIBUTE_", value))
	}
	err := eachField(raw, func(number protowire.Number, kind protowire.Type, payload []byte, value uint64) error {
		switch {
		case number == 1 && kind == protowire.VarintType && value != 0:
			out.ThreatType = enumName(threatTypeNames, "THREAT_TYPE_", value)
		case number == 2 && kind == protowire.VarintType:
			attribute(value)
		case number == 2 && kind == protowire.BytesType:
			// proto3 packs repeated enums by default.
			for len(payload) > 0 {
				packed, n := protowire.ConsumeVarint(payload)
				if n < 0 {
					return protowire.ParseError(n)
				}
				attribute(packed)
				payload = payload[n:]
			}
		}
		return nil
	})
	return out, err
}

// decodeDuration reads a google.protobuf.Duration. A value too large for
// time.Duration wraps negative, which the caller reads as "do not rely on
// this", the safe direction.
func decodeDuration(raw []byte) (time.Duration, error) {
	var seconds, nanos int64
	err := eachField(raw, func(number protowire.Number, kind protowire.Type, _ []byte, value uint64) error {
		if kind != protowire.VarintType {
			return nil
		}
		switch number {
		case 1:
			seconds = int64(value)
		case 2:
			nanos = int64(int32(value))
		}
		return nil
	})
	return time.Duration(seconds)*time.Second + time.Duration(nanos), err
}

func enumName(names map[uint64]string, prefix string, value uint64) string {
	if name, ok := names[value]; ok {
		return name
	}
	return prefix + strconv.FormatUint(value, 10)
}

// eachField calls visit with every field of a message: the payload of a
// length-delimited field, or the value of a varint one. Fields of the other
// wire types are skipped, since nothing read here uses them, and a message
// that does not parse to its end is an error.
func eachField(
	message []byte,
	visit func(number protowire.Number, kind protowire.Type, payload []byte, value uint64) error,
) error {
	for len(message) > 0 {
		number, kind, n := protowire.ConsumeTag(message)
		if n < 0 {
			return protowire.ParseError(n)
		}
		message = message[n:]

		var payload []byte
		var value uint64
		switch kind {
		case protowire.BytesType:
			payload, n = protowire.ConsumeBytes(message)
		case protowire.VarintType:
			value, n = protowire.ConsumeVarint(message)
		default:
			n = protowire.ConsumeFieldValue(number, kind, message)
		}
		if n < 0 {
			return protowire.ParseError(n)
		}
		message = message[n:]

		if kind == protowire.BytesType || kind == protowire.VarintType {
			if err := visit(number, kind, payload, value); err != nil {
				return err
			}
		}
	}
	return nil
}
