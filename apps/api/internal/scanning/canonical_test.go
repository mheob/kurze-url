package scanning_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// Google's own canonicalization examples, copied verbatim. They are the only
// specification of the edge cases that counts: a destination canonicalized
// differently from the way Google canonicalized its list entry hashes to a
// different value, and a listed phishing site then passes as clean with no
// error anywhere.
//
// The vectors come from Google's v4 "URLs and Hashing" page, which carries all
// 33 of them. The v5 page, the one this package follows, lists none: it keeps
// the same rules in prose, and the vectors still illustrate them.
func TestCanonicalizeMatchesGooglesExamples(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://host/%25%32%35", "http://host/%25"},
		{"http://host/%25%32%35%25%32%35", "http://host/%25%25"},
		{"http://host/%2525252525252525", "http://host/%25"},
		{"http://host/asdf%25%32%35asd", "http://host/asdf%25asd"},
		{"http://host/%%%25%32%35asd%%", "http://host/%25%25%25asd%25%25"},
		{"http://www.google.com/", "http://www.google.com/"},
		{
			"http://%31%36%38%2e%31%38%38%2e%39%39%2e%32%36/%2E%73%65%63%75%72%65/%77%77%77%2E%65%62%61%79%2E%63%6F%6D/",
			"http://168.188.99.26/.secure/www.ebay.com/",
		},
		{
			"http://195.127.0.11/uploads/%20%20%20%20/.verify/.eBaysecure=updateuserdataxplimnbqmn-xplmvalidateinfoswqpcmlx=hgplmcx/",
			"http://195.127.0.11/uploads/%20%20%20%20/.verify/.eBaysecure=updateuserdataxplimnbqmn-xplmvalidateinfoswqpcmlx=hgplmcx/",
		},
		{
			"http://host%23.com/%257Ea%2521b%2540c%2523d%2524e%25f%255E00%252611%252A22%252833%252944_55%252B",
			"http://host%23.com/~a!b@c%23d$e%25f^00&11*22(33)44_55+",
		},
		{"http://3279880203/blah", "http://195.127.0.11/blah"},
		{"http://www.google.com/blah/..", "http://www.google.com/"},
		{"www.google.com/", "http://www.google.com/"},
		{"www.google.com", "http://www.google.com/"},
		{"http://www.evil.com/blah#frag", "http://www.evil.com/blah"},
		{"http://www.GOOgle.com/", "http://www.google.com/"},
		{"http://www.google.com.../", "http://www.google.com/"},
		{"http://www.google.com/foo\tbar\rbaz\n2", "http://www.google.com/foobarbaz2"},
		{"http://www.google.com/q?", "http://www.google.com/q?"},
		{"http://www.google.com/q?r?", "http://www.google.com/q?r?"},
		{"http://www.google.com/q?r?s", "http://www.google.com/q?r?s"},
		{"http://evil.com/foo#bar#baz", "http://evil.com/foo"},
		{"http://evil.com/foo;", "http://evil.com/foo;"},
		{"http://evil.com/foo?bar;", "http://evil.com/foo?bar;"},
		{"http://\x01\x80.com/", "http://%01%80.com/"},
		{"http://notrailingslash.com", "http://notrailingslash.com/"},
		{"http://www.gotaport.com:1234/", "http://www.gotaport.com/"},
		{"  http://www.google.com/  ", "http://www.google.com/"},
		{"http:// leadingspace.com/", "http://%20leadingspace.com/"},
		{"http://%20leadingspace.com/", "http://%20leadingspace.com/"},
		{"%20leadingspace.com/", "http://%20leadingspace.com/"},
		{"https://www.securesite.com/", "https://www.securesite.com/"},
		{"http://host.com/ab%23cd", "http://host.com/ab%23cd"},
		{"http://host.com//twoslashes?more//slashes", "http://host.com/twoslashes?more//slashes"},
	} {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			got, err := scanning.Canonicalize(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// The spellings a Verein actually types. Google's table has no IDN, no
// userinfo, no IPv6 and no query with a percent-encoded umlaut, and each of
// those is a way for a listed host to slip past unhashed.
func TestCanonicalizeRealWorldDestinations(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// Google's rules: an internationalized domain name becomes Punycode.
		{"https://Bücher.de/", "https://xn--bcher-kva.de/"},
		{"https://BÜCHER.DE/Über", "https://xn--bcher-kva.de/%C3%9Cber"},
		{"https://WWW.Verein.DE/Mitglieder", "https://www.verein.de/Mitglieder"},
		{"https://%76erein.de/", "https://verein.de/"},
		{"https://kasse:geheim@verein.de/", "https://verein.de/"},
		{"https://verein.de:8443/x", "https://verein.de/x"},
		{"https://verein.de./x", "https://verein.de/x"},
		{"https://[2001:DB8:0:0::1]:443/x", "https://[2001:db8::1]/x"},
		{"https://verein.de/a/./b/../c", "https://verein.de/a/c"},
		{"https://verein.de/anmeldung?name=M%C3%BCller#oben", "https://verein.de/anmeldung?name=M%C3%BCller"},
		{"https://0x7f.1/", "https://127.0.0.1/"},
		// IDNA maps the ideographic full stop (U+3002), the fullwidth one
		// (U+FF0E) and the halfwidth ideographic one (U+FF61) to ".", and
		// browsers open the host that results. The dots they produce are
		// trimmed and collapsed like any other.
		{"https://verein.de。/x", "https://verein.de/x"},
		{"https://verein.de%E3%80%82/x", "https://verein.de/x"},
		{"https://。verein.de/x", "https://verein.de/x"},
		{"https://verein.de。。/x", "https://verein.de/x"},
		{"https://verein。。de/x", "https://verein.de/x"},
		{"https://verein．de｡/x", "https://verein.de/x"},
		// A browser does not apply the STD3 or hyphen rules of the lookup
		// profile, so a host they would refuse is still the host that opens.
		{"https://bücher_x.evil.com/", "https://xn--bcher_x-n2a.evil.com/"},
		{"https://ab--ü.evil.com/", "https://xn--ab---3ra.evil.com/"},
		{"https://-ü.evil.com/", "https://xn----eha.evil.com/"},
		{"https://faß.de/", "https://xn--fa-hia.de/"},
		// Only bytes up to 0x20 are trimmed, as Google's rules and browsers do;
		// Unicode whitespace is part of the path and gets escaped with it.
		{"https://verein.de/x\u3000", "https://verein.de/x%E3%80%80"},
		{"https://verein.de/x\u00a0 ", "https://verein.de/x%C2%A0"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scanning.Canonicalize(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// A trailing ideographic full stop must not cost the host its own expression:
// a registrable-domain lookup refuses a host that ends in an empty label, and
// "evil.com/" would then never be hashed for a URL a browser opens at evil.com.
func TestExpressionsSurviveAnIdeographicTrailingDot(t *testing.T) {
	for _, in := range []string{"https://evil.com。/login", "https://evil.com%E3%80%82/login", "https://www.evil.com。。/login"} {
		t.Run(in, func(t *testing.T) {
			got, err := scanning.Expressions(in)
			require.NoError(t, err)
			require.Contains(t, got, "evil.com/")
			require.Contains(t, got, "evil.com/login")
			for _, expression := range got {
				require.NotContains(t, expression, "..")
				require.NotContains(t, expression, ".//")
			}
		})
	}
}

// Google's v5 page states these IPv6 rules in prose and gives no vector for
// any of them: leading zeros dropped, zero components collapsed to "::", and
// an IPv4-mapped or well-known-prefix NAT64 address read as the IPv4 address
// it carries. The last two matter beyond tidiness: a listed address spelled
// as IPv6 is otherwise a way to reach it unhashed.
func TestCanonicalizeNormalizesIPv6AsGooglesV5Page(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://[0:0:0:0:0:0:0:1]/", "https://[::1]/"},
		{"https://[2001:0db8:0000:0000:0000:ff00:0042:8329]/", "https://[2001:db8::ff00:42:8329]/"},
		{"https://[::ffff:1.2.3.4]/", "https://1.2.3.4/"},
		{"https://[::FFFF:0102:0304]:8443/x", "https://1.2.3.4/x"},
		{"https://[64:ff9b::1.2.3.4]/", "https://1.2.3.4/"},
		{"https://[64:FF9B::102:304]/x?y", "https://1.2.3.4/x?y"},
		// Only the well-known /96 is NAT64; its neighbour is an ordinary address.
		{"https://[64:ff9b:1::1.2.3.4]/", "https://[64:ff9b:1::102:304]/"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scanning.Canonicalize(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCanonicalizeRejectsAURLWithoutAHost(t *testing.T) {
	_, err := scanning.Canonicalize("https:///nohost")
	require.ErrorIs(t, err, scanning.ErrNoHost)
}

// Google's own expression examples. The order is not part of the protocol —
// every expression is hashed and looked up — so only the set is compared.
//
// The first three are the v4 page's, the last three the v5 page's. Both
// generations agree wherever the top-level domain has one label; they part
// where the public suffix has two (see TestExpressionsWalkUpFromTheRegistrableDomain).
func TestExpressionsMatchGooglesExamples(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"http://a.b.c/1/2.html?param=1", []string{
			"a.b.c/1/2.html?param=1", "a.b.c/1/2.html", "a.b.c/", "a.b.c/1/",
			"b.c/1/2.html?param=1", "b.c/1/2.html", "b.c/", "b.c/1/",
		}},
		{"http://a.b.c.d.e.f.g/1.html", []string{
			"a.b.c.d.e.f.g/1.html", "a.b.c.d.e.f.g/",
			"c.d.e.f.g/1.html", "c.d.e.f.g/",
			"d.e.f.g/1.html", "d.e.f.g/",
			"e.f.g/1.html", "e.f.g/",
			"f.g/1.html", "f.g/",
		}},
		{"http://1.2.3.4/1/", []string{"1.2.3.4/1/", "1.2.3.4/"}},
		{"http://a.b.com/1/2.html?param=1", []string{
			"a.b.com/1/2.html?param=1", "a.b.com/1/2.html", "a.b.com/", "a.b.com/1/",
			"b.com/1/2.html?param=1", "b.com/1/2.html", "b.com/", "b.com/1/",
		}},
		{"http://example.co.uk/1", []string{"example.co.uk/1", "example.co.uk/"}},
		{"http://a.b.c.d.e.f.com/1.html", []string{
			"a.b.c.d.e.f.com/1.html", "a.b.c.d.e.f.com/",
			"c.d.e.f.com/1.html", "c.d.e.f.com/",
			"d.e.f.com/1.html", "d.e.f.com/",
			"e.f.com/1.html", "e.f.com/",
			"f.com/1.html", "f.com/",
		}},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scanning.Expressions(tc.in)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

// Google's v5 page derives host suffixes from the registrable domain, not from
// a count of labels: "starting with the eTLD+1 domain and adding successive
// leading components", the eTLD+1 taken from the Public Suffix List. A v4 style
// walk over the last five labels would check "co.uk/" — a name no one registers
// — and stop one label short of the deepest host Google lists.
func TestExpressionsWalkUpFromTheRegistrableDomain(t *testing.T) {
	got, err := scanning.Expressions("https://a.b.c.d.e.example.co.uk/x")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"a.b.c.d.e.example.co.uk/x", "a.b.c.d.e.example.co.uk/",
		"c.d.e.example.co.uk/x", "c.d.e.example.co.uk/",
		"d.e.example.co.uk/x", "d.e.example.co.uk/",
		"e.example.co.uk/x", "e.example.co.uk/",
		"example.co.uk/x", "example.co.uk/",
	}, got)

	got, err = scanning.Expressions("https://www.example.co.uk/")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"www.example.co.uk/", "example.co.uk/"}, got)

	// A public suffix has no registrable domain, so there is nothing to walk
	// up to: only the host itself is checked.
	got, err = scanning.Expressions("https://co.uk/a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"co.uk/a", "co.uk/"}, got)
}

// Five hosts times six paths is the ceiling: the exact host plus up to four
// from the registrable domain upwards, never the bare top-level domain; the
// path with and without its query, the root, and up to three directories.
func TestExpressionsNeverExceedThirty(t *testing.T) {
	got, err := scanning.Expressions("https://a.b.c.d.e.f.g/1/2/3/4/5.html?q=1")
	require.NoError(t, err)
	require.Len(t, got, 30)
	require.Contains(t, got, "a.b.c.d.e.f.g/1/2/3/4/5.html?q=1")
	require.Contains(t, got, "f.g/1/2/3/")
	require.NotContains(t, got, "b.c.d.e.f.g/", "only four hosts above the registrable domain are walked")
	require.NotContains(t, got, "g/", "the bare top-level domain is never an expression")
	require.NotContains(t, got, "a.b.c.d.e.f.g/1/2/3/4/", "at most three directories below the root")
}

// An address has no parent domain to walk up to.
func TestExpressionsCheckAnIPHostOnlyExactly(t *testing.T) {
	got, err := scanning.Expressions("https://203.0.113.7/a/b.html?x=1")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"203.0.113.7/a/b.html?x=1", "203.0.113.7/a/b.html", "203.0.113.7/", "203.0.113.7/a/",
	}, got)

	got, err = scanning.Expressions("https://[2001:db8::1]/a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"[2001:db8::1]/a", "[2001:db8::1]/"}, got)

	// An IPv6 spelling of an IPv4 address canonicalizes to that address, so it
	// is checked as one.
	got, err = scanning.Expressions("https://[::ffff:203.0.113.7]/a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"203.0.113.7/a", "203.0.113.7/"}, got)
}
