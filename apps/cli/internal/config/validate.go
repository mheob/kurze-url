package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Code names the rule a config file or a `config set` call broke. The package
// prints nothing: the command layer turns each Code into a catalog message,
// so the codes are the contract between the two and are never reworded.
type Code string

// The codes a ValidationError carries. The first eleven are reachable from a
// file on disk; the last two only from Set and Unset, which refuse a key in a
// scope it has no meaning in.
const (
	CodeSyntax          Code = "syntax"            // not parseable YAML, or more than one document
	CodeNotAMapping     Code = "not_a_mapping"     // a scalar, list or alias where a section belongs
	CodeUnknownKey      Code = "unknown_key"       // a name the file does not know in that place
	CodeDuplicateKey    Code = "duplicate_key"     // one name twice in the same mapping
	CodeInvalidLanguage Code = "invalid_language"  // not exactly en or de
	CodeInvalidTeamSlug Code = "invalid_team_slug" // not a slug of 3 to 40 characters
	CodeInvalidFormat   Code = "invalid_format"    // not svg or png
	CodeInvalidSize     Code = "invalid_size"      // not an integer from 64 to 2048
	CodeInvalidColour   Code = "invalid_colour"    // not six hexadecimal digits
	CodeInvalidHostname Code = "invalid_hostname"  // not lower-case letters, digits, dots and hyphens
	CodeEmptyValue      Code = "empty_value"       // a folder or domain with nothing in it
	CodeKeyNeedsTeam    Code = "key_needs_team"    // folder or domain set without a team
	CodeKeyIsGlobal     Code = "key_is_global"     // language or team set for one team
)

// ValidationError is every way the config can be refused, typed so the CLI
// can show its own message in the person's language instead of this
// package's text.
type ValidationError struct {
	// Path is the file the error is about, as it was given to Load, so the
	// command layer can print "<path>:<line>". Empty for ParseKey, which
	// sees no file.
	Path string
	// Line is the 1-based line in Path, or 0 when no line applies: a value
	// refused by Set never reached the file, and the parser reports no line
	// for a few syntax errors.
	Line int
	// Code is the rule that was broken.
	Code Code
	// Key is the dotted path of the offending entry as it stands in the
	// file, such as "qr.size" or "teams.sv-gruenwald.qr.fg". The team is
	// part of it, because the same key means different lines in different
	// sections. It is empty when the document itself is the problem.
	Key string
	// Value is the text that failed, empty when there is none.
	Value string
}

// Error is meant for logs: "path:line: code". The person sees the command
// layer's translation of Code, never this string.
func (e *ValidationError) Error() string {
	switch {
	case e.Path != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Code)
	case e.Path != "":
		return fmt.Sprintf("%s: %s", e.Path, e.Code)
	}
	return string(e.Code)
}

// The limits of the rules. The team slug rule is the API's (3 to 40
// characters, the pattern the teams table enforces), and the QR bounds are
// the ones GET /v1/links/{id}/qr accepts, so a value this package passes is
// one the API will not refuse for its range.
const (
	slugMinLen = 3
	slugMaxLen = 40
	sizeMin    = 64
	sizeMax    = 2048
)

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	colourPattern   = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)
	hostnamePattern = regexp.MustCompile(`^[a-z0-9.-]+$`)
)

// validSlug reports whether s is a team slug.
func validSlug(s string) bool {
	return len(s) >= slugMinLen && len(s) <= slugMaxLen && slugPattern.MatchString(s)
}

// parseSize reads a QR size. It accepts digits only, because "+128", "1e3"
// and "128.5" are not the integer the rule asks for, and checks the range.
func parseSize(text string) (int, bool) {
	if text == "" || text[0] < '0' || text[0] > '9' {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= sizeMin && n <= sizeMax
}

// check judges text as the value of k and returns the Code of the rule it
// breaks, or "" when it is acceptable. It sees the text exactly as it stands
// in the file, so a colour with a leading "#" is refused here; Set strips the
// hash before it asks.
func check(k Key, text string) Code {
	switch k {
	case Language:
		if text != "en" && text != "de" {
			return CodeInvalidLanguage
		}
	case Team:
		if !validSlug(text) {
			return CodeInvalidTeamSlug
		}
	case Folder:
		if strings.TrimSpace(text) == "" {
			return CodeEmptyValue
		}
	case Domain:
		if text == "" {
			return CodeEmptyValue
		}
		if !hostnamePattern.MatchString(text) {
			return CodeInvalidHostname
		}
	case QRFormat:
		if text != "svg" && text != "png" {
			return CodeInvalidFormat
		}
	case QRSize:
		if _, ok := parseSize(text); !ok {
			return CodeInvalidSize
		}
	case QRFG, QRBG:
		if !colourPattern.MatchString(text) {
			return CodeInvalidColour
		}
	}
	return ""
}

// scalarText is the text a value is judged on. The rules look at the text and
// not at the YAML tag, because YAML reads the colour 000000 as an integer and
// '1024' as a string, and a person who wrote either meant the same thing.
// Two shapes have no text: anything that is not a scalar, and a null (`~`,
// `null` or nothing at all), which is "no value" and not the word "null".
// A person who really wants the slug null quotes it.
func scalarText(n *yaml.Node) string {
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// qrKeys maps an entry of a `qr` section to its Key. The same four entries
// are valid under the top-level `qr` and under every team's.
var qrKeys = map[string]Key{
	"format": QRFormat,
	"size":   QRSize,
	"fg":     QRFG,
	"bg":     QRBG,
}

// walker validates a parsed document and reports the first problem in file
// order, so the line a person is sent to is the topmost one to fix.
type walker struct {
	path string
}

// fail builds the error for an entry, positioned at the node it is about.
func (w walker) fail(at *yaml.Node, code Code, key, value string) *ValidationError {
	return &ValidationError{Path: w.path, Line: at.Line, Code: code, Key: key, Value: value}
}

// join extends a dotted key path by one entry.
func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// mapping checks that n is a mapping with no repeated key and calls each for
// every entry in file order. A key that is not a plain scalar reaches each as
// the empty name, which no section accepts, so it is reported like any other
// name the file does not know. key is the dotted path of n for the errors.
func (w walker) mapping(n *yaml.Node, key string, each func(name string, k, v *yaml.Node) error) error {
	if n.Kind != yaml.MappingNode {
		return w.fail(n, CodeNotAMapping, key, "")
	}
	seen := make(map[string]bool, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		name := ""
		if k.Kind == yaml.ScalarNode {
			name = k.Value
			if seen[name] {
				return w.fail(k, CodeDuplicateKey, join(key, name), "")
			}
			seen[name] = true
		}
		if err := each(name, k, v); err != nil {
			return err
		}
	}
	return nil
}

// value checks one scalar entry against the rules of k.
func (w walker) value(k Key, key string, v *yaml.Node) error {
	text := scalarText(v)
	if code := check(k, text); code != "" {
		return w.fail(v, code, key, text)
	}
	return nil
}

// root checks the whole document: language, team, qr and teams, nothing else.
func (w walker) root(n *yaml.Node) error {
	return w.mapping(n, "", func(name string, k, v *yaml.Node) error {
		switch name {
		case "language":
			return w.value(Language, name, v)
		case "team":
			return w.value(Team, name, v)
		case "qr":
			return w.qr(name, v)
		case "teams":
			return w.teams(name, v)
		}
		return w.fail(k, CodeUnknownKey, join("", name), "")
	})
}

// qr checks a `qr` section, the global one or a team's.
func (w walker) qr(key string, n *yaml.Node) error {
	return w.mapping(n, key, func(name string, k, v *yaml.Node) error {
		if qrKey, ok := qrKeys[name]; ok {
			return w.value(qrKey, join(key, name), v)
		}
		return w.fail(k, CodeUnknownKey, join(key, name), "")
	})
}

// teams checks the `teams` section: every key is a team slug and every value
// a team section.
func (w walker) teams(key string, n *yaml.Node) error {
	return w.mapping(n, key, func(slug string, k, v *yaml.Node) error {
		if !validSlug(slug) {
			return w.fail(k, CodeInvalidTeamSlug, join(key, slug), slug)
		}
		return w.team(join(key, slug), v)
	})
}

// team checks one `teams.<slug>` section: folder, domain and qr.
func (w walker) team(key string, n *yaml.Node) error {
	return w.mapping(n, key, func(name string, k, v *yaml.Node) error {
		switch name {
		case "folder":
			return w.value(Folder, join(key, name), v)
		case "domain":
			return w.value(Domain, join(key, name), v)
		case "qr":
			return w.qr(join(key, name), v)
		}
		return w.fail(k, CodeUnknownKey, join(key, name), "")
	})
}
