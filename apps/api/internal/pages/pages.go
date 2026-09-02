// Package pages renders the redirect surface's browser-facing HTML. These
// pages sit on the redirect hot path, so they are plain html/template with no
// framework, no build step and no client-side JavaScript.
//
// Every string here exists in both English and German. Nothing user-facing is
// hardcoded in a single language, including on this surface, which never
// passes through the React app's i18n layer.
package pages

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Locale is one of the two languages the redirect surface ships in.
type Locale string

const (
	// LocaleEN is English, the default when no locale can be negotiated.
	LocaleEN Locale = "en"
	// LocaleDE is German, the second language this surface ships in.
	LocaleDE Locale = "de"
)

// Kind identifies which error page to show.
type Kind string

const (
	// KindNotFound means no link resolves the requested slug.
	KindNotFound Kind = "not_found"
	// KindDisabled means the link's owner has turned it off.
	KindDisabled Kind = "disabled"
	// KindExpired means the link has passed its configured expiry date.
	KindExpired Kind = "expired"
	// KindFlagged means the link was flagged as unsafe by Safe Browsing scanning.
	KindFlagged Kind = "flagged"
	// KindRateLimited means the caller has exceeded the redirect rate limit.
	KindRateLimited Kind = "rate_limited"
	// KindServerError means the redirect could not be resolved due to an internal error.
	KindServerError Kind = "server_error"
)

type copyText struct {
	title   string
	heading string
	body    string
}

type localeStrings struct {
	errors        map[Kind]copyText
	passwordTitle string
	passwordHead  string
	passwordBody  string
	passwordLabel string
	submitLabel   string
	wrongPassword string
}

var localeCopy = map[Locale]localeStrings{
	LocaleEN: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link not found", "Link not found", "This short link does not exist, or it has been removed."},
			KindDisabled:    {"Link disabled", "Link disabled", "The owner of this short link has turned it off."},
			KindExpired:     {"Link expired", "Link expired", "This short link has passed its expiry date."},
			KindFlagged:     {"Link blocked", "Link blocked", "This short link was flagged as unsafe and is no longer forwarded."},
			KindRateLimited: {"Too many requests", "Too many requests", "You have opened links too quickly. Please wait a moment and try again."},
			KindServerError: {"Something went wrong", "Something went wrong", "This link could not be resolved right now. Please try again shortly."},
		},
		passwordTitle: "Password required",
		passwordHead:  "Password required",
		passwordBody:  "This short link is protected. Enter its password to continue.",
		passwordLabel: "Password",
		submitLabel:   "Continue",
		wrongPassword: "That password is incorrect.",
	},
	LocaleDE: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link nicht gefunden", "Link nicht gefunden", "Dieser Kurzlink existiert nicht oder wurde entfernt."},
			KindDisabled:    {"Link deaktiviert", "Link deaktiviert", "Die Inhaberin oder der Inhaber dieses Kurzlinks hat ihn deaktiviert."},
			KindExpired:     {"Link abgelaufen", "Link abgelaufen", "Dieser Kurzlink hat sein Ablaufdatum überschritten."},
			KindFlagged:     {"Link gesperrt", "Link gesperrt", "Dieser Kurzlink wurde als unsicher eingestuft und wird nicht mehr weitergeleitet."},
			KindRateLimited: {"Zu viele Anfragen", "Zu viele Anfragen", "Sie haben zu schnell zu viele Links geöffnet. Bitte warten Sie einen Moment."},
			KindServerError: {"Etwas ist schiefgelaufen", "Etwas ist schiefgelaufen", "Dieser Link konnte gerade nicht aufgelöst werden. Bitte versuchen Sie es gleich erneut."},
		},
		passwordTitle: "Passwort erforderlich",
		passwordHead:  "Passwort erforderlich",
		passwordBody:  "Dieser Kurzlink ist geschützt. Geben Sie das Passwort ein, um fortzufahren.",
		passwordLabel: "Passwort",
		submitLabel:   "Weiter",
		wrongPassword: "Das Passwort ist nicht korrekt.",
	},
}

// Negotiate picks a locale from an Accept-Language header, honouring q-values.
// Anything that is not a German preference falls back to English, the default.
func Negotiate(acceptLanguage string) Locale {
	best := LocaleEN
	bestQuality := -1.0

	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}

		quality := 1.0
		if _, raw, ok := strings.Cut(params, "q="); ok {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
				quality = parsed
			}
		}

		var locale Locale
		switch {
		case strings.HasPrefix(tag, "de"):
			locale = LocaleDE
		case strings.HasPrefix(tag, "en"):
			locale = LocaleEN
		default:
			continue
		}

		if quality > bestQuality {
			best, bestQuality = locale, quality
		}
	}

	return best
}

type errorView struct {
	Lang    Locale
	Title   string
	Heading string
	Body    string
}

// RenderError writes a localised error page with the given HTTP status.
func RenderError(w http.ResponseWriter, status int, loc Locale, kind Kind) {
	text, ok := localeCopy[loc].errors[kind]
	if !ok {
		text = localeCopy[LocaleEN].errors[KindServerError]
	}

	render(w, status, "error.html", errorView{
		Lang:    loc,
		Title:   text.title,
		Heading: text.heading,
		Body:    text.body,
	})
}

type passwordView struct {
	Lang          Locale
	Title         string
	Heading       string
	Body          string
	Action        string
	PasswordLabel string
	SubmitLabel   string
	WrongPassword bool
	ErrorMessage  string
}

// RenderPasswordPrompt writes the password interstitial. action is the path
// the form posts to; html/template escapes it as an attribute value.
func RenderPasswordPrompt(w http.ResponseWriter, status int, loc Locale, action string, wrongPassword bool) {
	s := localeCopy[loc]

	render(w, status, "password.html", passwordView{
		Lang:          loc,
		Title:         s.passwordTitle,
		Heading:       s.passwordHead,
		Body:          s.passwordBody,
		Action:        action,
		PasswordLabel: s.passwordLabel,
		SubmitLabel:   s.submitLabel,
		WrongPassword: wrongPassword,
		ErrorMessage:  s.wrongPassword,
	})
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// These pages are per-request and must never be cached by an intermediary.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		// The status is already written, so there is nothing to do but record it.
		slog.Error("rendering redirect-surface page failed", "template", name, "error", err)
	}
}
