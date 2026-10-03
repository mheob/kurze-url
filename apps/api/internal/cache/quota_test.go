package cache_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/link"
)

// upstashQuotaReply is the refusal as a client reported it in September
// 2026: Upstash puts the limit and the usage into the text itself.
const upstashQuotaReply = "ERR max requests limit exceeded. Limit: 500000, Usage: 500002"

// redisReply stands in for go-redis's server-reply error, which lives in an
// internal package this module cannot import. Anything with a RedisError
// method satisfies redis.Error, and that interface is all QuotaExceeded looks
// for.
type redisReply string

func (e redisReply) Error() string { return string(e) }
func (redisReply) RedisError()     {}

func TestQuotaExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "the monthly limit, wrapped the way the cache wraps",
			err:  fmt.Errorf("cache: redirect lookup: %w", redisReply(upstashQuotaReply)),
			want: true,
		},
		{
			name: "the daily limit older databases still answer with",
			err:  fmt.Errorf("cache: rate limit: %w", redisReply("ERR max daily request limit exceeded")),
			want: true,
		},
		{
			name: "the monthly limit, unwrapped",
			err:  redisReply(upstashQuotaReply),
			want: true,
		},
		{
			name: "another Redis reply",
			err:  fmt.Errorf("cache: put not-found: %w", redisReply("ERR wrong number of arguments for 'set' command")),
			want: false,
		},
		{
			name: "no error",
			err:  nil,
			want: false,
		},
		// The phrase alone is not enough: only a reply Redis itself sent
		// counts, so nothing this process formats can be mistaken for one.
		{
			name: "a plain error holding the phrase",
			err:  errors.New(upstashQuotaReply),
			want: false,
		},
		{
			name: "a plain error holding the phrase, wrapped",
			err:  fmt.Errorf("cache: x: %w", errors.New(upstashQuotaReply)),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, cache.QuotaExceeded(tc.err))
		})
	}
}

// The table above proves the matcher against a stand-in type. This proves it
// against the one that matters: go-redis's own reply type, parsed off a real
// connection and passed up through each of the redirect path's three cache
// calls. If go-redis ever wrapped a reply in something errors.As cannot see
// through, or a cache method stopped wrapping with %w, the hourly Sentry rule
// would silently stop matching and the outage would cost the month's events
// again — with every test of the matcher itself still green.
func TestTheRedirectPathsCacheCallsSurfaceTheQuotaRefusal(t *testing.T) {
	ctx := context.Background()
	client, err := cache.New("redis://"+quotaExhaustedServer(t), "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	key := link.CacheKey("short.test", "hello")

	calls := map[string]func() error{
		"LookupForRedirect": func() error {
			_, err := client.LookupForRedirect(ctx, key, "v1", "2026-10-03", time.Hour)
			return err
		},
		"Allow": func() error {
			_, _, err := client.Allow(ctx, "rl:redirect:test", 60, time.Minute)
			return err
		},
		"PutNotFound": func() error {
			return client.PutNotFound(ctx, key, time.Minute)
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			require.True(t, cache.QuotaExceeded(err), "got %v", err)
			// The Sentry event is built from this text, and the usage figure
			// in it is the one number worth reading there.
			require.Contains(t, err.Error(), "Usage: 500002")
		})
	}
}

// quotaExhaustedServer starts a listener that speaks just enough RESP to
// answer every command — the connection handshake included — with Upstash's
// quota refusal, which is what an exhausted database does. It returns the
// listener's address.
func quotaExhaustedServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go answerEveryCommandWithTheQuotaRefusal(conn)
		}
	}()

	return listener.Addr().String()
}

func answerEveryCommandWithTheQuotaRefusal(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	for {
		if err := skipCommand(reader); err != nil {
			return
		}
		if _, err := io.WriteString(conn, "-"+upstashQuotaReply+"\r\n"); err != nil {
			return
		}
	}
}

// skipCommand consumes one client command: an array of bulk strings, which
// is the only shape go-redis sends.
func skipCommand(reader *bufio.Reader) error {
	count, err := readLength(reader, '*')
	if err != nil {
		return err
	}
	for range count {
		size, err := readLength(reader, '$')
		if err != nil {
			return err
		}
		if _, err := reader.Discard(size + len("\r\n")); err != nil {
			return err
		}
	}
	return nil
}

func readLength(reader *bufio.Reader, marker byte) (int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if line == "" || line[0] != marker {
		return 0, fmt.Errorf("unexpected RESP line %q", line)
	}
	return strconv.Atoi(line[1:])
}
