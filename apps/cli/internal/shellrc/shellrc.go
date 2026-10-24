// Package shellrc edits the shell configuration files that `kurze-url
// completion install` and `uninstall` touch.
//
// What it adds is a block of two lines, a marker comment and the command that
// turns completion on, and what it removes is exactly that block. The package
// is pure on purpose: it works on bytes, apart from WriteFile, and it knows
// neither which file belongs to which shell nor how to ask the person. A
// person's .zshrc is one of the few files they treat as theirs, so the one
// promise it makes is that adding the block and then removing it gives the
// file back byte for byte.
package shellrc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Marker is the comment line that heads the block. It is how a second install
// recognises the block and how uninstall finds it, so a block without it is
// someone else's setup and is left alone.
const Marker = "# kurze-url completion (added by kurze-url completion install)"

// handWritten is what a completion line made by hand has in common with ours.
// It is also inside Marker, which is why Inspect looks for the marker
// first.
const handWritten = "kurze-url completion"

// dirMode is the mode of a directory WriteFile has to create, before the
// umask takes its share.
const dirMode os.FileMode = 0o755

// Shell is a shell completion can be installed for.
type Shell string

// The four shells the CLI generates completion scripts for.
const (
	Zsh        Shell = "zsh"
	Bash       Shell = "bash"
	Fish       Shell = "fish"
	PowerShell Shell = "powershell"
)

// shells lists every Shell, for the code that has to recognise a block
// without being told which shell wrote it.
var shells = []Shell{Zsh, Bash, Fish, PowerShell}

// ParseShell reads a shell's name, exactly as the CLI spells it. It also
// takes `pwsh`, which is how PowerShell 7 names itself in $SHELL on macOS and
// Linux, and reports false for anything else, the empty string included.
func ParseShell(s string) (Shell, bool) {
	switch s {
	case "zsh":
		return Zsh, true
	case "bash":
		return Bash, true
	case "fish":
		return Fish, true
	case "powershell", "pwsh":
		return PowerShell, true
	}
	return "", false
}

// Command returns the one line that follows the marker, or "" for a Shell this
// package does not know. It is generated at shell start and never a snapshot
// of the script, so a CLI update brings its own completions along.
//
// Every line first checks that kurze-url is on the PATH, because a shell that
// prints an error at every start is a shell its owner fixes by deleting the
// block.
func Command(s Shell) string {
	switch s {
	case Zsh:
		// A .zshrc without compinit would get a block that does nothing:
		// the script calls compdef, which does not exist yet.
		return `if command -v kurze-url >/dev/null 2>&1; then (( $+functions[compdef] )) || { autoload -Uz compinit && compinit; }; source <(kurze-url completion zsh); fi`
	case Bash:
		// eval, not source <(…): bash 3.2, which macOS still ships, reads
		// nothing from a process substitution given to source and says so
		// to nobody.
		return `if command -v kurze-url >/dev/null 2>&1; then eval "$(kurze-url completion bash)"; fi`
	case Fish:
		return `command -q kurze-url; and kurze-url completion fish | source`
	case PowerShell:
		return `if (Get-Command kurze-url -ErrorAction SilentlyContinue) { kurze-url completion powershell | Out-String | Invoke-Expression }`
	}
	return ""
}

// Block returns what Add appends to a file: a newline, the marker and the
// command, each line ended. The leading newline does two jobs. It ends a last
// line that has no newline of its own, so the marker never lands in the middle
// of one, and it is exactly what Remove takes back, which is how uninstall
// restores the file byte for byte.
func Block(s Shell) string {
	return "\n" + Marker + "\n" + Command(s) + "\n"
}

// OwnFile returns the whole content of a file that is ours alone, which is
// fish's completions file. It has no leading newline because there is no line
// before it to end.
func OwnFile(s Shell) string {
	return Marker + "\n" + Command(s) + "\n"
}

// State says what a file already holds of our setup.
type State int

const (
	// Absent means the file has no completion setup for kurze-url.
	Absent State = iota
	// Installed means the file holds the marker, so a block of ours.
	Installed
	// ByHand means the file calls `kurze-url completion` without the marker:
	// somebody set it up themselves, which install and uninstall leave alone.
	ByHand
)

// Inspect reads a file's content and says which of the three it is. The
// marker wins over the hand-written check, because the marker line itself
// contains the words that check looks for.
func Inspect(content []byte) State {
	switch {
	case bytes.Contains(content, []byte(Marker)):
		return Installed
	case bytes.Contains(content, []byte(handWritten)):
		return ByHand
	}
	return Absent
}

// Add returns content with the block for s after it. It never writes into
// content, whose spare capacity is the caller's. The caller has checked
// Inspect first: Add does not look for a block that is already there.
func Add(content []byte, s Shell) []byte {
	block := Block(s)
	out := make([]byte, 0, len(content)+len(block))
	out = append(out, content...)
	return append(out, block...)
}

// Remove takes the block out of content and reports whether it found one,
// leaving every other byte as it was. It never writes into content.
//
// The block it looks for is exactly what Block writes, for any of the four
// shells, and the first such block goes. Block's leading newline goes with it,
// except where it is the terminator of the file's last line (the file had no
// newline at its end) and something was appended after the block since:
// taking it back would glue that line to the next, so it stays. At the end of
// the file nothing follows, and the newline goes, which is what makes add
// then remove byte-exact.
//
// A block someone edited, a different command line or line endings an editor
// rewrote, no longer matches, so the fallback removes the marker's own line
// and the line after it only when that line is still a call of ours, one that
// holds "kurze-url completion". The marker says the line below it was written
// by install, but a person may have replaced or deleted that line, and the
// line that happens to follow is then theirs: uninstall touches nothing else,
// so it stays. The fallback also leaves behind the blank line Block put in
// front of the marker.
//
// It reports false only when the marker is absent, which is what Inspect
// calls Absent or ByHand.
func Remove(content []byte) ([]byte, bool) {
	if start, end, ok := exactBlock(content); ok {
		if start > 0 && content[start-1] != '\n' && end < len(content) {
			start++
		}
		return splice(content, start, end), true
	}

	marker := bytes.Index(content, []byte(Marker))
	if marker < 0 {
		return content, false
	}
	start := bytes.LastIndexByte(content[:marker], '\n') + 1
	end := nextLine(content, marker)
	// A marker with no line after it leaves next empty, and an empty line
	// holds nothing of ours.
	next := content[end:nextLine(content, end)]
	if bytes.Contains(next, []byte(handWritten)) && !bytes.Contains(next, []byte(Marker)) {
		end += len(next)
	}
	return splice(content, start, end), true
}

// exactBlock finds the first block Block could have written, whichever shell
// it was written for. Two shells' blocks cannot match at one position: they
// differ in the command line, and both end at its newline.
func exactBlock(content []byte) (start, end int, ok bool) {
	start = -1
	for _, s := range shells {
		block := []byte(Block(s))
		if i := bytes.Index(content, block); i >= 0 && (start < 0 || i < start) {
			start, end = i, i+len(block)
		}
	}
	return start, end, start >= 0
}

// nextLine returns the offset of the start of the line after the one that
// holds from, or len(content) when that is the last line.
func nextLine(content []byte, from int) int {
	if i := bytes.IndexByte(content[from:], '\n'); i >= 0 {
		return from + i + 1
	}
	return len(content)
}

// splice returns content without content[start:end], in a slice of its own.
// append(content[:start], …) would shift the tail inside the caller's array.
func splice(content []byte, start, end int) []byte {
	out := make([]byte, 0, len(content)-(end-start))
	out = append(out, content[:start]...)
	return append(out, content[end:]...)
}

// WriteFile writes data to path through a temporary file in the same
// directory and an atomic rename, so a crash or a full disk leaves the old
// file whole instead of half a new one. The temporary file is a sibling and
// not in /tmp because a rename is atomic only within one filesystem.
//
// A path that is a symlink is a dotfile manager's doing. Renaming over it
// would replace the link with a regular file and cut the file from the
// repository it is managed in, so the write goes to the link's target and the
// link stays where it is.
//
// An existing file keeps its mode, and perm is the mode of a file that is new.
// Missing parent directories are created with mode 0755. Owner, ACLs and
// extended attributes of an existing file do not carry over to its
// replacement.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	mode := perm
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".kurze-url-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	// Remove is a no-op after the rename and the cleanup for every early
	// return before it.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	// CreateTemp makes the file 0600, whatever the umask says.
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
