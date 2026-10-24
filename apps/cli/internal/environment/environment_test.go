package environment_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/environment"
)

// The variable names are repeated here, not imported, so renaming one in the
// package breaks a test instead of silently making an override that people
// have in their shell profiles do nothing.
const (
	varAPIURL         = "KURZE_URL_API_URL"
	varSupabaseURL    = "KURZE_URL_SUPABASE_URL"
	varPublishableKey = "KURZE_URL_SUPABASE_PUBLISHABLE_KEY"
	varClientID       = "KURZE_URL_CLIENT_ID"
	varSharedHost     = "KURZE_URL_SHARED_HOST"
	varVercelBypass   = "KURZE_URL_VERCEL_BYPASS"
)

// getenvFrom answers like os.Getenv would for a process that has exactly vars
// set, so no test reads or changes the real environment.
func getenvFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// TestFromEnvDefaults is the production case: nothing is set, so every field
// is the compiled-in value. The Name literal is the one thing repeated on
// purpose, because it keys every stored login: changing the default Supabase
// URL changes it, and every person would have to log in again.
func TestFromEnvDefaults(t *testing.T) {
	got, err := environment.FromEnv(getenvFrom(nil))
	require.NoError(t, err)

	require.Equal(t, environment.Environment{
		APIURL:         environment.DefaultAPIURL,
		SupabaseURL:    environment.DefaultSupabaseURL,
		PublishableKey: environment.DefaultPublishableKey,
		ClientID:       environment.DefaultClientID,
		SharedHost:     environment.DefaultSharedHost,
		VercelBypass:   "",
		Name:           "yfgzuzxgsghieoavpluy-supabase-co",
	}, got)
}

// TestFromEnvOverrides sets every variable at once. The Supabase URL is the
// local stack's, whose trailing slash and port are what the Name rule has to
// get right.
func TestFromEnvOverrides(t *testing.T) {
	got, err := environment.FromEnv(getenvFrom(map[string]string{
		varAPIURL:         "https://kurze-url-api-git-feat-x.vercel.app/",
		varSupabaseURL:    "http://127.0.0.1:54321/",
		varPublishableKey: "sb_publishable_override",
		varClientID:       "11111111-2222-3333-4444-555555555555",
		varSharedHost:     "SHORT.INVALID",
		varVercelBypass:   "bypass-value",
	}))
	require.NoError(t, err)

	require.Equal(t, environment.Environment{
		APIURL:         "https://kurze-url-api-git-feat-x.vercel.app",
		SupabaseURL:    "http://127.0.0.1:54321",
		PublishableKey: "sb_publishable_override",
		ClientID:       "11111111-2222-3333-4444-555555555555",
		SharedHost:     "short.invalid",
		VercelBypass:   "bypass-value",
		Name:           "127-0-0-1-54321",
	}, got)
}

// TestFromEnvOverridesOneVariableAtATime proves that each variable is read on
// its own and the rest keep their defaults, which is how a preview run is
// usually set up: the API URL alone, say.
func TestFromEnvOverridesOneVariableAtATime(t *testing.T) {
	base, err := environment.FromEnv(getenvFrom(nil))
	require.NoError(t, err)

	t.Run("API URL", func(t *testing.T) {
		got, err := environment.FromEnv(getenvFrom(map[string]string{varAPIURL: "http://localhost:8080"}))
		require.NoError(t, err)
		want := base
		want.APIURL = "http://localhost:8080"
		require.Equal(t, want, got)
	})
	t.Run("publishable key", func(t *testing.T) {
		got, err := environment.FromEnv(getenvFrom(map[string]string{varPublishableKey: "k"}))
		require.NoError(t, err)
		want := base
		want.PublishableKey = "k"
		require.Equal(t, want, got)
	})
	t.Run("client ID", func(t *testing.T) {
		got, err := environment.FromEnv(getenvFrom(map[string]string{varClientID: "c"}))
		require.NoError(t, err)
		want := base
		want.ClientID = "c"
		require.Equal(t, want, got)
	})
	t.Run("Vercel bypass", func(t *testing.T) {
		got, err := environment.FromEnv(getenvFrom(map[string]string{varVercelBypass: "b"}))
		require.NoError(t, err)
		want := base
		want.VercelBypass = "b"
		require.Equal(t, want, got)
	})
}

// TestFromEnvTreatsBlankAsUnset covers a variable exported empty, which shells
// and CI systems do all the time, and one holding only whitespace, such as a
// value pasted with its trailing newline. Both mean the default, not an
// override that is then refused or, worse, sent as a blank header.
func TestFromEnvTreatsBlankAsUnset(t *testing.T) {
	blank := map[string]string{
		varAPIURL:         "",
		varSupabaseURL:    "  ",
		varPublishableKey: "\n",
		varClientID:       "\t",
		varSharedHost:     " ",
		varVercelBypass:   "\n",
	}
	got, err := environment.FromEnv(getenvFrom(blank))
	require.NoError(t, err)

	want, err := environment.FromEnv(getenvFrom(nil))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestFromEnvTrimsSurroundingWhitespace is the other half: a real value with
// a newline or spaces around it is the value, not an invalid URL and not a
// header that net/http would refuse to send.
func TestFromEnvTrimsSurroundingWhitespace(t *testing.T) {
	got, err := environment.FromEnv(getenvFrom(map[string]string{
		varAPIURL:         " https://api.example.test/\n",
		varPublishableKey: "sb_publishable_x\n",
		varVercelBypass:   "bypass\n",
	}))
	require.NoError(t, err)
	require.Equal(t, "https://api.example.test", got.APIURL)
	require.Equal(t, "sb_publishable_x", got.PublishableKey)
	require.Equal(t, "bypass", got.VercelBypass)
}

// TestFromEnvTrimsExactlyOneTrailingSlash pins the rule's size: a base URL is
// joined with paths that start with a slash, so one slash goes, and a second
// one is the caller's own doing and stays visible.
func TestFromEnvTrimsExactlyOneTrailingSlash(t *testing.T) {
	got, err := environment.FromEnv(getenvFrom(map[string]string{varAPIURL: "https://api.example.test//"}))
	require.NoError(t, err)
	require.Equal(t, "https://api.example.test/", got.APIURL)
}

// TestFromEnvRejectsARelativeURL covers everything that is not an absolute
// http or https URL with a host, for both variables. Each case is something
// a person plausibly types: a bare host, a path, another scheme, a scheme
// with no host, and a host:port that URL parsing reads as a scheme.
func TestFromEnvRejectsARelativeURL(t *testing.T) {
	bad := []string{
		"api.kurze-url.app",
		"/v1",
		"//api.kurze-url.app",
		"localhost:8080",
		"ftp://api.kurze-url.app",
		"file:///etc/hosts",
		"https://",
		"https:///v1",
		"https:api.kurze-url.app",
		"http://:8080",
		"http://host:notaport",
		"https://exa mple.test",
	}
	for _, variable := range []string{varAPIURL, varSupabaseURL} {
		for _, value := range bad {
			t.Run(variable+"="+value, func(t *testing.T) {
				_, err := environment.FromEnv(getenvFrom(map[string]string{variable: value}))
				require.Error(t, err)

				var invalid *environment.InvalidURLError
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, variable, invalid.Variable)
				require.Contains(t, err.Error(), variable)
			})
		}
	}
}

// TestFromEnvErrorsNeverEchoASecret holds the line the global constraints
// draw: an error that names the variable never prints a value. The bypass
// and the key are set to sentinels beside an invalid URL, and the URL itself
// carries one as userinfo, which url.Parse's own error would quote.
func TestFromEnvErrorsNeverEchoASecret(t *testing.T) {
	const (
		bypass = "bypass-sentinel-8c1f"
		key    = "key-sentinel-4d27" // gitleaks:allow -- a test sentinel, not a key
		userin = "userinfo-sentinel-91ab"
	)

	for _, bad := range []string{
		"not a url",
		"ftp://user:" + userin + "@host.example.test",
		"http://user:" + userin + "@host:notaport",
	} {
		t.Run(bad, func(t *testing.T) {
			_, err := environment.FromEnv(getenvFrom(map[string]string{
				varAPIURL:         bad,
				varPublishableKey: key,
				varVercelBypass:   bypass,
			}))
			require.Error(t, err)
			for _, secret := range []string{bypass, key, userin} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}

// TestNameIsDerivedFromTheSupabaseHost is the credential key's rule on its
// own: lowercase host and port, every other character a hyphen, whatever the
// scheme, path, case or port of the URL.
func TestNameIsDerivedFromTheSupabaseHost(t *testing.T) {
	tests := []struct {
		supabaseURL string
		name        string
	}{
		{"https://yfgzuzxgsghieoavpluy.supabase.co", "yfgzuzxgsghieoavpluy-supabase-co"},
		{"https://yfgzuzxgsghieoavpluy.supabase.co/", "yfgzuzxgsghieoavpluy-supabase-co"},
		{"http://127.0.0.1:54321", "127-0-0-1-54321"},
		{"HTTPS://Preview-Ref.Supabase.CO", "preview-ref-supabase-co"},
		{"https://proxy.example.test:8443/auth", "proxy-example-test-8443"},
		{"http://[::1]:54321", "---1--54321"},
		{"https://bücher.example", "b-cher-example"},
	}
	for _, tt := range tests {
		t.Run(tt.supabaseURL, func(t *testing.T) {
			got, err := environment.FromEnv(getenvFrom(map[string]string{varSupabaseURL: tt.supabaseURL}))
			require.NoError(t, err)
			require.Equal(t, tt.name, got.Name)
		})
	}
}

// TestNameIsSafeAsAKeychainUserAndAFileName is the property the credentials
// package relies on and does not check: a Name is never empty (it would put
// the tokens in credentials-.json) and holds nothing but lowercase letters,
// digits and hyphens (no separator, no dot, no way out of the directory).
func TestNameIsSafeAsAKeychainUserAndAFileName(t *testing.T) {
	safe := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, supabaseURL := range []string{
		environment.DefaultSupabaseURL,
		"http://127.0.0.1:54321/",
		"https://a.b",
		"https://../..:80",
		"https://exa%2Fmple.test",
		"http://[2001:db8::1]:8080",
		"https://名前.example",
	} {
		got, err := environment.FromEnv(getenvFrom(map[string]string{varSupabaseURL: supabaseURL}))
		if err != nil {
			continue // a URL that is refused has no Name to be unsafe
		}
		require.Regexp(t, safe, got.Name, supabaseURL)
	}
}

// TestDifferentSupabaseProjectsGetDifferentNames is the reason the key comes
// from the URL at all: switching overrides must never reuse another
// environment's token.
func TestDifferentSupabaseProjectsGetDifferentNames(t *testing.T) {
	production, err := environment.FromEnv(getenvFrom(nil))
	require.NoError(t, err)
	local, err := environment.FromEnv(getenvFrom(map[string]string{varSupabaseURL: "http://127.0.0.1:54321"}))
	require.NoError(t, err)
	require.NotEqual(t, production.Name, local.Name)
}

// TestOverridingOnlyTheAPIKeepsTheCredentialKey: the API URL is not part of
// the credential key, because the tokens belong to the Supabase project that
// issued them, and a preview API in front of production's Supabase accepts
// the same login.
func TestOverridingOnlyTheAPIKeepsTheCredentialKey(t *testing.T) {
	production, err := environment.FromEnv(getenvFrom(nil))
	require.NoError(t, err)
	preview, err := environment.FromEnv(getenvFrom(map[string]string{varAPIURL: "https://preview.example.test"}))
	require.NoError(t, err)
	require.Equal(t, production.Name, preview.Name)
}

// TestDefaultsAreFilledIn guards against a build that ships without the
// values the maintainer supplied. The shapes are checked as well, because a
// key pasted from the wrong place compiles just as well as the right one: a
// Supabase secret key would be a leak in a public binary, and a client ID
// that is not a UUID would fail only at the first login.
func TestDefaultsAreFilledIn(t *testing.T) {
	require.NotEmpty(t, environment.DefaultClientID)
	require.NotEmpty(t, environment.DefaultPublishableKey)
	require.NotEmpty(t, environment.DefaultAPIURL)
	require.NotEmpty(t, environment.DefaultSupabaseURL)
	require.NotEmpty(t, environment.DefaultSharedHost)

	require.True(t, strings.HasPrefix(environment.DefaultPublishableKey, "sb_publishable_"),
		"the compiled-in key must be a publishable key, never a secret or service-role key")
	require.Regexp(t,
		`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
		environment.DefaultClientID)
	require.Equal(t, strings.ToLower(environment.DefaultSharedHost), environment.DefaultSharedHost)
}
