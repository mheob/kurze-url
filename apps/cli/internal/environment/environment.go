// Package environment decides which instance the CLI talks to: the values
// compiled in for production, the KURZE_URL_* overrides that point a build at
// a local stack or a preview, and the name that keeps one instance's stored
// login apart from another's.
//
// The package reads no process state. FromEnv takes the lookup function as a
// parameter (os.Getenv in production), so a test hands it a map and the real
// environment is never read or changed.
package environment

import (
	"fmt"
	"net/url"
	"strings"
)

// The production values, compiled in so a downloaded binary works with no
// configuration at all. An override replaces one of them for development or
// a preview; none is ever required.
const (
	// DefaultAPIURL is the host that serves /v1. It is not the short-link
	// host: api.kurze-url.app answers the API, and redirects are answered
	// on DefaultSharedHost.
	DefaultAPIURL = "https://api.kurze-url.app"

	// DefaultSupabaseURL is the production Supabase project, which runs the
	// OAuth server the CLI logs in through. The environment name that keys
	// the stored credentials is derived from it, so changing this value logs
	// every existing user out.
	DefaultSupabaseURL = "https://yfgzuzxgsghieoavpluy.supabase.co"

	// DefaultPublishableKey is the production project's publishable key, sent
	// as the apikey header on every Supabase auth call. It is public by
	// design: the web app ships the very same key to every browser, and it
	// only identifies the project to Supabase's gateway, with no privilege
	// beyond what any anonymous caller has. It is not the service-role key,
	// so compiling it into a public binary leaks nothing. A secret key must
	// never be put here.
	DefaultPublishableKey = "sb_publishable_7ISzw8Y0Ibg4DYw4BgFi5A_NWuq4GQn"

	// DefaultClientID is the CLI's OAuth client at the production Supabase
	// project. The client is public (no secret exists), and the ID appears
	// in every authorize URL the CLI opens, so it is public by design too.
	DefaultClientID = "899156e1-0482-4c7c-ab66-55610e80b9e1"

	// DefaultSharedHost is the hostname of the instance's shared short-link
	// domain. A link created on it carries no domain_id.
	DefaultSharedHost = "go.kurze-url.app"
)

// The variables FromEnv reads. They are named in errors, never their values.
const (
	envAPIURL         = "KURZE_URL_API_URL"
	envSupabaseURL    = "KURZE_URL_SUPABASE_URL"
	envPublishableKey = "KURZE_URL_SUPABASE_PUBLISHABLE_KEY"
	envClientID       = "KURZE_URL_CLIENT_ID"
	envSharedHost     = "KURZE_URL_SHARED_HOST"
	envVercelBypass   = "KURZE_URL_VERCEL_BYPASS"
)

// Environment is one instance of the service as the CLI sees it: where its
// API and its login server are, which client and key identify the CLI to the
// latter, and what to call it in the credential store.
//
// PublishableKey and VercelBypass are credentials of a sort: the first is
// public by design (see DefaultPublishableKey), the second is not, because
// it lets a request past Vercel's deployment protection. Neither is ever
// printed, logged or put into an error by this package, and a caller should
// not print the struct whole.
type Environment struct {
	// APIURL is the base of the /v1 API, with no trailing slash.
	APIURL string
	// SupabaseURL is the base of the Supabase project, with no trailing
	// slash. The OAuth endpoints are found from it by discovery.
	SupabaseURL string
	// PublishableKey is sent as the apikey header on every Supabase auth
	// call.
	PublishableKey string
	// ClientID is the CLI's OAuth client ID at SupabaseURL.
	ClientID string
	// SharedHost is the lowercase hostname of the shared short-link domain.
	SharedHost string
	// VercelBypass is the deployment-protection bypass secret that API
	// previews need, sent as x-vercel-protection-bypass. Empty means none,
	// which is also what production wants: there is no compiled-in default.
	VercelBypass string
	// Name identifies the instance in the credential store: the lowercase
	// host and port of SupabaseURL with every character outside [a-z0-9]
	// replaced by a hyphen. It is the keychain user and part of the fallback
	// file's name, so it holds no separator, no dot and is never empty, and
	// a login against one Supabase project can never be read back under
	// another. It comes from the Supabase URL, not the API URL, because the
	// tokens belong to the project that issued them.
	Name string
}

// InvalidURLError is returned when a URL override is not an absolute http or
// https URL with a host. It names the variable and nothing else: the value
// is left out on purpose, because a URL may carry a password as userinfo and
// url.Parse's own error quotes the whole string. The command layer turns it
// into a message in the person's language, so the text here is for logs.
type InvalidURLError struct {
	// Variable is the environment variable that held the value.
	Variable string
}

// Error describes the rule without echoing the value.
func (e *InvalidURLError) Error() string {
	return fmt.Sprintf("environment: %s must be an absolute http or https URL with a host", e.Variable)
}

// FromEnv builds the Environment from the process environment, which the
// caller passes as getenv (os.Getenv in production).
//
// A variable that is unset, empty or only whitespace means the compiled-in
// default; any other value is trimmed of surrounding whitespace, so a value
// pasted with its newline is not an invalid URL and not a header that
// net/http would refuse to send. The two URLs must be absolute http or https
// URLs with a host, or the result is an *InvalidURLError, and one trailing
// slash is trimmed so a base URL can be joined with a path that starts with
// one. The shared host is lowercased, because hostnames are stored that way
// and compared against it.
func FromEnv(getenv func(string) string) (Environment, error) {
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}

	apiURL, _, err := baseURL(envAPIURL, value(envAPIURL, DefaultAPIURL))
	if err != nil {
		return Environment{}, err
	}
	supabaseURL, supabase, err := baseURL(envSupabaseURL, value(envSupabaseURL, DefaultSupabaseURL))
	if err != nil {
		return Environment{}, err
	}

	return Environment{
		APIURL:         apiURL,
		SupabaseURL:    supabaseURL,
		PublishableKey: value(envPublishableKey, DefaultPublishableKey),
		ClientID:       value(envClientID, DefaultClientID),
		SharedHost:     strings.ToLower(value(envSharedHost, DefaultSharedHost)),
		VercelBypass:   value(envVercelBypass, ""),
		Name:           nameOf(supabase),
	}, nil
}

// baseURL checks that raw is an absolute http or https URL with a host and
// returns it with one trailing slash trimmed, together with its parse.
//
// The host test is on Hostname, not Host, because "http://:8080" has a Host
// (the port) and nothing to connect to. The parse error is dropped, not
// wrapped: it quotes the input, which may hold a password.
func baseURL(variable, raw string) (string, *url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", nil, &InvalidURLError{Variable: variable}
	}
	return strings.TrimSuffix(raw, "/"), u, nil
}

// nameOf derives the credential-store name from a Supabase URL that baseURL
// has already accepted. Its host is therefore not empty, so neither is the
// name: every character of it becomes one of [a-z0-9-], which is what makes
// the name safe as a keychain user and in a file name.
//
// Different hosts can in principle map to the same name ("a.b" and "a-b"),
// which is accepted: Supabase hosts are a project reference and a fixed
// suffix, and the only other host in use is the local stack's.
func nameOf(supabase *url.URL) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(supabase.Host))
}
