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
// Nothing reaches the disk until Save.
func (f *File) Unset(k Key, team string) error {
	if err := f.checkScope(k, team); err != nil {
		return err
	}
	segs, _ := segments(k, team)

	// chain[i] is the mapping that holds segs[i].
	chain := []*yaml.Node{f.root()}
	for _, seg := range segs[:len(segs)-1] {
		next := mapValue(chain[len(chain)-1], seg)
		if next == nil || next.Kind != yaml.MappingNode {
			return nil
		}
		chain = append(chain, next)
	}

	for i := len(segs) - 1; i >= 0; i-- {
		if !removeEntry(chain[i], segs[i]) || len(chain[i].Content) > 0 {
			return nil
		}
	}
	return nil
}

// removeEntry deletes the entry called name from mapping m, comments and all,
// and reports whether there was one.
func removeEntry(m *yaml.Node, name string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			m.Content = slices.Delete(m.Content, i, i+2)
			return true
		}
	}
	return false
}
