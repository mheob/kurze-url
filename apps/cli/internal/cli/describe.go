package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/environment"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// describe words an error for the person, in the catalog's language. It is
// what Run prints after "kurze-url: ".
//
// It branches on the error's type and on the tokens the API attaches to a
// validation failure, never on the wording of any message, which the API and
// the standard library are free to change. The cases are the ones the spec
// lists as known; everything else is shown as it is, because an English
// message that says what happened beats a translated guess.
//
// The 409 that means "that slug is taken" is not here: only `link create`
// knows a 409 means that, and it words it itself.
func describe(err error, c *i18n.Catalog) string {
	var (
		apiErr  *apiclient.ProblemError
		invalid *config.ValidationError
		badURL  *environment.InvalidURLError
	)
	switch {
	// First, because the sentinel is wrapped by the token endpoint's refusal
	// and by the API client's "refresh the access token", and nothing else in
	// the chain is worth showing.
	case errors.Is(err, auth.ErrLoginRequired):
		return c.T("errors.loginAgain")
	case errors.As(err, &apiErr):
		return describeProblem(apiErr, c)
	case errors.Is(err, auth.ErrPortsBusy):
		return c.T("errors.loginPortsBusy")
	case errors.Is(err, auth.ErrStateMismatch):
		return c.T("errors.loginStateMismatch")
	case errors.Is(err, auth.ErrDenied):
		return c.T("errors.loginDenied")
	case errors.Is(err, auth.ErrTimeout):
		return c.T("errors.loginTimeout")
	case errors.As(err, &invalid):
		return describeConfig(invalid, c)
	case errors.As(err, &badURL):
		return c.T("errors.invalidEnvURL", i18n.Args{"variable": badURL.Variable})
	}
	if host := unreachableHost(err); host != "" {
		return c.T("errors.network", i18n.Args{"host": host})
	}
	return err.Error()
}

// describeProblem words an answer of the API. A 401 is "log in again" whoever
// sent it: the API client has already refreshed the token and tried once more
// by the time one arrives here, and a Vercel deployment-protection page,
// which also answers 401, is a login problem from the person's chair too.
// A 403 and a 404 read the same on purpose, because the API answers 404 for
// an entity the caller may not know exists.
func describeProblem(p *apiclient.ProblemError, c *i18n.Catalog) string {
	switch p.Status {
	case http.StatusUnauthorized:
		return c.T("errors.loginAgain")
	case http.StatusForbidden, http.StatusNotFound:
		return c.T("errors.notFound")
	case http.StatusTooManyRequests:
		return c.T("errors.rateLimited")
	case http.StatusUnprocessableEntity:
		switch {
		case p.HasDetail("query.fg", "low_contrast"):
			return c.T("errors.qrLowContrast")
		case p.HasDetail("query.fg", "invalid_color"), p.HasDetail("query.bg", "invalid_color"):
			return c.T("errors.qrInvalidColour")
		case p.HasDetail("body.destination_url", ""):
			return c.T("errors.invalidDestination")
		}
	}
	// Any other answer: what the API says, in the words it chose.
	switch {
	case p.Detail != "":
		return p.Detail
	case p.Title != "":
		return p.Title
	case http.StatusText(p.Status) != "":
		return http.StatusText(p.Status)
	}
	return p.Error()
}

// describeConfig words a config refusal. The location comes first, the way a
// compiler prints it, as "file:line: " or, when there is no line, "file: ".
// The parser reports no line for an error on the file's first line and `config
// set` refuses a value before it reaches the file, so a line of 0 is common
// and must not print as ":0". ParseKey sees no file, and then there is no
// location at all.
//
// The code picks the message. Each case calls T with its own literal key,
// which is what TestEveryUsedKeyIsDefined needs to see.
func describeConfig(e *config.ValidationError, c *i18n.Catalog) string {
	args := i18n.Args{
		"file":  e.Path,
		"line":  strconv.Itoa(e.Line),
		"key":   e.Key,
		"value": e.Value,
	}
	var message string
	switch e.Code {
	case config.CodeSyntax:
		message = c.T("config.invalid.syntax", args)
	case config.CodeNotAMapping:
		// A document that is not a mapping at all has no key to name.
		if e.Key == "" {
			message = c.T("config.invalid.not_a_mapping_top", args)
		} else {
			message = c.T("config.invalid.not_a_mapping", args)
		}
	case config.CodeUnknownKey:
		message = c.T("config.invalid.unknown_key", args)
	case config.CodeDuplicateKey:
		message = c.T("config.invalid.duplicate_key", args)
	case config.CodeInvalidLanguage:
		message = c.T("config.invalid.invalid_language", args)
	case config.CodeInvalidTeamSlug:
		message = c.T("config.invalid.invalid_team_slug", args)
	case config.CodeInvalidFormat:
		message = c.T("config.invalid.invalid_format", args)
	case config.CodeInvalidSize:
		message = c.T("config.invalid.invalid_size", args)
	case config.CodeInvalidColour:
		message = c.T("config.invalid.invalid_colour", args)
	case config.CodeInvalidHostname:
		message = c.T("config.invalid.invalid_hostname", args)
	case config.CodeEmptyValue:
		message = c.T("config.invalid.empty_value", args)
	case config.CodeKeyNeedsTeam:
		message = c.T("config.invalid.key_needs_team", args)
	case config.CodeKeyIsGlobal:
		message = c.T("config.invalid.key_is_global", args)
	default:
		// A code the config package gained after this switch was written.
		// Its own log form, "file:line: code", is better than nothing.
		return e.Error()
	}

	switch {
	case e.Path != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, message)
	case e.Path != "":
		return fmt.Sprintf("%s: %s", e.Path, message)
	}
	return message
}

// unreachableHost returns the host a failed request was for, or "" when the
// error is not a failed request or names no host.
//
// A request the HTTP client could not complete is a *url.Error naming the URL
// it was sent to, which holds no secret: tokens travel in headers and the
// client strips a password from the URL. The "parse" operation is the URL
// failing to parse, which is a mistake and not a network. A bare net.Error is
// accepted when it knows an address. One that does not, such as a
// context.DeadlineExceeded (which satisfies the interface), is left to
// print as it is, because "Could not reach ." helps nobody.
func unreachableHost(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Op == "parse" {
			return ""
		}
		if u, perr := url.Parse(urlErr.URL); perr == nil {
			return u.Host
		}
		return ""
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		var op *net.OpError
		if errors.As(err, &op) && op.Addr != nil {
			return op.Addr.String()
		}
	}
	return ""
}
