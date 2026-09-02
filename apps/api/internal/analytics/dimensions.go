package analytics

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/mileusna/useragent"
)

// QRQueryParam marks a click as coming from a scanned QR code. The QR
// generator (plan 2) appends "?qr=1" to the short URL it encodes; nothing else
// can distinguish a scan from a normal click.
const QRQueryParam = "qr"

const (
	unknown = "unknown"
	// maxValueLength bounds a dimension value so an attacker cannot inflate
	// link_click_stats by sending long, distinct referrers or utm_sources.
	maxValueLength = 128
)

// Dimensions are the rollup axes a single click contributes to.
type Dimensions struct {
	Browser   string
	OS        string
	Device    string
	Country   string
	Referrer  string
	UTMSource string
	BotStatus string
	Source    string
}

// DimensionRow is one (dimension_type, dimension_value) pair. Value is nil
// only for the "total" row.
type DimensionRow struct {
	Type  string
	Value *string
}

// ExtractDimensions derives the rollup axes from a redirect request. It reads
// only the User-Agent, the Referer host, the utm_source query parameter and
// Vercel's geo header — never the client IP.
func ExtractDimensions(r *http.Request) Dimensions {
	ua := useragent.Parse(r.UserAgent())

	d := Dimensions{
		Browser:   fallback(ua.Name),
		OS:        fallback(ua.OS),
		Device:    device(ua),
		Country:   country(r),
		Referrer:  referrerHost(r.Referer()),
		UTMSource: truncate(r.URL.Query().Get("utm_source")),
		BotStatus: "human",
		Source:    "regular",
	}
	if ua.Bot {
		d.BotStatus = "bot"
	}
	if r.URL.Query().Get(QRQueryParam) == "1" {
		d.Source = "qr"
	}
	return d
}

// Rows flattens the dimensions into the rows a click writes. utm_source is
// omitted when absent: emitting "unknown" for every non-campaign click would
// double the table's row count for no analytical value.
func (d Dimensions) Rows() []DimensionRow {
	rows := []DimensionRow{
		{Type: "total", Value: nil},
		{Type: "browser", Value: &d.Browser},
		{Type: "os", Value: &d.OS},
		{Type: "device", Value: &d.Device},
		{Type: "country", Value: &d.Country},
		{Type: "referrer", Value: &d.Referrer},
		{Type: "bot_status", Value: &d.BotStatus},
		{Type: "qr_vs_regular", Value: &d.Source},
	}
	if d.UTMSource != "" {
		rows = append(rows, DimensionRow{Type: "utm_source", Value: &d.UTMSource})
	}
	return rows
}

func device(ua useragent.UserAgent) string {
	switch {
	case ua.Mobile:
		return "mobile"
	case ua.Tablet:
		return "tablet"
	case ua.Desktop:
		return "desktop"
	default:
		return unknown
	}
}

// country reads the geo header Vercel adds at the edge. Doing it this way
// keeps the promise of no per-request third-party geolocation call without
// shipping and updating a GeoIP database. Off Vercel the header is absent.
func country(r *http.Request) string {
	code := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Vercel-IP-Country")))
	if code == "" {
		return unknown
	}
	return truncate(code)
}

// referrerHost keeps only the host. A full referrer URL can carry personal
// data in its path or query, and its cardinality is unbounded.
func referrerHost(referer string) string {
	if referer == "" {
		return "direct"
	}
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" {
		return unknown
	}
	return truncate(strings.ToLower(u.Hostname()))
}

func fallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return unknown
	}
	return truncate(v)
}

// truncate bounds v to maxValueLength bytes without ever returning invalid
// UTF-8: v is attacker-supplied free-form text, so a naive byte slice can cut
// a multi-byte rune in half. Backing off a few extra bytes to the last valid
// rune boundary is preferable to writing a broken string into Postgres.
func truncate(v string) string {
	if len(v) <= maxValueLength {
		return v
	}
	v = v[:maxValueLength]
	for len(v) > 0 && !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return v
}
