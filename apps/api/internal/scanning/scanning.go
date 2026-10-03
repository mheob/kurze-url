// Package scanning checks link destinations against Google Safe Browsing, the
// one package in this module that talks to it.
//
// It uses the v5 hashes.search method, never urls.search: only the first four
// bytes of the SHA-256 of each canonicalized URL expression leave this
// process, never a URL. A destination can carry personal data — a prefilled
// form, a member's own page — and Google's terms let it reuse and share URLs
// sent to urls.search, but not hash prefixes. The price is that this package
// canonicalizes, expands, hashes and compares full hashes itself, exactly as
// Google's "URLs and Hashing" rules describe.
package scanning
