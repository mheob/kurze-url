package scanning

import "time"

// This file is compiled only into this package's tests.

// NewClientForTest points a Client at a test server, with a timeout short
// enough for a test to wait out.
func NewClientForTest(apiKey, endpoint string, timeout time.Duration) *Client {
	c := NewClient(apiKey)
	c.endpoint = endpoint
	c.http.Timeout = timeout
	return c
}

// TimeoutForTest is the timeout c's HTTP client applies to one request. It
// reads what NewClient configured, which NewClientForTest overrides.
func TimeoutForTest(c *Client) time.Duration {
	return c.http.Timeout
}

// RequestTimeoutForTest is the timeout NewClient sets.
const RequestTimeoutForTest = requestTimeout

// MaxPrefixesPerRequestForTest is where Check splits a lookup.
const MaxPrefixesPerRequestForTest = maxPrefixesPerRequest
