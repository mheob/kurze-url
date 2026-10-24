package config

import (
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// refuse builds the error for a call Set or Unset turns down before it looks
// at the file. There is no line: the value never reached the file.
func (f *File) refuse(code Code, key, value string) *ValidationError {
	return &ValidationError{Path: f.path, Code: code, Key: key, Value: value}
}

// checkScope refuses a key in a scope it has no meaning in: folder and domain
// belong to a team and need one, language and team are global and take none.
// A team that is not a slug is refused as well, because no such section can
// exist in a file that loaded.
func (f *File) checkScope(k Key, team string) error {
	if !slices.Contains(allKeys, k) {
		return f.refuse(CodeUnknownKey, string(k), "")
	}
	if team == "" {
		if !k.Global() {
			return f.refuse(CodeKeyNeedsTeam, string(k), "")
		}
		return nil
	}
	if !k.PerTeam() {
		return f.refuse(CodeKeyIsGlobal, string(k), team)
	}
	if !validSlug(team) {
		return f.refuse(CodeInvalidTeamSlug, join("teams", team), team)
	}
	return nil
}

// normalise puts a value the way it is stored. A colour may be typed with the
// leading "#" of a CSS colour, which is also what people paste, and in any
// case; it is stored as six lower-case digits. Load does none of this: a "#"
// in a file is a mistake worth reporting, since the API takes bare digits.
func normalise(k Key, value string) string {
	if k == QRFG || k == QRBG {
		return strings.ToLower(strings.TrimPrefix(value, "#"))
	}
	return value
}

// scalarFor builds the node a validated value is stored as. The shapes are
// part of the file format: a colour is quoted, because 000000 or 1e3456
// unquoted is read back as a number, and the size is a plain integer.
func scalarFor(k Key, value string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	switch k {
	case QRFG, QRBG:
		n.Style = yaml.SingleQuotedStyle
	case QRSize:
		// Written back as the integer it is, so "01024" becomes 1024.
		size, _ := parseSize(value)
		n.Tag = "!!int"
		n.Value = strconv.Itoa(size)
	}
	return n
}

// Set stores value for k in one scope: the top of the file when team is
// empty, that team's section otherwise. It validates exactly as Load does, on
// the normalised value, and changes nothing when it refuses, so a rejected
// call leaves no empty section behind. An existing entry is updated in place
// and keeps its comment; a new one is appended at the end of its section, and
// missing sections are created.
//
// Nothing reaches the disk until Save.
func (f *File) Set(k Key, team, value string) error {
	if err := f.checkScope(k, team); err != nil {
		return err
	}
	segs, _ := segments(k, team)
	if code := check(k, normalise(k, value)); code != "" {
		return f.refuse(code, strings.Join(segs, "."), value)
	}

	parent := f.root()
	for _, seg := range segs[:len(segs)-1] {
		next := mapValue(parent, seg)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			parent.Content = append(parent.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}, next)
		}
		parent = next
	}

	node := scalarFor(k, normalise(k, value))
	name := segs[len(segs)-1]
	if existing := mapValue(parent, name); existing != nil && existing.Kind == yaml.ScalarNode {
		// Only the value changes. The node's comments, its position in the
		// file and its anchor stay, which is why it is not replaced.
		existing.Tag, existing.Value, existing.Style = node.Tag, node.Value, node.Style
		return nil
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, node)
	return nil
}

// Unset removes k from one scope, then every section that removal left empty:
// the team's `qr`, the team's section and `teams` itself. A section with
// anything else in it stays. Removing a key that is not there is not an
// error, so `config unset` can be run twice; a caller that wants to tell the
// person asks Get first.
//
// Comments are the person's and survive the removal, as they do `config
// set`: see removeEntry for where they go. A key's own line comment is the
// exception, because it says something about that line and goes with it.
//
// Nothing reaches the disk until Save.
func (f *File) Unset(k Key, team string) error {
	if err := f.checkScope(k, team); err != nil {
		return err
	}
	segs, _ := segments(k, team)

	// chain[i] is the mapping that holds segs[i], and heirs[i] is who inherits
	// the comments of its last entry: the document for the top mapping, and for
	// a section the key that names it, which is removed next when the section
	// is empty and passes them on again.
	chain := []*yaml.Node{f.root()}
	heirs := []*yaml.Node{f.doc}
	for _, seg := range segs[:len(segs)-1] {
		parent := chain[len(chain)-1]
		at := entryIndex(parent, seg)
		if at < 0 || parent.Content[at+1].Kind != yaml.MappingNode {
			return nil
		}
		heirs = append(heirs, parent.Content[at])
		chain = append(chain, parent.Content[at+1])
	}

	for i := len(segs) - 1; i >= 0; i-- {
		if !removeEntry(chain[i], heirs[i], segs[i]) || len(chain[i].Content) > 0 {
			return nil
		}
	}
	return nil
}

// entryIndex returns where the key called name is in mapping m's Content, the
// value being the next element, or -1 when there is none.
func entryIndex(m *yaml.Node, name string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			return i
		}
	}
	return -1
}

// removeEntry deletes the entry called name from mapping m and reports
// whether there was one.
//
// A comment above an entry, or in the foot after it, is a note the person
// wrote, and it need not be about that entry: the first key of a file carries
// the file's header, the last one the closing note. Deleting it with the entry
// is what made `config unset` take both away. The YAML library hangs both on
// the entry's key, so they are moved before the entry goes:
//
//   - the head comment to the next entry's key, which is what it now sits
//     above, or with no next entry to the foot of the one before;
//   - the foot comment to the foot of the entry before, or with none before to
//     the head of the next entry.
//
// When m has no other entry, heir takes them: the head after its own head
// comment and the foot before its own foot comment, which is the order they
// had in the file. For the top mapping heir is the document, whose comments
// are what a file of nothing else is written from; for a section it is the
// key that names the section, which Unset removes next because the section is
// empty, and which passes them on in the same way.
func removeEntry(m, heir *yaml.Node, name string) bool {
	at := entryIndex(m, name)
	if at < 0 {
		return false
	}
	gone := m.Content[at]
	head, foot := gone.HeadComment, gone.FootComment

	var prev, next *yaml.Node
	if at >= 2 {
		prev = m.Content[at-2]
	}
	if at+2 < len(m.Content) {
		next = m.Content[at+2]
	}

	switch {
	case prev == nil && next == nil:
		heir.HeadComment = joinComments(heir.HeadComment, head)
		heir.FootComment = joinComments(foot, heir.FootComment)
	case prev == nil:
		next.HeadComment = joinComments(head, foot, next.HeadComment)
	case next == nil:
		prev.FootComment = joinComments(prev.FootComment, head, foot)
	default:
		next.HeadComment = joinComments(head, next.HeadComment)
		prev.FootComment = joinComments(prev.FootComment, foot)
	}

	m.Content = slices.Delete(m.Content, at, at+2)
	return true
}

// joinComments puts comment blocks one under the other, leaving out the empty
// ones. A block that ends in a newline, which is how the YAML library says a
// blank line followed it, keeps that blank line before the next block.
func joinComments(blocks ...string) string {
	var kept []string
	for _, b := range blocks {
		if b != "" {
			kept = append(kept, b)
		}
	}
	return strings.Join(kept, "\n")
}
