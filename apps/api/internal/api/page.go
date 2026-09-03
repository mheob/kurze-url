package api

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
	Page    int `query:"page" default:"1" minimum:"1" doc:"1-based page number."`
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

// Offset is the SQL OFFSET for these params.
func (p PageParams) Offset() int32 {
	page, perPage := p.Normalized()
	return int32((page - 1) * perPage)
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
