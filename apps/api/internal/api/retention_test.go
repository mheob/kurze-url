package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

const testRetentionToken = "test-retention-token"

// retention sends one POST /internal/retention. token == "" sends no header at
// all, which is a different case from sending a wrong one.
func retention(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/retention", nil)
	req.Host = "api.test"
	if token != "" {
		req.Header.Set("X-Retention-Token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeRetention(t *testing.T, rec *httptest.ResponseRecorder) (int64, string) {
	t.Helper()
	var body struct {
		Deleted    int64  `json:"deleted"`
		OldestKept string `json:"oldest_kept"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	return body.Deleted, body.OldestKept
}

func TestRetentionRefusesWithoutTheToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "").Code)
}

func TestRetentionRefusesAWrongToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "wrong").Code)
}

// An unset token disables the endpoint rather than opening it. This is the
// test that matters most of the three: without it, one forgotten environment
// variable leaves a delete endpoint answering to whoever guesses the path.
func TestRetentionIsDisabledWhenNoTokenIsConfigured(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = ""

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "").Code)
	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "anything").Code)
}

// The fixture pins the clock to 2026-09-02, so the cutoff is 2026-06-05 —
// eighty-nine days earlier. The row on the cutoff day survives; the one before
// it does not.
func TestRetentionDeletesOnlyWhatIsPastTheCutoff(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken

	for _, d := range []string{"2026-06-04", "2026-06-05", "2026-09-01"} {
		_, err := f.pool.Exec(context.Background(),
			`insert into link_click_stats
			   (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
			 values ($1, $2::date, 'total', null, 1, 1)`, f.linkID, d)
		require.NoError(t, err)
	}

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	deleted, oldestKept := decodeRetention(t, rec)
	require.EqualValues(t, 1, deleted)
	require.Equal(t, "2026-06-05", oldestKept)

	var surviving int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link_click_stats where link_id = $1`, f.linkID).Scan(&surviving))
	require.Equal(t, 2, surviving)
}

// Running twice is safe and the second run reports nothing. The workflow has
// no way to know whether an earlier attempt got through, so an endpoint that
// misbehaved on a repeat call would turn a retried run into a hazard.
func TestRetentionIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken
	_, err := f.pool.Exec(context.Background(),
		`insert into link_click_stats
		   (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
		 values ($1, '2026-01-01'::date, 'total', null, 1, 1)`, f.linkID)
	require.NoError(t, err)

	router := api.NewRouter(f.deps)

	first, _ := decodeRetention(t, retention(t, router, testRetentionToken))
	second, _ := decodeRetention(t, retention(t, router, testRetentionToken))

	require.EqualValues(t, 1, first)
	require.EqualValues(t, 0, second)
}
