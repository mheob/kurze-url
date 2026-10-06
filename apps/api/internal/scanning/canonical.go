package scanning

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// ErrNoHost means one reading of a URL found no host to check. Check can
// still judge the URL by its other reading (see lookupExpressions), so this
// alone says nothing about a destination; destination.Validate refuses only a
// URL for which neither reading finds a host (see HasLookupHost).
var ErrNoHost = errors.New("scanning: url has no host")

// canonicalURL is a URL in the form Safe Browsing hashes, split into the parts
// expressions are built from. host, path and query are already
// percent-escaped. scheme plays no part in any expression; it is kept only so
// Canonicalize can print Google's test vectors back.
type canonicalURL struct {
	scheme   string
	host     string
	path     string
	query    string
	hasQuery bool
}

func (c canonicalURL) String() string {
	out := c.scheme + "://" + c.host + c.path
	if c.hasQuery {
		out += "?" + c.query
	}
	return out
}

// Canonicalize returns raw in the canonical form Google's Safe Browsing rules
// define. It exists for the test vectors; Check uses the parts directly.
func Canonicalize(raw string) (string, error) {
	c, err := canonicalize(raw)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}

// Expressions returns the host-suffix/path-prefix expressions Google's rules
// derive from raw: at most five hosts times six paths. Each one is hashed and
// looked up on its own, so a list entry for a whole host or a directory
// matches every URL below it.
func Expressions(raw string) ([]string, error) {
	c, err := canonicalize(raw)
	if err != nil {
		return nil, err
	}
	return c.expressions(), nil
}

// controlStripper removes the three characters Google's rules drop wherever
// they appear. A strings.Replacer rather than strings.Map, because Map decodes
// runes and turns an invalid UTF-8 byte into U+FFFD, and the raw bytes are
// exactly what gets hashed: Google's vector "http://\x01\x80.com/" depends on
// the 0x80 surviving as itself.
var controlStripper = strings.NewReplacer("\t", "", "\r", "", "\n", "")

// canonicalize follows Google's rules in their order: strip whitespace and
// the three control characters, drop the fragment, percent-unescape until
// nothing changes, then normalize the host and the path separately and
// re-escape all three parts. The unescape comes before the URL is split, so a
// host spelled with escapes ("%31%36%38%2e…") is read as the host it spells,
// as Google's vectors require; net/url refuses such a host outright, which is
// why the split here is by hand.
func canonicalize(raw string) (canonicalURL, error) {
	s := controlStripper.Replace(strings.TrimFunc(raw, isTrimmed))
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = unescapeFully(s)

	c := canonicalURL{scheme: "http"}
	if i := strings.Index(s, "://"); i > 0 && isScheme(s[:i]) {
		c.scheme = asciiLower(s[:i])
		s = s[i+len("://"):]
	}

	authority, rest := s, ""
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		authority, rest = s[:i], s[i:]
	}
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	host := canonicalHost(withoutPort(authority))
	if host == "" {
		return canonicalURL{}, ErrNoHost
	}
	c.host = escape(host)

	path := rest
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		path, c.query, c.hasQuery = rest[:i], escape(rest[i+1:]), true
	}
	c.path = escape(canonicalPath(path))
	return c, nil
}

// isTrimmed reports whether r is one of the bytes Google's rules trim from the
// ends of a URL: everything up to and including the space. strings.TrimSpace
// is wrong here, because it also trims Unicode spaces such as U+00A0 and
// U+3000, which a browser keeps and which Google hashes as part of the path.
func isTrimmed(r rune) bool {
	return r <= ' '
}

// isScheme reports whether s is an RFC 3986 scheme. A URL without one is read
// as http, as Google's rules say; "www.google.com/" is one of its vectors.
func isScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// withoutPort drops a port, keeping an IPv6 literal's brackets intact.
func withoutPort(authority string) string {
	if strings.HasPrefix(authority, "[") {
		if i := strings.IndexByte(authority, ']'); i >= 0 {
			return authority[:i+1]
		}
		return authority
	}
	if i := strings.LastIndexByte(authority, ':'); i >= 0 {
		return authority[:i]
	}
	return authority
}

// nat64Prefix is the well-known NAT64 prefix, the only one Google's rules
// read an IPv4 address out of.
var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// hostProfile converts an internationalized host to Punycode the way a browser
// does, which is looser than idna.Lookup: no STD3 rule and no hyphen check, so
// "bücher_x.evil.com", "ab--ü.evil.com" and "-ü.evil.com" still become the
// hosts a browser opens instead of being left as escaped UTF-8 that Google
// never lists. Mapping and the Bidi and joiner checks stay on, and processing
// is non-transitional, so "faß.de" keeps its ß as browsers do.
var hostProfile = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.StrictDomainName(false),
	idna.CheckHyphens(false),
	idna.BidiRule(),
	idna.CheckJoiners(true),
)

// canonicalHost applies Google's host rules — leading and trailing dots
// stripped, runs of dots collapsed, any legal IPv4 spelling normalized,
// everything lowercased — plus the one their rules state in prose: an
// internationalized name becomes Punycode, which is the form a browser opens
// and Google lists. That conversion comes first, because IDNA itself produces
// dots: it maps U+3002, U+FF0E and U+FF61 to ".", so a host spelled
// "evil.com。" is "evil.com." until the trim sees it, and a registrable-domain
// lookup refuses a trailing empty label. A host that is not valid UTF-8, or
// that IDNA refuses, is left as it is and percent-escaped later, which is what
// Google's "\x01\x80.com" vector expects. A bracketed IPv6 literal is
// normalized to RFC 5952, the form a browser's address bar shows, except that
// an IPv4-mapped or well-known-prefix NAT64 address becomes the IPv4 address it
// carries, as the v5 page says: otherwise a listed address spelled as IPv6
// would hash to a value Google never lists.
func canonicalHost(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if addr, err := netip.ParseAddr(host[1 : len(host)-1]); err == nil && addr.Is6() {
			if v4, ok := embeddedIPv4(addr); ok {
				return v4.String()
			}
			return "[" + addr.String() + "]"
		}
		return asciiLower(host)
	}

	if !isASCII(host) && utf8.ValidString(host) {
		if ascii, err := hostProfile.ToASCII(host); err == nil {
			host = ascii
		}
	}
	host = strings.Trim(host, ".")
	for strings.Contains(host, "..") {
		host = strings.ReplaceAll(host, "..", ".")
	}
	if ip, ok := parseIPv4(host); ok {
		return ip
	}
	return asciiLower(host)
}

// embeddedIPv4 returns the IPv4 address an IPv6 address merely wraps: the
// mapped form "::ffff:1.2.3.4" or the NAT64 form "64:ff9b::1.2.3.4".
func embeddedIPv4(addr netip.Addr) (netip.Addr, bool) {
	switch {
	case addr.Is4In6():
		return addr.Unmap(), true
	case nat64Prefix.Contains(addr):
		octets := addr.As16()
		return netip.AddrFrom4([4]byte(octets[12:])), true
	}
	return netip.Addr{}, false
}

// parseIPv4 reads host the way inet_aton does — one to four parts, each
// decimal, octal with a leading 0, or hexadecimal with 0x, the last part
// filling every byte the earlier ones left — because Google's rules tell
// clients to accept any legal encoding, and a browser opens all of them.
func parseIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if host == "" || len(parts) > 4 {
		return "", false
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		value, ok := parseIPv4Part(part)
		if !ok {
			return "", false
		}
		values[i] = value
	}

	var addr uint64
	for i, value := range values[:len(values)-1] {
		if value > 0xff {
			return "", false
		}
		addr |= value << (8 * (3 - i))
	}
	last := values[len(values)-1]
	if last >= 1<<(8*(5-len(values))) {
		return "", false
	}
	addr |= last

	return netip.AddrFrom4([4]byte{
		byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr),
	}).String(), true
}

func parseIPv4Part(part string) (uint64, bool) {
	base := 10
	switch {
	case len(part) > 2 && (part[:2] == "0x" || part[:2] == "0X"):
		base, part = 16, part[2:]
	case len(part) > 1 && part[0] == '0':
		base, part = 8, part[1:]
	}
	value, err := strconv.ParseUint(part, base, 32)
	return value, err == nil
}

// canonicalPath resolves "/./" and "/../" and collapses runs of slashes. The
// query is never passed here: Google's rules leave its slashes alone, as the
// "?more//slashes" vector shows.
func canonicalPath(path string) string {
	if path == "" {
		return "/"
	}
	trailing := strings.HasSuffix(path, "/") ||
		strings.HasSuffix(path, "/.") || strings.HasSuffix(path, "/..")

	var segments []string
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}

	out := "/" + strings.Join(segments, "/")
	if trailing && len(segments) > 0 {
		out += "/"
	}
	return out
}

// unescapeFully percent-unescapes until nothing changes, Google's
// "repeatedly unescape" rule. Every pass that changes the string makes it
// shorter, so it terminates.
func unescapeFully(s string) string {
	for {
		next := unescapeOnce(s)
		if next == s {
			return s
		}
		s = next
	}
}

// unescapeOnce decodes every well-formed %XX once and leaves a stray % as it
// is, which is what lets "%%%25%32%35asd%%" settle into Google's expected
// "%%%asd%%" before re-escaping.
func unescapeOnce(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escape percent-escapes every byte at or below 0x20, at or above 0x7F, '#'
// and '%', in uppercase hex, and nothing else — Google's rule, which is not
// net/url's.
func escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || c == '#' || c == '%' {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiLower lowercases A to Z only. strings.ToLower would rewrite an invalid
// UTF-8 byte as U+FFFD, and those bytes are hashed as they are.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// expressions pairs every host suffix with every path prefix.
func (c canonicalURL) expressions() []string {
	hosts := hostSuffixes(c.host)
	paths := pathPrefixes(c.path, c.query)
	out := make([]string, 0, len(hosts)*len(paths))
	for _, host := range hosts {
		for _, path := range paths {
			out = append(out, host+path)
		}
	}
	return out
}

// hostSuffixes is the exact host plus up to four more, the way Google's v5
// rules choose them: start at the registrable domain (the eTLD+1, from the
// Public Suffix List) and add one leading label at a time. It is not a count of
// labels from the right, which is what v4 said and which differs wherever the
// public suffix has two labels: "co.uk/" is never an expression, and a host five
// labels deep under "example.co.uk" reaches one label further than a walk over
// the last five labels would. A public suffix on its own has no registrable
// domain, and an address no parent at all, so each is checked only as itself.
func hostSuffixes(host string) []string {
	suffixes := []string{host}
	if isIPHost(host) {
		return suffixes
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return suffixes
	}

	labels := strings.Split(host, ".")
	first := len(labels) - strings.Count(registrable, ".") - 1
	// Index 0 is the whole host, which is already the first entry.
	for i := first; i > 0 && i > first-4; i-- {
		suffixes = append(suffixes, strings.Join(labels[i:], "."))
	}
	return suffixes
}

func isIPHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is4()
}

// pathPrefixes is the path with its query (when there is one), the path
// without it, the root, and up to three directories below the root, each with
// its trailing slash.
func pathPrefixes(path, query string) []string {
	var prefixes []string
	add := func(p string) {
		if !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	if query != "" {
		add(path + "?" + query)
	}
	add(path)
	add("/")

	directories := strings.Split(strings.TrimPrefix(path, "/"), "/")
	prefix := "/"
	for i := 0; i < len(directories)-1 && i < 3; i++ {
		prefix += directories[i] + "/"
		add(prefix)
	}
	return prefixes
}
