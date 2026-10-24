package auth

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"

	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// pageSource is the one page the loopback listener serves. It is a template
// file and not a Go string so that it reads, and gets formatted, as HTML. It
// is self-contained on purpose: no script, no font, no image and no link, so
// nothing leaves the machine while the person looks at it, and its inline
// style needs nothing but the style-src 'unsafe-inline' the listener's
// Content-Security-Policy grants.
//
//go:embed page.html.tmpl
var pageSource string

// pageTemplate is parsed once. html/template escapes the catalog text for
// the context it lands in, which keeps a translation containing "<" from
// becoming markup.
var pageTemplate = template.Must(template.New("page").Parse(pageSource))

// pageData is what the template shows.
type pageData struct {
	Lang  string
	Title string
	Body  string
}

// renderPage renders the page the browser lands on, in the catalog's
// language: the confirmation when success is true, and otherwise the one that
// sends the person back to the terminal. The failure page is the same for a
// denial, a state mismatch and a failed exchange, because the terminal is
// where each of them is explained.
//
// Every key is a literal because the catalog test reads them out of the
// source.
func renderPage(cat *i18n.Catalog, success bool) ([]byte, error) {
	data := pageData{Lang: string(cat.Lang())}
	if success {
		data.Title = cat.T("login.page.successTitle")
		data.Body = cat.T("login.page.successBody")
	} else {
		data.Title = cat.T("login.page.failureTitle")
		data.Body = cat.T("login.page.failureBody")
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render the callback page: %w", err)
	}
	return buf.Bytes(), nil
}
