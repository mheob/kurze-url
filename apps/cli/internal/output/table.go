// Package output formats what a command prints as a result: an aligned table
// for a person at a terminal, tab-separated rows for a pipe, and the width
// of the terminal that decides which of the two fits.
//
// Neither formatter knows a language. The caller passes the header already
// translated, and a cell is whatever text the caller made of a value.
package output

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ellipsis marks where Truncate cut. It is one rune wide, so a limit of n
// runes holds n-1 runes of the text and the mark.
const ellipsis = "…"

// columnGap separates the columns of a Table.
const columnGap = "  "

// Truncate shortens s to at most limit runes, ending in "…" when it had to
// cut.
//
// It counts runes and not bytes: the texts here are German names and a cut
// through the middle of "ü" would print a broken character. A limit of 1 or
// less leaves no room for text, so it returns the mark alone (1) or nothing
// (0 and below). A string that fits is returned as it is.
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit-1]) + ellipsis
}

// Clean makes a piece of text safe to print: every control character, tabs
// and newlines included, becomes a space.
//
// Text a team's admin wrote (a team name, a folder) or the API returned (a
// destination, a short URL, an error's detail) is not the CLI's own. An
// embedded ESC or BEL would let that text drive the person's terminal: an OSC
// 52 sequence writes to the clipboard without showing anything. An embedded
// newline would turn one table row into two and an embedded tab would add a
// column, so a script splitting the output would misread it. None of these has
// a place in a name, so Table and TSV clean every cell, and a command cleans
// what it puts into a sentence or a plain line itself, because only the
// command knows which of its values came from outside.
//
// Only the characters Unicode classes as control are replaced, which covers
// the C0 and C1 ranges and DEL. Text with none comes back unchanged.
func Clean(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

// cleanRow cleans every cell of a row.
func cleanRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = Clean(cell)
	}
	return out
}

// Table writes header and rows as columns aligned with two spaces between
// them, every column as wide as its widest cell. A nil or empty header
// writes no header line. Cells are measured in runes, which is exact for the
// Latin text these tables hold and is the width a terminal gives them.
//
// width is the terminal's width, or 0 for "do not shorten". When it is
// positive and a line would be wider, only the column at shrinkCol is
// shortened with Truncate, by exactly as much as is needed, so the other
// columns stay whole and still line up. It collapses to one rune at the
// least: when the other columns alone are wider than the terminal nothing
// else is cut, and the lines overflow rather than lose a short URL. A
// shrinkCol outside the columns shortens nothing.
//
// The last column is not padded, and no line ends in a blank, so the output
// copies cleanly from a terminal.
func Table(w io.Writer, header []string, rows [][]string, width, shrinkCol int) {
	all := make([][]string, 0, len(rows)+1)
	if len(header) > 0 {
		all = append(all, cleanRow(header))
	}
	for _, row := range rows {
		all = append(all, cleanRow(row))
	}

	columns := 0
	for _, row := range all {
		columns = max(columns, len(row))
	}
	widths := make([]int, columns)
	for _, row := range all {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	if width > 0 && shrinkCol >= 0 && shrinkCol < columns {
		total := gapsWidth(columns)
		for _, column := range widths {
			total += column
		}
		if excess := total - width; excess > 0 {
			widths[shrinkCol] = max(widths[shrinkCol]-excess, 1)
		}
	}

	for _, row := range all {
		cells := make([]string, columns)
		for i := range cells {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if i == shrinkCol {
				cell = Truncate(cell, widths[i])
			}
			cells[i] = cell + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
		}
		line := strings.TrimRight(strings.Join(cells, columnGap), " ")
		// A failed write to the person's own terminal has nowhere to be
		// reported, so it is not.
		_, _ = fmt.Fprintln(w, line)
	}
}

// gapsWidth is the width of every gap between n columns.
func gapsWidth(n int) int {
	if n < 2 {
		return 0
	}
	return (n - 1) * len(columnGap)
}

// TSV writes rows as tab-separated lines, with no header and no shortening.
//
// It is the format for a pipe: a script splits on the tab and reads the
// columns by position, so nothing a person would want trimmed to fit a
// screen is trimmed here, and a header would be one more row to skip.
func TSV(w io.Writer, rows [][]string) {
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, strings.Join(cleanRow(row), "\t"))
	}
}
