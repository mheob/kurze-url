package apiclient

// The API operations the client calls, spelled exactly as the spec keys them.
// The client builds every request from these constants and
// contract_test.go reads the same ones, so a path cannot be renamed in one
// place and forgotten in the other: the spec check and the code both start
// from this file.
const (
	PathMe          = "/v1/me"
	PathTeamFolders = "/v1/teams/{team_id}/folders"
	PathTeamDomains = "/v1/teams/{team_id}/domains"
	PathTeamLinks   = "/v1/teams/{team_id}/links"
	PathLinkStats   = "/v1/links/{link_id}/stats"
	PathLinkQR      = "/v1/links/{link_id}/qr"
)

// The path parameters the templates above use.
const (
	paramTeamID = "team_id"
	paramLinkID = "link_id"
)

// The query parameter names, again as the spec spells them. One constant per
// name, shared by every operation that takes it, because the spec gives
// page and per_page the same meaning everywhere.
const (
	QueryPage     = "page"
	QueryPerPage  = "per_page"
	QueryQ        = "q"
	QueryFolderID = "folder_id"
	QuerySort     = "sort"
	QueryFrom     = "from"
	QueryTo       = "to"
	QueryFormat   = "format"
	QuerySize     = "size"
	QueryFG       = "fg"
	QueryBG       = "bg"
)
