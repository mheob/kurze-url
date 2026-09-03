package api_test

import (
	"math"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func TestPageParamsClampsAndDefaults(t *testing.T) {
	cases := []struct {
		name        string
		in          api.PageParams
		wantLimit   int32
		wantOffset  int32
		wantPage    int
		wantPerPage int
	}{
		{"defaults when zero", api.PageParams{}, 25, 0, 1, 25},
		{"second page", api.PageParams{Page: 2, PerPage: 10}, 10, 10, 2, 10},
		{"per_page is capped at 100", api.PageParams{Page: 1, PerPage: 5000}, 100, 0, 1, 100},
		{"negative page is treated as the first", api.PageParams{Page: -3, PerPage: 10}, 10, 0, 1, 10},
		{"negative per_page falls back to the default", api.PageParams{Page: 1, PerPage: -5}, 25, 0, 1, 25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantLimit, tc.in.Limit())
			require.Equal(t, tc.wantOffset, tc.in.Offset())

			page, perPage := tc.in.Normalized()
			require.Equal(t, tc.wantPage, page)
			require.Equal(t, tc.wantPerPage, perPage)
		})
	}
}

func TestOffsetSaturatesInsteadOfOverflowingOnAnOversizedPage(t *testing.T) {
	// PageParams is a plain struct: later tasks can build one directly without
	// going through Huma's request validation (and hence without the
	// PageParams.Page maximum tag ever being enforced), so Offset() itself
	// must never produce a negative or wrapped-around SQL OFFSET.
	params := api.PageParams{Page: 100_000_000, PerPage: 100}

	offset := params.Offset()

	require.GreaterOrEqual(t, offset, int32(0))
	require.Equal(t, int32(math.MaxInt32), offset)
}

func TestNewPageReportsTheNormalisedParamsAndTotal(t *testing.T) {
	page := api.NewPage([]string{"a", "b"}, api.PageParams{Page: 0, PerPage: 0}, 7)

	require.Equal(t, []string{"a", "b"}, page.Items)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 25, page.PerPage)
	require.Equal(t, 7, page.TotalCount)
}

func TestNewPageNeverEmitsANullItemsArray(t *testing.T) {
	// Explicit [string] instantiation: an untyped nil carries no type
	// information for Go to infer T from, so the brief's literal
	// api.NewPage(nil, ...) does not compile.
	page := api.NewPage[string](nil, api.PageParams{}, 0)

	require.NotNil(t, page.Items,
		"a generated TypeScript client should get [], never null")
	require.Empty(t, page.Items)
}

// The generated TypeScript client in plan 4 inherits these schema names, so a
// mangled generic name would propagate into the frontend's types. This reads
// the names off the actual generated OpenAPI document — the DoD's real
// assertion — rather than calling huma.DefaultSchemaNamer directly, which
// would only prove the namer behaves as expected in isolation, not that the
// three list endpoints' response schemas actually end up named that way in
// the document a generated client would consume.
func TestGenericEnvelopeSchemaNamesAreReadable(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	schemas := humaAPI.OpenAPI().Components.Schemas.Map()

	for _, name := range []string{"PageTeam", "PageMember", "PageAuditEntry"} {
		require.Contains(t, schemas, name)
	}
}
