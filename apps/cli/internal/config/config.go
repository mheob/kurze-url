// Package config loads, validates, resolves and edits the CLI's YAML config
// file.
//
// The file is held as a yaml.Node tree and never decoded into a struct. A
// struct round trip drops comments and reorders keys, and this file is one
// people write by hand, so `config set` has to edit the tree in place and
// write back what was there. The package prints nothing and knows nothing
// about languages: every refusal is a *ValidationError whose Code the command
// layer translates.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Where the file lives, unless KURZE_URL_CONFIG says otherwise.
const (
	pathEnv  = "KURZE_URL_CONFIG"
	dirName  = "kurze-url"
	fileName = "config.yaml"
)

// File modes. The directory is private because by default it also holds the
// credentials fallback file; the config itself carries no secret, so it stays
// readable for the editors and diff tools a person points at it.
const (
	dirMode  fs.FileMode = 0o700
	fileMode fs.FileMode = 0o644
)

// Source says which section of the file a resolved value came from.
type Source string

// The two places Resolve finds a value in. Built-in defaults are a third
// source, but they are the caller's business and not this package's.
const (
	SourceTeam   Source = "team"
	SourceGlobal Source = "global"
)

// File is a loaded config file. The zero value is not usable; get one from
// Load.
type File struct {
	path string
	// doc is always a document node whose only child is a mapping, also for
	// an empty or missing file, so no edit has to special-case "nothing yet".
	doc *yaml.Node
}

// Path returns where the config file lives: KURZE_URL_CONFIG when it is set
// and not empty, else kurze-url/config.yaml under the user's config
// directory. getenv is a parameter so a test supplies a map and never reads
// the process's environment.
func Path(getenv func(string) string) (string, error) {
	if p := getenv(pathEnv); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate the user config directory: %w", err)
	}
	return filepath.Join(dir, dirName, fileName), nil
}

// Load reads and validates the file at path. A file that does not exist is an
// empty File and not an error, because nobody has to create a config before
// the first `config set`; Save creates it. A file that exists but breaks a
// rule is refused whole with a *ValidationError, and every other failure
// (permissions, a directory in place of the file) is a plain wrapped error.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{path: path, doc: emptyDocument()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	doc, err := parse(path, data)
	if err != nil {
		return nil, err
	}
	if err := (walker{path: path}).root(doc.Content[0]); err != nil {
		return nil, err
	}
	return &File{path: path, doc: doc}, nil
}

// emptyDocument is a document holding an empty mapping.
func emptyDocument() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

// syntaxLine finds the line the YAML parser blames in its error message,
// "yaml: line 3: mapping values are not allowed in this context". The parser
// offers no typed position, so the message is the only place it is.
var syntaxLine = regexp.MustCompile(`^yaml: line (\d+):`)

// syntaxError turns a parser failure into the typed error. A message without
// a line number leaves Line at 0.
func syntaxError(path string, err error) *ValidationError {
	verr := &ValidationError{Path: path, Code: CodeSyntax}
	if m := syntaxLine.FindStringSubmatch(err.Error()); m != nil {
		verr.Line, _ = strconv.Atoi(m[1])
	}
	return verr
}

// parse reads data into a document node whose child is the root mapping, or
// refuses it with a *ValidationError. It does not judge the contents.
func parse(path string, data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	err := dec.Decode(&doc)
	if errors.Is(err, io.EOF) {
		// Nothing but whitespace and comments. The decoder reports no
		// document at all and drops the comments with it, so they are
		// carried as the head of an empty one: a person's note in an
		// otherwise empty file must outlive the first `config set`.
		empty := emptyDocument()
		if text := strings.TrimSpace(string(data)); strings.HasPrefix(text, "#") {
			empty.HeadComment = text
		}
		return empty, nil
	}
	if err != nil {
		return nil, syntaxError(path, err)
	}

	// Only the first document is read and Save would write back only that
	// one, so a second would be deleted by the next edit. Refuse it instead.
	var next yaml.Node
	switch err := dec.Decode(&next); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, syntaxError(path, err)
	default:
		return nil, &ValidationError{Path: path, Line: next.Line, Code: CodeSyntax}
	}

	// A bare `---` or `~` is a null document: no content, the same as an
	// empty file.
	if root := doc.Content[0]; root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	return &doc, nil
}

// root is the document's top-level mapping.
func (f *File) root() *yaml.Node {
	return f.doc.Content[0]
}

// mapValue returns the value stored under name in mapping m, or nil.
func mapValue(m *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			return m.Content[i+1]
		}
	}
	return nil
}

// lookup follows segs down from the root and returns the node at the end, or
// nil when any step is missing. Every step before the last is a mapping,
// because Load refused a file where one is not and Set only ever adds
// mappings there.
func (f *File) lookup(segs []string) *yaml.Node {
	n := f.root()
	for _, seg := range segs {
		if n.Kind != yaml.MappingNode {
			return nil
		}
		if n = mapValue(n, seg); n == nil {
			return nil
		}
	}
	return n
}

// Get returns the raw value of k in one scope: the top of the file when team
// is empty, that team's own section otherwise. It never falls back to the
// other scope, which is what Resolve is for, so `config get --team x` can
// say what the team section itself holds. A key that has no meaning in that
// scope is simply not found.
func (f *File) Get(k Key, team string) (string, bool) {
	segs, ok := segments(k, team)
	if !ok {
		return "", false
	}
	n := f.lookup(segs)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// Resolve finds the value k has for team: the team's own section first, then
// the global one for the keys that have a global scope. For a key that is
// global only, such as language, the team makes no difference. When the file
// has nothing, ok is false and the built-in default is the caller's to apply.
// An empty team resolves globally.
func (f *File) Resolve(k Key, team string) (value string, source Source, ok bool) {
	if team != "" {
		if v, found := f.Get(k, team); found {
			return v, SourceTeam, true
		}
	}
	if v, found := f.Get(k, ""); found {
		return v, SourceGlobal, true
	}
	return "", "", false
}

// TeamSections lists the slugs that have a section under `teams`, in the
// order they appear in the file. The command layer compares them with the
// person's memberships, because a section for a team they are no longer in is
// ignored with a warning and not refused.
func (f *File) TeamSections() []string {
	teams := mapValue(f.root(), "teams")
	if teams == nil {
		return nil
	}
	var slugs []string
	for i := 0; i+1 < len(teams.Content); i += 2 {
		slugs = append(slugs, teams.Content[i].Value)
	}
	return slugs
}

// Save writes the file back, creating its directory (mode 0700) when it is
// missing. The bytes go to a temporary file in the same directory and are
// renamed over the target, so a crash or a full disk leaves the old file
// whole instead of half a new one; the rename is atomic only within one
// filesystem, which is why the temporary file is a sibling and not in /tmp.
// The result has mode 0644.
func (f *File) Save() error {
	data, err := f.encode()
	if err != nil {
		return err
	}

	// A config file that is a symlink is a dotfile manager's doing. Renaming
	// over the link would replace it with a regular file and cut the file
	// from the repository it is managed in, so the write goes to its target.
	target := f.path
	if resolved, err := filepath.EvalSymlinks(f.path); err == nil {
		target = resolved
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	// Remove is a no-op after the rename and the cleanup for every early
	// return before it.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	// CreateTemp makes the file 0600, whatever the umask says.
	if err := os.Chmod(tmp.Name(), fileMode); err != nil {
		return fmt.Errorf("set config file mode: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// encode renders the document the way Save writes it: two-space indents.
func (f *File) encode() ([]byte, error) {
	if len(f.root().Content) == 0 {
		// The encoder writes an empty mapping as "{}", which is not an empty
		// config in anyone's eyes. Only the document's own comments are left
		// to write.
		return commentsOnly(f.doc), nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f.doc); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// commentsOnly renders a document that has no entries left: its head and foot
// comments, or nothing at all. Unset hands the comments of the last entry it
// removes to the document, so what the person wrote around it is here too.
func commentsOnly(doc *yaml.Node) []byte {
	var parts []string
	for _, c := range []string{doc.HeadComment, doc.FootComment} {
		if c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []byte(strings.Join(parts, "\n\n") + "\n")
}
