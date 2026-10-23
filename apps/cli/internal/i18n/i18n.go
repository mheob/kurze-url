// Package i18n holds the CLI's English and German texts. Every string a
// command prints comes from here, because golden rule 6 (no hardcoded
// user-facing string, not even temporarily) covers the CLI as it covers the
// web app. Command names, flags and --help text are the one exception and
// stay English.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
)

// Lang is a language the CLI speaks.
type Lang string

// The supported languages. English is the default whenever nothing says
// otherwise.
const (
	EN Lang = "en"
	DE Lang = "de"
)

// Args fills a message's {name} placeholders.
type Args map[string]string

// locales holds one flat JSON object per language: dotted key to text, with
// placeholders written {name}. They are embedded so the binary is one file
// and a catalog cannot go missing at run time.
//
//go:embed locales/*.json
var locales embed.FS

// placeholderPattern matches a {name} placeholder. T substitutes by it and
// the parity test compares by it, so the two cannot disagree about what a
// placeholder is.
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

// Catalog is one language's texts.
type Catalog struct {
	lang     Lang
	messages map[string]string
}

// newCatalog builds a catalog from messages directly. The tests use it to
// exercise T without putting a throwaway key into the shipped catalogs.
func newCatalog(lang Lang, messages map[string]string) *Catalog {
	return &Catalog{lang: lang, messages: messages}
}

// loadMessages reads and decodes one language's embedded catalog. It is
// split from Load so the tests can judge the shipped files through the
// code path production uses, and see an error where Load can only panic.
func loadMessages(l Lang) (map[string]string, error) {
	path := "locales/" + string(l) + ".json"
	data, err := locales.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return messages, nil
}

// Load returns the catalog for l. A language the CLI does not speak gets the
// English one, so the result always answers T.
//
// The catalogs are compiled into the binary, so a failure to read one is a
// defect of this build and not something a user can cause or recover from:
// Load panics, and the package's own tests, which decode both files, keep a
// release from ever getting there.
func Load(l Lang) *Catalog {
	if l != EN && l != DE {
		l = EN
	}
	messages, err := loadMessages(l)
	if err != nil {
		panic(fmt.Sprintf("i18n: embedded catalog is unusable: %v", err))
	}
	return newCatalog(l, messages)
}

// Lang reports which language the catalog is in.
func (c *Catalog) Lang() Lang {
	return c.lang
}

// T returns the text for key with its {name} placeholders replaced from
// args. Several Args are merged, a later one winning, and a placeholder with
// no value is left in the text as it is written. A key the catalog lacks
// comes back as itself, which reads as a defect on screen and never as a
// crash.
//
// key must be a string literal at every call site. TestEveryUsedKeyIsDefined
// checks each one against en.json, which is what makes the fallback above
// unreachable; a key built at run time would escape that check.
//
// Substitution runs once over the text, so a value that itself contains
// {other} is not expanded again.
func (c *Catalog) T(key string, args ...Args) string {
	text, ok := c.messages[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return text
	}
	values := make(Args)
	for _, a := range args {
		for name, value := range a {
			values[name] = value
		}
	}
	return placeholderPattern.ReplaceAllStringFunc(text, func(token string) string {
		if value, ok := values[token[1:len(token)-1]]; ok {
			return value
		}
		return token
	})
}
