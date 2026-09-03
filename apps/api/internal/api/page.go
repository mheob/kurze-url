package api

import "math"

// Page is the envelope every list endpoint returns. Pagination metadata lives
// in the body, not in headers: Huma's value is typed response bodies, and a
// generated TypeScript client cannot see headers.
type Page[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
}

const (
	defaultPerPage = 25
	maxPerPage     = 100
)

// PageParams is embedded by every list operation's input. Huma applies the
// default and range tags; the methods below normalise again so a value built
// directly in a test behaves identically to one parsed from a request.
type PageParams struct {
	Page    int `query:"page" default:"1" minimum:"1" maximum:"1000000" doc:"1-based page number."`
	PerPage int `query:"per_page" default:"25" minimum:"1" maximum:"100" doc:"Items per page, capped at 100."`
}

// Normalized returns the effective page and per-page values.
func (p PageParams) Normalized() (page, perPage int) {
	page, perPage = p.Page, p.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	return page, perPage
}

// Limit is the SQL LIMIT for these params.
func (p PageParams) Limit() int32 {
	_, perPage := p.Normalized()
	return int32(perPage)
}

// Offset is the SQL OFFSET for these params. It saturates at math.MaxInt32
// rather than overflowing: PageParams is a plain struct that later tasks can
// construct directly, bypassing Huma's request validation (and hence the
// Page field's maximum tag), so this must stay safe on its own.
func (p PageParams) Offset() int32 {
	page, perPage := p.Normalized()
	offset := int64(page-1) * int64(perPage)
	if offset > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(offset)
}

// NewPage wraps a query's rows. total comes from the count(*) over () column
// the list queries project, so the items and the total are always consistent.
func NewPage[T any](items []T, params PageParams, total int64) Page[T] {
	page, perPage := params.Normalized()
	if items == nil {
		items = []T{}
	}
	return Page[T]{
		Items:      items,
		Page:       page,
		PerPage:    perPage,
		TotalCount: int(total),
	}
}

// NeedsTotalFallback reports whether a list handler must fall back to a plain
// count query to recover the true total_count. count(*) over () is only
// readable off a row the paginated query actually returned, so a page past
// the last one comes back with zero rows and nothing to read it from — the
// handler must issue a separate plain count in that case. It is never needed
// for the first page: an empty first page means the collection itself is
// empty, and 0 is already the correct total.
func NeedsTotalFallback(params PageParams, rowCount int) bool {
	page, _ := params.Normalized()
	return rowCount == 0 && page > 1
}
