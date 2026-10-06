package api_test

import (
	"context"
	"sync"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// fakeChecker stands in for Google Safe Browsing in handler tests. It answers
// only for the URLs a test named, and leaves every other URL out of its
// answer — which the code under test must read as "no verdict", never as
// clean. That is also what keeps an instance-wide sweep test from writing to
// links other tests, and other packages in parallel processes, have
// committed: their URLs get no verdict, so nothing is written to them.
//
// Safe for concurrent use: the immediate checks call it from goroutines, and
// CI runs this package under -race.
type fakeChecker struct {
	mu      sync.Mutex
	results map[string]scanning.Result
	err     error
	delay   time.Duration
	during  func(context.Context)
	calls   int
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{results: map[string]scanning.Result{}}
}

// flag makes url report threats, valid for five minutes.
func (c *fakeChecker) flag(url string, threats ...string) {
	c.answer(url, scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute})
}

// pass makes url check clean.
func (c *fakeChecker) pass(url string) {
	c.answer(url, scanning.Result{ValidFor: 5 * time.Minute})
}

func (c *fakeChecker) answer(url string, result scanning.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[url] = result
}

func (c *fakeChecker) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *fakeChecker) Check(ctx context.Context, urls []string) (map[string]scanning.Result, error) {
	c.mu.Lock()
	c.calls++
	err, delay, during := c.err, c.delay, c.during
	answer := make(map[string]scanning.Result, len(urls))
	for _, url := range urls {
		if result, ok := c.results[url]; ok {
			answer[url] = result
		}
	}
	c.mu.Unlock()

	if during != nil {
		during(ctx)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return answer, nil
}
