package apiclient

// The types below carry only the fields the CLI reads or sends, under the
// JSON names apps/api/openapi.json gives them. Leaving the rest out is
// deliberate: a field the CLI never uses is one more name that could drift
// unseen, and the contract test checks every name that is here.

// Me is the signed-in user and the teams they belong to.
type Me struct {
	UserID      string       `json:"user_id"`
	Email       string       `json:"email"`
	Memberships []Membership `json:"memberships"`
}

// Membership is one team the user belongs to. The CLI addresses a team by
// Slug and the API by TeamID, and this is where the two meet.
type Membership struct {
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Role   string `json:"role"`
}

// Folder is a team's folder.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Domain is a hostname a team can put links on.
type Domain struct {
	ID                 string `json:"id"`
	Hostname           string `json:"hostname"`
	VerificationStatus string `json:"verification_status"`
}

// Link is a short link.
type Link struct {
	ID             string `json:"id"`
	TeamID         string `json:"team_id"`
	DomainID       string `json:"domain_id"`
	Hostname       string `json:"hostname"`
	Slug           string `json:"slug"`
	ShortURL       string `json:"short_url"`
	DestinationURL string `json:"destination_url"`
	State          string `json:"state"`
}

// Page is the API's list envelope. Every list endpoint answers one, with the
// schema named Page plus the item type (PageLink, PageFolder, PageDomain).
type Page[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
}

// CreateLinkRequest is the body of a link creation. Everything but the
// destination is optional and left out of the JSON when empty, so the API
// applies its own default (a generated slug, the shared domain, no folder)
// instead of being told to use an empty string.
type CreateLinkRequest struct {
	DestinationURL string `json:"destination_url"`
	Slug           string `json:"slug,omitempty"`
	FolderID       string `json:"folder_id,omitempty"`
	DomainID       string `json:"domain_id,omitempty"`
}

// ListLinksQuery filters and pages a team's links. Empty and zero fields are
// not sent at all, because the API treats an explicit page=0 or q= as a
// value to validate and not as "unset".
type ListLinksQuery struct {
	Q        string
	FolderID string
	Sort     string
	Page     int
	PerPage  int
}

// LinkStats is a link's statistics for one window.
type LinkStats struct {
	// AnalyticsEnabled is false when the link's counting is switched off, in
	// which case an all-zero document means "not counted" and not "not
	// clicked".
	AnalyticsEnabled bool       `json:"analytics_enabled"`
	Totals           StatCounts `json:"totals"`
	Series           []StatDay  `json:"series"`
}

// StatCounts are the counters for a whole window. UniqueVisitors is a sum of
// daily figures, so a person returning on three days counts three times; the
// API's own description says so, because it is the opposite of what the
// name suggests.
type StatCounts struct {
	Clicks              int64 `json:"clicks"`
	UniqueVisitors      int64 `json:"unique_visitors"`
	HumanClicks         int64 `json:"human_clicks"`
	HumanUniqueVisitors int64 `json:"human_unique_visitors"`
}

// StatDay is one day of a link's series, gaps included.
type StatDay struct {
	Date                string `json:"date"`
	Clicks              int64  `json:"clicks"`
	UniqueVisitors      int64  `json:"unique_visitors"`
	HumanClicks         int64  `json:"human_clicks"`
	HumanUniqueVisitors int64  `json:"human_unique_visitors"`
}

// QRQuery styles a link's QR code. Empty and zero fields are not sent, so the
// API's defaults (SVG, black on white, 512 pixels) apply.
type QRQuery struct {
	Format string
	Size   int
	FG     string
	BG     string
}
