package output

import "golang.org/x/term"

// IsTerminal reports whether fd is a terminal, which is how a command knows
// whether a person is looking at its output or a pipe is reading it.
func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// Width returns the terminal's width in columns, or 0 when fd is not a
// terminal or its size cannot be read. 0 is the caller's "unknown", and
// Table reads it as "do not shorten".
func Width(fd uintptr) int {
	width, _, err := term.GetSize(int(fd))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}
