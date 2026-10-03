package scanning_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

const testKey = "not-a-key"

type detail struct {
	ThreatType string   `json:"threatType"`
	Attributes []string `json:"attributes,omitempty"`
}

type fullHash struct {
	FullHash        string   `json:"fullHash"`
	FullHashDetails []detail `json:"fullHashDetails"`
}

// fakeGoogle stands in for hashes.search. Like the real endpoint, it answers a
// request with every listed full hash whose four-byte prefix the request asked
// for — which is exactly how one URL's lookup comes back carrying a hash that
// belongs to a neighbour's expression, or to nobody's.
type fakeGoogle struct {
	mu            sync.Mutex
	listed        map[[sha256.Size]byte][]detail
	cacheDuration string
	// cacheDurations, when set, answers each request with the next entry
	// instead of cacheDuration, so split requests can disagree.
	cacheDurations []string
	status         int
	body           string
	location       string
	delay          time.Duration
	requests       []*http.Request
}

func newFakeGoogle(t *testing.T) (*fakeGoogle, *scanning.Client) {
	t.Helper()
	g := &fakeGoogle{listed: map[[sha256.Size]byte][]detail{}, cacheDuration: "300s"}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	return g, scanning.NewClientForTest(testKey, server.URL+"/v5/hashes:search", time.Second)
}

// list puts an expression on the fake list, the way Google lists a URL
// pattern: by the SHA-256 of its canonical expression.
func (g *fakeGoogle) list(expression string, details ...detail) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listed[sha256.Sum256([]byte(expression))] = details
}

// listCollision lists a full hash that shares expression's four-byte prefix
// and differs after it: a prefix hit that is not a match.
func (g *fakeGoogle) listCollision(expression string, details ...detail) {
	sum := sha256.Sum256([]byte(expression))
	sum[sha256.Size-1] ^= 0xff
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listed[sum] = details
}

// configure changes how the fake answers, under its lock: the server reads
// these fields on its own goroutine.
func (g *fakeGoogle) configure(change func(*fakeGoogle)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	change(g)
}

func (g *fakeGoogle) recorded() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.requests)
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r.Clone(context.Background()))
	status, body, location, delay, cacheDuration := g.status, g.body, g.location, g.delay, g.cacheDuration
	if len(g.cacheDurations) > 0 {
		cacheDuration, g.cacheDurations = g.cacheDurations[0], g.cacheDurations[1:]
	}
	listed := maps.Clone(g.listed)
	g.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}

	asked := map[[4]byte]bool{}
	for _, encoded := range r.URL.Query()["hashPrefixes"] {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != 4 {
			http.Error(w, "bad prefix", http.StatusBadRequest)
			return
		}
		asked[[4]byte(raw)] = true
	}

	answer := struct {
		FullHashes    []fullHash `json:"fullHashes,omitempty"`
		CacheDuration string     `json:"cacheDuration,omitempty"`
	}{CacheDuration: cacheDuration}
	for sum, details := range listed {
		if asked[[4]byte(sum[:4])] {
			answer.FullHashes = append(answer.FullHashes, fullHash{
				FullHash:        base64.StdEncoding.EncodeToString(sum[:]),
				FullHashDetails: details,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func TestCheckSendsTheKeyInAHeaderAndNeverInTheURL(t *testing.T) {
	g, client := newFakeGoogle(t)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.NoError(t, err)

	requests := g.recorded()
	require.Len(t, requests, 1)
	require.Equal(t, testKey, requests[0].Header.Get("X-Goog-Api-Key"))
	require.NotContains(t, requests[0].URL.String(), testKey)
	require.Empty(t, requests[0].URL.Query().Get("key"))
}

// *url.Error prints the request URL into its text, and error text reaches the
// logs and Sentry. That is the whole reason the key is a header.
func TestCheckKeepsTheKeyOutOfItsErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL + "/v5/hashes:search"
	server.Close()
	client := scanning.NewClientForTest(testKey, endpoint, time.Second)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), testKey)
}

func TestCheckSendsOnlyFourBytePrefixes(t *testing.T) {
	g, client := newFakeGoogle(t)

	_, err := client.Check(context.Background(),
		[]string{"https://geheim.verein.test/mitglieder?token=abc"})
	require.NoError(t, err)

	query := g.recorded()[0].URL.Query()
	require.Equal(t, []string{"hashPrefixes"}, slices.Collect(maps.Keys(query)))
	for _, encoded := range query["hashPrefixes"] {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		require.Len(t, raw, 4)
	}
	raw := g.recorded()[0].URL.RawQuery
	require.NotContains(t, raw, "geheim")
	require.NotContains(t, raw, "mitglieder")
}

// 200 URLs with seven unique expressions each and five shared make 1,405
// prefixes, which at 250 a request needs six requests. Each request answers
// with its own cacheDuration, and every verdict may be relied on only for the
// shortest of them: a URL's prefixes may have been spread across several.
func TestCheckSplitsAtTheRequestLimitAndSendsEachPrefixOnce(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.configure(func(g *fakeGoogle) {
		g.cacheDurations = []string{"300s", "120s", "45s", "600s", "900s", "200s"}
	})
	urls := make([]string, 0, 200)
	for i := range 200 {
		urls = append(urls, fmt.Sprintf("https://h%d.example.org/a/b/c/d.html?q=%d", i, i))
	}

	results, err := client.Check(context.Background(), urls)
	require.NoError(t, err)
	require.Len(t, results, 200)

	limit := scanning.MaxPrefixesPerRequestForTest
	seen := map[string]bool{}
	for _, request := range g.recorded() {
		prefixes := request.URL.Query()["hashPrefixes"]
		require.LessOrEqual(t, len(prefixes), limit)
		for _, prefix := range prefixes {
			require.False(t, seen[prefix], "prefix %s sent twice", prefix)
			seen[prefix] = true
		}
	}
	require.Greater(t, len(seen), limit, "the input must need more than one request")
	require.Len(t, g.recorded(), (len(seen)+limit-1)/limit)
	for _, u := range urls {
		require.Empty(t, results[u].ThreatTypes)
		require.Equal(t, 45*time.Second, results[u].ValidFor)
	}
}

func TestCheckFlagsOnlyTheURLWhoseExpressionIsListed(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("shared.test/phishing.html", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{
		"https://shared.test/phishing.html",
		"https://shared.test/sommerfest",
		"https://other.test/",
	})
	require.NoError(t, err)

	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://shared.test/phishing.html"].ThreatTypes)
	require.Empty(t, results["https://shared.test/sommerfest"].ThreatTypes,
		"a neighbour on the same host shares prefixes, not the match")
	require.Empty(t, results["https://other.test/"].ThreatTypes)
}

// A listed host covers everything below it, subdomains included.
func TestCheckFlagsEveryURLBelowAListedHost(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("evil.test/", detail{ThreatType: "MALWARE"})

	results, err := client.Check(context.Background(),
		[]string{"https://evil.test/a", "https://www.evil.test/b?c=d"})
	require.NoError(t, err)

	require.Equal(t, []string{"MALWARE"}, results["https://evil.test/a"].ThreatTypes)
	require.Equal(t, []string{"MALWARE"}, results["https://www.evil.test/b?c=d"].ThreatTypes)
}

// Google's rules unescape before they split, so they read this URL's host as
// "x". net/url, which destination.Validate accepted it with, reads bad.com,
// and so does the browser the redirect sends there. Check looks the URL up
// under both readings.
func TestCheckFlagsAListedHostBehindAnEscapedUserinfo(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("bad.com/", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{"https://x%2F@bad.com/login"})
	require.NoError(t, err)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://x%2F@bad.com/login"].ThreatTypes)
}

// Here Google's reading finds no host at all. Without net/url's, the URL would
// never get a verdict, and a browser would still open bad.com.
func TestCheckJudgesAURLOnlyNetURLFindsAHostIn(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("bad.com/", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{"https://%2F@bad.com/"})
	require.NoError(t, err)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://%2F@bad.com/"].ThreatTypes)
}

// net/url refuses a host spelled with escapes; Google's rules read the
// address it spells. A URL one reading refuses is judged on the other alone.
func TestCheckJudgesAURLOnlyGooglesRulesFindAHostIn(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("168.188.99.26/", detail{ThreatType: "MALWARE"})

	raw := "https://%31%36%38%2e%31%38%38%2e%39%39%2e%32%36/"
	results, err := client.Check(context.Background(), []string{raw})
	require.NoError(t, err)
	require.Equal(t, []string{"MALWARE"}, results[raw].ThreatTypes)
}

// Each URL is judged on its own readings only. "https://x/login" shares every
// expression of the first URL's Google reading, and none of its net/url one.
func TestCheckKeepsOneURLsSecondReadingFromItsNeighbour(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("bad.com/", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(),
		[]string{"https://x%2F@bad.com/login", "https://x/login"})
	require.NoError(t, err)

	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://x%2F@bad.com/login"].ThreatTypes)
	require.Contains(t, results, "https://x/login")
	require.Empty(t, results["https://x/login"].ThreatTypes)
}

func TestCheckIgnoresAPrefixMatchWhoseFullHashDiffers(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.listCollision("verein.test/", detail{ThreatType: "MALWARE"})

	results, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.NoError(t, err)
	require.Empty(t, results["https://verein.test/"].ThreatTypes)
}

// CANARY means "do not enforce", and FRAME_ONLY means "enforce only on
// frames"; a top-level redirect is not a frame.
func TestCheckDoesNotEnforceCanaryOrFrameOnlyEntries(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("canary.test/", detail{ThreatType: "MALWARE", Attributes: []string{"CANARY"}})
	g.list("frame.test/", detail{ThreatType: "SOCIAL_ENGINEERING", Attributes: []string{"FRAME_ONLY"}})
	g.list("both.test/",
		detail{ThreatType: "MALWARE", Attributes: []string{"CANARY"}},
		detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(),
		[]string{"https://canary.test/", "https://frame.test/", "https://both.test/"})
	require.NoError(t, err)

	require.Empty(t, results["https://canary.test/"].ThreatTypes)
	require.Empty(t, results["https://frame.test/"].ThreatTypes)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://both.test/"].ThreatTypes)
}

// Google's reference tells clients to tolerate values they do not know.
// Dropping one would turn a report into "clean".
func TestCheckKeepsAThreatTypeItDoesNotKnow(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("neu.test/", detail{ThreatType: "THREAT_TYPE_FROM_THE_FUTURE"})

	results, err := client.Check(context.Background(), []string{"https://neu.test/"})
	require.NoError(t, err)
	require.Equal(t, []string{"THREAT_TYPE_FROM_THE_FUTURE"}, results["https://neu.test/"].ThreatTypes)
}

func TestCheckFindsAnInternationalizedHostUnderItsPunycode(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("xn--bcher-kva.de/", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{"https://Bücher.de/anmeldung"})
	require.NoError(t, err)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://Bücher.de/anmeldung"].ThreatTypes)
}

// A verdict whose validity cannot be read may not be relied on at all.
func TestCheckReadsTheCacheDuration(t *testing.T) {
	for _, tc := range []struct {
		cacheDuration string
		want          time.Duration
	}{
		{"300s", 5 * time.Minute},
		{"1.5s", 1500 * time.Millisecond},
		{"", 0},
		{"0s", 0},
		{"soon", 0},
		{"-5s", 0},
	} {
		t.Run(fmt.Sprintf("%q", tc.cacheDuration), func(t *testing.T) {
			g, client := newFakeGoogle(t)
			g.configure(func(g *fakeGoogle) { g.cacheDuration = tc.cacheDuration })

			results, err := client.Check(context.Background(), []string{"https://verein.test/"})
			require.NoError(t, err)
			require.Equal(t, tc.want, results["https://verein.test/"].ValidFor)
		})
	}
}

func TestCheckClassifiesGooglesRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		quota  bool
	}{
		{"429", http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, true},
		{"403 resource exhausted", http.StatusForbidden, `{"error":{"code":403,"status":"RESOURCE_EXHAUSTED"}}`, true},
		{"403 anything else", http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED","message":"secret detail"}}`, false},
		{"503", http.StatusServiceUnavailable, "secret detail", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, client := newFakeGoogle(t)
			g.configure(func(g *fakeGoogle) { g.status, g.body = tc.status, tc.body })

			_, err := client.Check(context.Background(), []string{"https://verein.test/"})
			require.Error(t, err)
			require.Equal(t, tc.quota, scanning.QuotaExceeded(err))
			require.Contains(t, err.Error(), fmt.Sprint(tc.status))
			require.NotContains(t, err.Error(), "secret detail", "the body never reaches an error")
		})
	}
}

// A client that followed this would send the key along: on a redirect Go
// strips Authorization, Cookie and their kin, and X-Goog-Api-Key is none of
// them. The Location is what makes Go consult CheckRedirect at all; a 3xx
// without one comes back unchanged whatever the client's policy.
func TestCheckDoesNotFollowARedirect(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(elsewhere.Close)

	g, client := newFakeGoogle(t)
	g.configure(func(g *fakeGoogle) { g.status, g.location = http.StatusFound, elsewhere.URL+"/collect" })

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "302")
	require.False(t, scanning.QuotaExceeded(err))
	require.Zero(t, hits.Load(), "the redirect target was requested")
}

func TestCheckRefusesAnOversizedResponse(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.configure(func(g *fakeGoogle) { g.status, g.body = http.StatusOK, strings.Repeat("a", 1<<20+1) })

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.ErrorContains(t, err, "exceeds")
}

// The status decides before the body is read, so a refusal too large to read
// is still classified, and still carries its status.
func TestCheckClassifiesAnOversizedRefusalByItsStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		quota  bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusServiceUnavailable, false},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			g, client := newFakeGoogle(t)
			g.configure(func(g *fakeGoogle) { g.status, g.body = tc.status, strings.Repeat("a", 1<<20+1) })

			_, err := client.Check(context.Background(), []string{"https://verein.test/"})
			require.Error(t, err)
			require.Equal(t, tc.quota, scanning.QuotaExceeded(err))
			require.Contains(t, err.Error(), fmt.Sprint(tc.status))
		})
	}
}

// *url.Error prints the request URL, and the query carries every prefix:
// several kilobytes at 250 of them, in every log line and Sentry event.
func TestCheckKeepsThePrefixesOutOfItsErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL + "/v5/hashes:search"
	server.Close()
	client := scanning.NewClientForTest(testKey, endpoint, time.Second)

	urls := make([]string, 0, 50)
	for i := range 50 {
		urls = append(urls, fmt.Sprintf("https://h%d.example.org/a/b/c/d.html?q=%d", i, i))
	}
	_, err := client.Check(context.Background(), urls)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hashPrefixes")
	require.Contains(t, err.Error(), endpoint, "the error still says where the request went")
	require.Less(t, len(err.Error()), 300)
}

// The redirect path bounds a re-check with a context deadline far shorter
// than the client's own timeout, so Check must hand the context to the
// request rather than wait the timeout out.
func TestCheckStopsAtItsContextsDeadline(t *testing.T) {
	g := &fakeGoogle{listed: map[[sha256.Size]byte][]detail{}, delay: 500 * time.Millisecond}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	client := scanning.NewClientForTest(testKey, server.URL+"/v5/hashes:search", time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Check(ctx, []string{"https://verein.test/"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// NewClientForTest replaces the timeout, so the test below cannot notice a
// NewClient that forgot to set one. This one reads what NewClient configured.
func TestNewClientBoundsEveryRequest(t *testing.T) {
	require.Equal(t, 5*time.Second, scanning.RequestTimeoutForTest)
	require.Equal(t, scanning.RequestTimeoutForTest, scanning.TimeoutForTest(scanning.NewClient(testKey)))
}

func TestCheckGivesUpAfterItsTimeout(t *testing.T) {
	g := &fakeGoogle{listed: map[[sha256.Size]byte][]detail{}, delay: 300 * time.Millisecond}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	client := scanning.NewClientForTest(testKey, server.URL+"/v5/hashes:search", 50*time.Millisecond)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	var timeout interface{ Timeout() bool }
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.Timeout(), "shortening the error's URL must keep it a timeout")
}

func TestCheckWithNothingToCheckMakesNoRequest(t *testing.T) {
	g, client := newFakeGoogle(t)

	results, err := client.Check(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, results)

	// A URL without a host has no verdict: it is left out of the map, which
	// every caller reads as "not checked", never as clean.
	results, err = client.Check(context.Background(), []string{"https:///nohost"})
	require.NoError(t, err)
	require.NotContains(t, results, "https:///nohost")

	require.Empty(t, g.recorded())
}

func TestQuotaExceededMatchesAWrappedRefusal(t *testing.T) {
	wrapped := fmt.Errorf("sweep: %w", fmt.Errorf("%w: hashes.search answered 429", scanning.ErrQuotaExceeded))
	require.True(t, scanning.QuotaExceeded(wrapped))
	require.False(t, scanning.QuotaExceeded(errors.New("quota exceeded, but not ours")))
}
