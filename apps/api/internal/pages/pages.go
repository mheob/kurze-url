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
	// KindFlagged means Google Safe Browsing reports the link's destination.
	// Only RenderFlagged, given the threat types of a confirmation younger
	// than thirty minutes, shows Google's warning; RenderError has no such
	// confirmation and renders KindFlagged as KindUnavailable.
	KindFlagged Kind = "flagged"
	// KindRateLimited means the caller has exceeded the redirect rate limit.
	KindRateLimited Kind = "rate_limited"
	// KindServerError means the redirect could not be resolved due to an internal error.
	KindServerError Kind = "server_error"
	// KindUnavailable means a flagged link could not be re-confirmed just
	// now. It says nothing about the destination: without a confirmation from
	// Google younger than thirty minutes, the terms forbid calling it unsafe.
	KindUnavailable Kind = "unavailable"
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
	flagged       flaggedStrings
}

// threatCategory groups Safe Browsing's threat types by what the block page
// says about them.
type threatCategory string

const (
	categoryPhishing threatCategory = "phishing"
	categoryHarmful  threatCategory = "harmful"
	categoryUnknown  threatCategory = "unknown"
)

// knownThreats are the threat types this page explains, in the order it
// explains them, each with the definition Google's Safe Browsing usage page
// (reference/Appropriate.Usage, "User Warnings") names for it. That page
// defines malware and unwanted software on one page, so both link there. A
// type Google adds later is shown under the generic text rather than dropped,
// because dropping it would leave a block page that names no reason.
var knownThreats = []struct {
	threatType string
	category   threatCategory
	definition string
}{
	{"SOCIAL_ENGINEERING", categoryPhishing, "https://developers.google.com/search/docs/monitor-debug/security/social-engineering"},
	{"MALWARE", categoryHarmful, "https://developers.google.com/search/docs/monitor-debug/security/malware"},
	{"UNWANTED_SOFTWARE", categoryHarmful, "https://developers.google.com/search/docs/monitor-debug/security/malware"},
	{"POTENTIALLY_HARMFUL_APPLICATION", categoryHarmful, "https://developers.google.com/android/play-protect/potentially-harmful-applications"},
}

const (
	// advisoryURL is Google's Safe Browsing advisory, which the attribution
	// line must link to.
	advisoryURL = "https://developers.google.com/safe-browsing/v4/advisory"
	// safeBrowsingURL is the definition offered for a threat type this page
	// does not know.
	safeBrowsingURL = "https://safebrowsing.google.com/"
)

// flaggedStrings is the block page's copy. Every claim is qualified
// ("suspected", "possibly", "may"), every threat links Google's definition of
// it, Google is credited, and the page says Google cannot promise to be right.
// All four are conditions of Google's Safe Browsing terms, not style, and the
// attribution and the disclaimer are Google's own wording in both languages,
// taken from the English and German versions of its usage page.
type flaggedStrings struct {
	lead       string
	headings   map[threatCategory]string
	bodies     map[threatCategory]string
	advisory   string
	disclaimer string
	// definitions labels each threat type's definition link; the empty key
	// labels the generic one.
	definitions map[string]string
}

var localeCopy = map[Locale]localeStrings{
	LocaleEN: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link not found", "Link not found", "This short link does not exist, or it has been removed."},
			KindDisabled:    {"Link disabled", "Link disabled", "The owner of this short link has turned it off."},
			KindExpired:     {"Link expired", "Link expired", "This short link has passed its expiry date."},
			KindRateLimited: {"Too many requests", "Too many requests", "You have opened links too quickly. Please wait a moment and try again."},
			KindServerError: {"Something went wrong", "Something went wrong", "This link could not be resolved right now. Please try again shortly."},
			KindUnavailable: {"Link temporarily unavailable", "Link temporarily unavailable", "This short link cannot be opened right now. Please try again in a few minutes."},
		},
		passwordTitle: "Password required",
		passwordHead:  "Password required",
		passwordBody:  "This short link is protected. Enter its password to continue.",
		passwordLabel: "Password",
		submitLabel:   "Continue",
		wrongPassword: "That password is incorrect.",
		flagged: flaggedStrings{
			lead: "This short link is not being forwarded.",
			headings: map[threatCategory]string{
				categoryPhishing: "Suspected phishing site",
				categoryHarmful:  "Possibly harmful software",
				categoryUnknown:  "Suspected unsafe site",
			},
			bodies: map[threatCategory]string{
				categoryPhishing: "Google Safe Browsing reports that its destination may be a phishing site: a page that tries to trick visitors into revealing passwords, payment details or other personal information.",
				categoryHarmful:  "Google Safe Browsing reports that its destination may distribute possibly harmful software, which could damage your device or act against your interests.",
				categoryUnknown:  "Google Safe Browsing reports that its destination may be unsafe.",
			},
			definitions: map[string]string{
				"SOCIAL_ENGINEERING":              "What Google means by phishing",
				"MALWARE":                         "What Google means by malware",
				"UNWANTED_SOFTWARE":               "What Google means by unwanted software",
				"POTENTIALLY_HARMFUL_APPLICATION": "What Google means by potentially harmful apps",
				"":                                "About Google Safe Browsing",
			},
			advisory:   "Advisory provided by Google",
			disclaimer: "Google works to provide the most accurate and up-to-date information about unsafe web resources. However, Google cannot guarantee that its information is comprehensive and error-free: some risky sites may not be identified, and some safe sites may be identified in error.",
		},
	},
	LocaleDE: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link nicht gefunden", "Link nicht gefunden", "Dieser Kurzlink existiert nicht oder wurde entfernt."},
			KindDisabled:    {"Link deaktiviert", "Link deaktiviert", "Die Inhaberin oder der Inhaber dieses Kurzlinks hat ihn deaktiviert."},
			KindExpired:     {"Link abgelaufen", "Link abgelaufen", "Dieser Kurzlink hat sein Ablaufdatum überschritten."},
			KindRateLimited: {"Zu viele Anfragen", "Zu viele Anfragen", "Sie haben zu schnell zu viele Links geöffnet. Bitte warten Sie einen Moment."},
			KindServerError: {"Etwas ist schiefgelaufen", "Etwas ist schiefgelaufen", "Dieser Link konnte gerade nicht aufgelöst werden. Bitte versuchen Sie es gleich erneut."},
			KindUnavailable: {"Link vorübergehend nicht verfügbar", "Link vorübergehend nicht verfügbar", "Dieser Kurzlink kann gerade nicht geöffnet werden. Bitte versuchen Sie es in einigen Minuten erneut."},
		},
		passwordTitle: "Passwort erforderlich",
		passwordHead:  "Passwort erforderlich",
		passwordBody:  "Dieser Kurzlink ist geschützt. Geben Sie das Passwort ein, um fortzufahren.",
		passwordLabel: "Passwort",
		submitLabel:   "Weiter",
		wrongPassword: "Das Passwort ist nicht korrekt.",
		flagged: flaggedStrings{
			lead: "Dieser Kurzlink wird nicht weitergeleitet.",
			headings: map[threatCategory]string{
				categoryPhishing: "Mutmaßliche Phishing-Seite",
				categoryHarmful:  "Möglicherweise schädliche Software",
				categoryUnknown:  "Mutmaßlich unsichere Seite",
			},
			bodies: map[threatCategory]string{
				categoryPhishing: "Laut Google Safe Browsing ist das Ziel möglicherweise eine Phishing-Seite: eine Seite, die versucht, Besucherinnen und Besucher zur Preisgabe von Passwörtern, Zahlungsdaten oder anderen persönlichen Daten zu verleiten.",
				categoryHarmful:  "Laut Google Safe Browsing wird über das Ziel möglicherweise schädliche Software verbreitet, die Ihrem Gerät schaden oder gegen Ihre Interessen handeln könnte.",
				categoryUnknown:  "Laut Google Safe Browsing ist das Ziel möglicherweise unsicher.",
			},
			definitions: map[string]string{
				"SOCIAL_ENGINEERING":              "Was Google unter Phishing versteht",
				"MALWARE":                         "Was Google unter Malware versteht",
				"UNWANTED_SOFTWARE":               "Was Google unter unerwünschter Software versteht",
				"POTENTIALLY_HARMFUL_APPLICATION": "Was Google unter potenziell schädlichen Apps versteht",
				"":                                "Über Google Safe Browsing",
			},
			advisory:   "Von Google bereitgestellte Hinweise",
			disclaimer: "Google arbeitet daran, möglichst präzise und aktuelle Informationen zu unsicheren Webressourcen bereitzustellen. Google kann jedoch nicht garantieren, dass die Informationen umfassend und fehlerfrei sind: Einige riskante Websites werden möglicherweise nicht und einige sichere Websites irrtümlich identifiziert.",
		},
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
//
// KindFlagged renders the neutral KindUnavailable copy. Google's warning may
// be shown only on a confirmation younger than thirty minutes, and this
// function takes no threat types, so it cannot know there is one; calling the
// destination unsafe without it is what Google's terms forbid. RenderFlagged
// is the one way to the block page.
func RenderError(w http.ResponseWriter, status int, loc Locale, kind Kind) {
	if kind == KindFlagged {
		kind = KindUnavailable
	}

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

type definitionLink struct {
	Label string
	URL   string
}

type threatSection struct {
	category    threatCategory
	Body        string
	Definitions []definitionLink
}

type flaggedView struct {
	Lang        Locale
	Title       string
	Heading     string
	Lead        string
	Threats     []threatSection
	Advisory    string
	AdvisoryURL string
	Disclaimer  string
}

// RenderFlagged writes the block page for a link whose destination Google
// Safe Browsing reports, naming threatTypes. Always a 403. The redirect path
// calls it only with a confirmation from Google younger than thirty minutes.
func RenderFlagged(w http.ResponseWriter, loc Locale, threatTypes []string) {
	render(w, http.StatusForbidden, "flagged.html", flaggedPage(loc, threatTypes))
}

// flaggedPage builds one section per category reported — phishing, then
// harmful software, then anything this page does not know — with a
// definition link for each reported type. One category gets its own heading;
// a mix, or nothing known, gets the generic one.
func flaggedPage(loc Locale, threatTypes []string) flaggedView {
	locale, ok := localeCopy[loc]
	if !ok {
		loc, locale = LocaleEN, localeCopy[LocaleEN]
	}
	text := locale.flagged

	reported := make(map[string]bool, len(threatTypes))
	for _, threatType := range threatTypes {
		reported[threatType] = true
	}
	known := make(map[string]bool, len(knownThreats))
	for _, threat := range knownThreats {
		known[threat.threatType] = true
	}
	unknown := len(threatTypes) == 0
	for _, threatType := range threatTypes {
		if !known[threatType] {
			unknown = true
		}
	}

	var sections []threatSection
	for _, category := range []threatCategory{categoryPhishing, categoryHarmful} {
		var links []definitionLink
		for _, threat := range knownThreats {
			if threat.category == category && reported[threat.threatType] {
				links = append(links, definitionLink{Label: text.definitions[threat.threatType], URL: threat.definition})
			}
		}
		if len(links) > 0 {
			sections = append(sections, threatSection{category: category, Body: text.bodies[category], Definitions: links})
		}
	}
	if unknown {
		sections = append(sections, threatSection{
			category:    categoryUnknown,
			Body:        text.bodies[categoryUnknown],
			Definitions: []definitionLink{{Label: text.definitions[""], URL: safeBrowsingURL}},
		})
	}

	heading := text.headings[categoryUnknown]
	if len(sections) == 1 {
		heading = text.headings[sections[0].category]
	}

	return flaggedView{
		Lang:        loc,
		Title:       heading,
		Heading:     heading,
		Lead:        text.lead,
		Threats:     sections,
		Advisory:    text.advisory,
		AdvisoryURL: advisoryURL,
		Disclaimer:  text.disclaimer,
	}
}
