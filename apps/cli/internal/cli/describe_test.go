package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/environment"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// problem builds the API's answer for a status, with the faults it lists.
func problem(status int, detail string, errs ...apiclient.ProblemDetail) *apiclient.ProblemError {
	return &apiclient.ProblemError{
		Status: status,
		Title:  http.StatusText(status),
		Detail: detail,
		Errors: errs,
	}
}

func TestDescribeMapsEveryKnownCase(t *testing.T) {
	refused := &url.Error{Op: "Get", URL: "http://127.0.0.1:9/v1/me", Err: syscall.ECONNREFUSED}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "login required",
			err:  auth.ErrLoginRequired,
			want: "Please run `kurze-url login` again.",
		},
		{
			name: "login required deep in a chain",
			err:  fmt.Errorf("get the access token: %w", fmt.Errorf("%w: nothing to refresh with", auth.ErrLoginRequired)),
			want: "Please run `kurze-url login` again.",
		},
		{
			name: "the API refused the token",
			err:  problem(http.StatusUnauthorized, "token expired"),
			want: "Please run `kurze-url login` again.",
		},
		{
			name: "a Vercel protection page has no title",
			err:  &apiclient.ProblemError{Status: http.StatusUnauthorized},
			want: "Please run `kurze-url login` again.",
		},
		{
			name: "forbidden",
			err:  problem(http.StatusForbidden, "you may not"),
			want: "Not found, or you have no access to it.",
		},
		{
			name: "not found",
			err:  problem(http.StatusNotFound, "no such link"),
			want: "Not found, or you have no access to it.",
		},
		{
			name: "rate limited",
			err:  problem(http.StatusTooManyRequests, "too many QR codes; try again shortly"),
			want: "Too many requests just now. Please wait a minute and try again.",
		},
		{
			name: "QR contrast",
			err: problem(http.StatusUnprocessableEntity, "the two colours are too close together",
				apiclient.ProblemDetail{Location: "query.fg", Value: "low_contrast"}),
			want: "The two colours are too close together for a camera to read the code.",
		},
		{
			name: "QR foreground is not a colour",
			err: problem(http.StatusUnprocessableEntity, "colours must be given as rrggbb",
				apiclient.ProblemDetail{Location: "query.fg", Value: "invalid_color"}),
			want: "A colour must be six hexadecimal digits, such as 1a3a6e.",
		},
		{
			name: "QR background is not a colour",
			err: problem(http.StatusUnprocessableEntity, "colours must be given as rrggbb",
				apiclient.ProblemDetail{Location: "query.bg", Value: "invalid_color"}),
			want: "A colour must be six hexadecimal digits, such as 1a3a6e.",
		},
		{
			name: "invalid destination",
			err: problem(http.StatusUnprocessableEntity, "validation failed",
				apiclient.ProblemDetail{Location: "body.destination_url", Message: "must be https", Value: "http://example.org"}),
			want: "The destination must be a complete https:// address.",
		},
		{
			name: "invalid destination with any value",
			err: problem(http.StatusUnprocessableEntity, "validation failed",
				apiclient.ProblemDetail{Location: "body.destination_url"}),
			want: "The destination must be a complete https:// address.",
		},
		{
			name: "another 422 shows the API's own words",
			err: problem(http.StatusUnprocessableEntity, "the slug may not be reserved",
				apiclient.ProblemDetail{Location: "body.slug", Value: "api"}),
			want: "the slug may not be reserved",
		},
		{
			name: "a 409 is not translated here",
			err:  problem(http.StatusConflict, "that slug is taken"),
			want: "that slug is taken",
		},
		{
			name: "a problem with no detail shows its title",
			err:  &apiclient.ProblemError{Status: http.StatusBadGateway, Title: "The proxy failed"},
			want: "The proxy failed",
		},
		{
			name: "a problem with nothing shows the status text",
			err:  &apiclient.ProblemError{Status: http.StatusBadGateway},
			want: "Bad Gateway",
		},
		{
			name: "a problem wrapped in context",
			err:  fmt.Errorf("list the links: %w", problem(http.StatusNotFound, "x")),
			want: "Not found, or you have no access to it.",
		},
		{
			name: "no free port",
			err:  fmt.Errorf("%w (tried [53682]): address in use", auth.ErrPortsBusy),
			want: "No free port for the sign-in callback. Close the program that uses it and try again.",
		},
		{
			name: "state mismatch",
			err:  auth.ErrStateMismatch,
			want: "The sign-in could not be verified, because the browser came back with an unexpected state. Please run `kurze-url login` again.",
		},
		{
			name: "denied",
			err:  auth.ErrDenied,
			want: "The sign-in was cancelled or denied in the browser.",
		},
		{
			name: "timed out",
			err:  fmt.Errorf("%w: the browser did not come back within 5m0s", auth.ErrTimeout),
			want: "The sign-in did not finish in time. Please run `kurze-url login` again.",
		},
		{
			name: "a bad URL override",
			err:  &environment.InvalidURLError{Variable: "KURZE_URL_API_URL"},
			want: "KURZE_URL_API_URL must be a complete http:// or https:// address.",
		},
		{
			name: "no connection",
			err:  fmt.Errorf("send the request: %w", refused),
			want: "Could not reach 127.0.0.1:9. Check your internet connection and try again.",
		},
		{
			name: "a net error with an address",
			err:  &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 443}, Err: syscall.ECONNREFUSED},
			want: "Could not reach 10.1.2.3:443. Check your internet connection and try again.",
		},
		{
			name: "anything else is shown as it is",
			err:  errors.New("something odd happened"),
			want: "something odd happened",
		},
		{
			name: "a usage error keeps cobra's words",
			err:  &UsageError{Err: errors.New("unknown flag: --nope")},
			want: "unknown flag: --nope",
		},
	}
	cat := i18n.Load(i18n.EN)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, describe(tt.err, cat))
		})
	}
}

func TestDescribeDoesNotTreatAnUnnamedHostAsANetworkFailure(t *testing.T) {
	// context.DeadlineExceeded satisfies net.Error and names no host. It
	// would read "Could not reach ." in either language, so it stays an error
	// message.
	err := fmt.Errorf("the login was interrupted: %w", context.DeadlineExceeded)

	require.Equal(t, "the login was interrupted: context deadline exceeded", describe(err, i18n.Load(i18n.EN)))
}

func TestDescribeDoesNotShowAParseErrorAsANetworkFailure(t *testing.T) {
	err := &url.Error{Op: "parse", URL: "://nope", Err: errors.New("missing protocol scheme")}

	require.NotContains(t, describe(err, i18n.Load(i18n.EN)), "Could not reach")
}

func TestDescribeSpeaksGerman(t *testing.T) {
	cat := i18n.Load(i18n.DE)

	require.Equal(t, "Bitte erneut `kurze-url login` ausführen.", describe(auth.ErrLoginRequired, cat))
	require.Equal(t, "Nicht gefunden, oder du hast keinen Zugriff darauf.", describe(problem(http.StatusNotFound, "x"), cat))
	require.Equal(t,
		"Gerade zu viele Anfragen. Bitte warte eine Minute und versuche es erneut.",
		describe(problem(http.StatusTooManyRequests, "x"), cat))
	require.Equal(t,
		"Die beiden Farben liegen zu nah beieinander, eine Kamera kann den Code so nicht lesen.",
		describe(problem(http.StatusUnprocessableEntity, "x", apiclient.ProblemDetail{Location: "query.fg", Value: "low_contrast"}), cat))
	require.Equal(t,
		"Das Ziel muss eine vollständige https://-Adresse sein.",
		describe(problem(http.StatusUnprocessableEntity, "x", apiclient.ProblemDetail{Location: "body.destination_url"}), cat))
	require.Equal(t,
		"127.0.0.1:9 ist nicht erreichbar. Prüfe deine Internetverbindung und versuche es erneut.",
		describe(&url.Error{Op: "Get", URL: "http://127.0.0.1:9/", Err: syscall.ECONNREFUSED}, cat))
}

func TestUnknownProblemShowsTheAPIDetail(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.api.problem(http.MethodGet, apiclient.PathMe, http.StatusConflict, "the thing is in the wrong state")

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: the thing is in the wrong state\n", h.stderr.String())
}

func TestExpiredLoginSaysLoginAgain(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.auth.refuseRefreshes()
	h.api.problem(http.MethodGet, apiclient.PathMe, http.StatusUnauthorized, "the token expired")

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: Please run `kurze-url login` again.\n", h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Len(t, h.api.callsTo(http.MethodGet, apiclient.PathMe), 1, "a refresh that fails is not followed by a second request")
}

func TestATokenTheAPIRefusesTwiceSaysLoginAgain(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.api.problem(http.MethodGet, apiclient.PathMe, http.StatusUnauthorized, "the token expired")

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: Please run `kurze-url login` again.\n", h.stderr.String())
	require.Len(t, h.api.callsTo(http.MethodGet, apiclient.PathMe), 2, "one retry with the refreshed token, and no more")
}

func TestAnUnreachableAPIIsDescribedInTheUsersLanguage(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\n")
	h.api.Close()

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "ist nicht erreichbar. Prüfe deine Internetverbindung")
}

func TestABrokenConfigFileNamesTheFileAndTheLine(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\nlanguage: fr\n")

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: "+h.configPath+":2: “language” must be en or de, not “fr”.\n",
		h.stderr.String())
	require.Empty(t, h.api.requests())
}

func TestABadURLOverrideNamesTheVariableAndNeverTheValue(t *testing.T) {
	h := newHarness(t)
	h.env["KURZE_URL_API_URL"] = "ftp://ada:hunter2@example.org"

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: KURZE_URL_API_URL must be a complete http:// or https:// address.\n",
		h.stderr.String())
	require.NotContains(t, h.stderr.String(), "hunter2")
}

// configCodes is every code the config package can produce, with a key and a
// value as the package would give them. TestDescribeCoversEveryConfigCode
// walks it, so a code added to the package without a message here is
// found by a test and not by a person staring at a raw code.
var configCodes = []struct {
	code     config.Code
	key, val string
	english  string
}{
	{config.CodeSyntax, "", "", "The file is not valid YAML, or it holds more than one document."},
	{config.CodeNotAMapping, "teams", "", "“teams” must be a section of settings, not a single value or a list."},
	{config.CodeUnknownKey, "qr.colour", "", "Unknown setting “qr.colour”."},
	{config.CodeDuplicateKey, "qr.fg", "", "“qr.fg” appears twice."},
	{config.CodeInvalidLanguage, "language", "fr", "“language” must be en or de, not “fr”."},
	{config.CodeInvalidTeamSlug, "teams.AB", "AB", "“teams.AB”: “AB” is not a valid team slug (3 to 40 lower-case letters, digits or hyphens, not starting or ending with a hyphen)."},
	{config.CodeInvalidFormat, "qr.format", "gif", "“qr.format” must be svg or png, not “gif”."},
	{config.CodeInvalidSize, "qr.size", "10", "“qr.size” must be a whole number from 64 to 2048, not “10”."},
	{config.CodeInvalidColour, "qr.fg", "red", "“qr.fg” must be six hexadecimal digits such as 1a3a6e, not “red”."},
	{config.CodeInvalidHostname, "teams.sv-gruenwald.domain", "Links.Example", "“teams.sv-gruenwald.domain” must be a lower-case host name such as links.example.org, not “Links.Example”."},
	{config.CodeEmptyValue, "teams.sv-gruenwald.folder", "", "“teams.sv-gruenwald.folder” must not be empty."},
	{config.CodeKeyNeedsTeam, "folder", "", "“folder” belongs to one team. Add --team SLUG."},
	{config.CodeKeyIsGlobal, "language", "sv-gruenwald", "“language” applies to every team and cannot be set for one team."},
}

func TestDescribeCoversEveryConfigCode(t *testing.T) {
	for _, tt := range configCodes {
		t.Run(string(tt.code), func(t *testing.T) {
			err := &config.ValidationError{Path: "/c/config.yaml", Line: 7, Code: tt.code, Key: tt.key, Value: tt.val}

			got := describe(err, i18n.Load(i18n.EN))

			require.Equal(t, "/c/config.yaml:7: "+tt.english, got)
			require.NotContains(t, got, "{", "a placeholder was left unfilled")
			require.NotContains(t, got, "config.invalid", "the key itself must never be what is shown")

			german := describe(err, i18n.Load(i18n.DE))
			require.NotEqual(t, got, german, "the German text is its own")
			require.NotContains(t, german, "{")
			require.NotContains(t, german, "config.invalid")
		})
	}
}

func TestDescribeNamesTheFileWithoutALineWhenThereIsNone(t *testing.T) {
	// `config set` refuses a value before it reaches the file, and the
	// parser reports no line for an error on the first one.
	err := &config.ValidationError{Path: "/c/config.yaml", Code: config.CodeInvalidSize, Key: "qr.size", Value: "10"}

	got := describe(err, i18n.Load(i18n.EN))

	require.Equal(t, "/c/config.yaml: “qr.size” must be a whole number from 64 to 2048, not “10”.", got)
	require.NotContains(t, got, ":0")
}

func TestDescribeLeavesOutTheLocationWhenThereIsNoFile(t *testing.T) {
	// ParseKey sees no file.
	err := &config.ValidationError{Code: config.CodeUnknownKey, Key: "colour"}

	require.Equal(t, "Unknown setting “colour”.", describe(err, i18n.Load(i18n.EN)))
}

func TestDescribeWordsAWholeDocumentThatIsNotAMapping(t *testing.T) {
	err := &config.ValidationError{Path: "/c/config.yaml", Line: 1, Code: config.CodeNotAMapping}

	got := describe(err, i18n.Load(i18n.EN))

	require.Equal(t, "/c/config.yaml:1: The top level of the file must be a mapping of settings.", got)
}

func TestDescribeKeepsAnUnknownConfigCodeReadable(t *testing.T) {
	err := &config.ValidationError{Path: "/c/config.yaml", Line: 2, Code: "from_the_future"}

	require.Equal(t, "/c/config.yaml:2: from_the_future", describe(err, i18n.Load(i18n.EN)))
}

func TestDescribeFindsAConfigErrorUnderAUsageError(t *testing.T) {
	// `config set nope x` is a usage error whose cause is a config refusal;
	// the person reads it in their language all the same.
	cause := &config.ValidationError{Code: config.CodeUnknownKey, Key: "nope"}

	got := describe(&UsageError{Err: cause}, i18n.Load(i18n.DE))

	require.True(t, strings.HasPrefix(got, "Unbekannte Einstellung „nope“"), got)
}
