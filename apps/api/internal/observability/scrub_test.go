package observability_test

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// eventWithEverything is one event carrying every category of request data
// this project must not send: a forwarded client address in three shapes, a
// session cookie, a cleartext password in the body, a query string, and one
// header — X-Request-Id — that names no known hazard at all. That last one
// is what makes this an allowlist test rather than a five-name denylist
// test: it is not personal data, but it still must not survive, because
// nothing outside allowedHeaders is permitted to leave this process.
func eventWithEverything() *sentry.Event {
	event := sentry.NewEvent()
	event.User.IPAddress = "203.0.113.7"
	event.Request = &sentry.Request{
		URL:         "https://go.kurze-url.app/abcd1234/verify?token=secret",
		Method:      "POST",
		Data:        "password=hunter2",
		QueryString: "token=secret",
		Cookies:     "sb-access-token=eyJhbGci",
		Headers: map[string]string{
			"User-Agent":             "Mozilla/5.0",
			"X-Forwarded-For":        "203.0.113.7",
			"X-Vercel-Forwarded-For": "203.0.113.7",
			"X-Real-Ip":              "203.0.113.7",
			"Cookie":                 "sb-access-token=eyJhbGci",
			"Authorization":          "Bearer eyJhbGci",
			"X-Request-Id":           "6f1a2b3c-4d5e-6f70-8192-a3b4c5d6e7f8",
		},
		Env: map[string]string{"REMOTE_ADDR": "203.0.113.7"},
	}
	return event
}

// The link password is the sharpest case in this codebase: POST
// /{slug}/verify posts it in cleartext, so a panic there would ship it to a
// third party.
func TestScrubDiscardsTheRequestBody(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.Request.Data)
}

func TestScrubKeepsOnlyTheUserAgentHeader(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Equal(t, map[string]string{"User-Agent": "Mozilla/5.0"}, got.Request.Headers)
}

func TestScrubRemovesEveryTraceOfTheClientAddress(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.User.IPAddress)
	require.Empty(t, got.Request.Env)
}

func TestScrubRemovesCookiesAndQuery(t *testing.T) {
	got := observability.Scrub(eventWithEverything(), nil)

	require.Empty(t, got.Request.Cookies)
	require.Empty(t, got.Request.QueryString)
	require.Equal(t, "https://go.kurze-url.app/abcd1234/verify", got.Request.URL)
}

// Header names arrive lowercased over HTTP/2, so an allowlist keyed on the
// canonical spelling has to fold case or it silently drops everything.
func TestScrubMatchesHeadersCaseInsensitively(t *testing.T) {
	event := sentry.NewEvent()
	event.Request = &sentry.Request{Headers: map[string]string{"user-agent": "curl/8.0"}}

	got := observability.Scrub(event, nil)

	require.Equal(t, map[string]string{"User-Agent": "curl/8.0"}, got.Request.Headers)
}

// A panic captured outside an HTTP request has no Request at all.
func TestScrubToleratesAnEventWithoutARequest(t *testing.T) {
	event := sentry.NewEvent()
	event.User.IPAddress = "203.0.113.7"

	got := observability.Scrub(event, nil)

	require.Empty(t, got.User.IPAddress)
	require.Nil(t, got.Request)
}
