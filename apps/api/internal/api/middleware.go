package api

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP resolves the caller's address. On Vercel the platform sets
// X-Forwarded-For, and the request cannot reach the process without passing
// through it, so the leftmost entry is trustworthy there. Outside a trusted
// proxy this header is spoofable — which is why it is only ever used as a
// rate-limit key and as visitor-hash input, never stored or logged.
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Hostname strips any port from a Host header so it can be compared with the
// domain.hostname column, which never carries one.
func Hostname(hostHeader string) string {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
