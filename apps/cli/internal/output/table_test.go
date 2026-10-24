package output

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncateIsRuneAware(t *testing.T) {
	// "ü" is two bytes. Cutting by bytes would split it, or leave the result
	// one rune short.
	require.Equal(t, "Grünwal…", Truncate("Grünwald-Sommerfest", 8))
	require.Equal(t, "日本語…", Truncate("日本語テキスト", 4))
}

func TestTruncateLeavesWhatFits(t *testing.T) {
	require.Equal(t, "short", Truncate("short", 10))
	require.Equal(t, "exact", Truncate("exact", 5), "a string of exactly max runes is not cut")
	require.Equal(t, "Grünwald", Truncate("Grünwald", 8))
	require.Empty(t, Truncate("", 3))
}

func TestTruncateAtTheSmallestLimits(t *testing.T) {
	require.Equal(t, "…", Truncate("abcdef", 1))
	require.Empty(t, Truncate("abcdef", 0))
	require.Empty(t, Truncate("abcdef", -4))
	// One rune that fits needs no ellipsis, however small the limit.
	require.Equal(t, "a", Truncate("a", 1))
}

func TestTruncateNeverExceedsTheLimit(t *testing.T) {
	for limit := 0; limit <= 12; limit++ {
		got := Truncate("Grünwald-Sommerfest", limit)
		require.LessOrEqual(t, utf8.RuneCountInString(got), limit, "limit %d", limit)
	}
}

// runeIndex is strings.Index in runes, because "…" is three bytes and one
// column.
func runeIndex(s, substr string) int {
	i := strings.Index(s, substr)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(s[:i])
}

func render(t *testing.T, header []string, rows [][]string, width, shrinkCol int) string {
	t.Helper()
	var buf bytes.Buffer
	Table(&buf, header, rows, width, shrinkCol)
	return buf.String()
}

func TestTableAlignsColumnsWithTwoSpaces(t *testing.T) {
	got := render(t,
		[]string{"Slug", "Name", "Role"},
		[][]string{
			{"sv-gruenwald", "SV Grünwald", "Owner"},
			{"tc", "TC", "Viewer"},
		}, 0, 0)

	require.Equal(t,
		"Slug          Name         Role\n"+
			"sv-gruenwald  SV Grünwald  Owner\n"+
			"tc            TC           Viewer\n",
		got)
}

func TestTableShrinksOneColumn(t *testing.T) {
	destination := "https://example.org/" + strings.Repeat("x", 100)
	got := render(t,
		[]string{"Short URL", "Destination", "State"},
		[][]string{
			{"https://go.kurze-url.app/abc", destination, "active"},
			{"https://go.kurze-url.app/def", "https://example.org/", "disabled"},
		}, 60, 1)

	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	require.Len(t, lines, 3)
	for _, line := range lines {
		require.LessOrEqual(t, utf8.RuneCountInString(line), 60, line)
	}
	// Only the chosen column is cut, and it is cut with an ellipsis.
	require.Contains(t, lines[1], "…")
	require.Contains(t, lines[1], "https://go.kurze-url.app/abc")
	require.True(t, strings.HasSuffix(lines[1], "active"), lines[1])
	// A row that already fits is left alone, and the columns still line up.
	require.Contains(t, lines[2], "https://example.org/")
	require.NotContains(t, lines[2], "…")
	require.Equal(t, runeIndex(lines[1], "active"), runeIndex(lines[2], "disabled"))
}

func TestTableFillsTheWidthExactly(t *testing.T) {
	got := render(t,
		[]string{"A", "B"},
		[][]string{{"aaaa", strings.Repeat("b", 50)}}, 20, 1)

	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	require.Equal(t, 20, utf8.RuneCountInString(lines[1]), "the shrunk column takes exactly what is left")
}

func TestTableWithoutAWidthKeepsEverything(t *testing.T) {
	long := strings.Repeat("y", 300)

	got := render(t, []string{"A", "B"}, [][]string{{"x", long}}, 0, 1)

	require.Contains(t, got, long)
	require.NotContains(t, got, "…")
}

func TestTableDoesNotPadTheLastColumn(t *testing.T) {
	got := render(t,
		[]string{"A", "B"},
		[][]string{{"long value", "x"}, {"s", ""}}, 0, 0)

	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		require.Equal(t, strings.TrimRight(line, " "), line, "trailing blanks in %q", line)
	}
}

func TestTableCountsRunesNotBytes(t *testing.T) {
	got := render(t,
		[]string{"Name", "Role"},
		[][]string{{"Grünwald", "Owner"}, {"Muster", "Viewer"}}, 0, 0)

	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	// "ü" must count as one column, or "Owner" would sit one place too far
	// left of "Viewer" and "Role".
	require.Equal(t, 10, strings.Index(lines[0], "Role"))
	require.Equal(t, "Name      Role", lines[0])
	require.Equal(t, "Grünwald  Owner", lines[1])
	require.Equal(t, "Muster    Viewer", lines[2])
}

func TestTableWithAnEmptyHeaderCellKeepsItsColumn(t *testing.T) {
	got := render(t,
		[]string{"", "Slug"},
		[][]string{{"*", "sv"}, {"", "tc"}}, 0, 0)

	require.Equal(t,
		"   Slug\n"+
			"*  sv\n"+
			"   tc\n",
		got)
}

func TestTableWithoutAHeaderPrintsOnlyRows(t *testing.T) {
	got := render(t, nil, [][]string{{"a", "b"}, {"cc", "d"}}, 0, 0)

	require.Equal(t, "a   b\ncc  d\n", got)
}

func TestTableToleratesRaggedRowsAndABadShrinkColumn(t *testing.T) {
	require.NotPanics(t, func() {
		got := render(t,
			[]string{"A", "B", "C"},
			[][]string{{"1"}, {"1", "2", "3", "4"}}, 10, 7)
		require.Contains(t, got, "1")
	})
	require.NotPanics(t, func() { _ = render(t, nil, nil, 10, 0) })
	require.NotPanics(t, func() { _ = render(t, []string{"A"}, nil, 10, -1) })
}

func TestTableGivesUpWhenTheOtherColumnsAloneAreTooWide(t *testing.T) {
	got := render(t,
		[]string{"A", "B"},
		[][]string{{strings.Repeat("a", 30), "bbbbbbbbbb"}}, 10, 1)

	// Nothing else can be cut, so the column keeps one rune: the ellipsis.
	require.Contains(t, got, strings.Repeat("a", 30)+"  …\n")
}

func TestTableReplacesControlCharacters(t *testing.T) {
	got := render(t,
		[]string{"Name"},
		[][]string{{"evil\x1b[31m\nname\t!"}}, 0, 0)

	require.NotContains(t, got, "\x1b")
	require.Equal(t, 2, strings.Count(got, "\n"), "a newline inside a cell must not add a row")
	require.NotContains(t, got, "\t")
}

// Clean is what commands put around a server's text before it goes into a
// sentence, so it is judged on its own as well as through Table.
func TestCleanReplacesEveryControlCharacterWithASpace(t *testing.T) {
	// The OSC 52 clipboard write a probe used: ESC ] 52 ; c ; <base64> BEL.
	require.Equal(t, "evil ]52;c;SGVsbG8= ", Clean("evil\x1b]52;c;SGVsbG8=\x07"))
	// C0 (tab, newline, carriage return), DEL and the C1 range, where 0x9d is
	// the eight-bit form of the OSC introducer.
	require.Equal(t, "a b c d e f", Clean("a\tb\nc\rd\x7fe\u009df"))
}

func TestCleanLeavesOrdinaryTextAlone(t *testing.T) {
	for _, text := range []string{
		"",
		"https://go.kurze-url.app/sommerfest",
		"SV Grünwald · Vorstand “Jugend”",
		"日本語 ñ ✓",
	} {
		require.Equal(t, text, Clean(text))
	}
}

func TestTSVHasNoHeader(t *testing.T) {
	var buf bytes.Buffer

	TSV(&buf, [][]string{
		{"*", "sv-gruenwald", "SV Grünwald", "Owner"},
		{"", "tc-muster", "TC Muster", "Viewer"},
	})

	require.Equal(t,
		"*\tsv-gruenwald\tSV Grünwald\tOwner\n"+
			"\ttc-muster\tTC Muster\tViewer\n",
		buf.String())
}

func TestTSVKeepsEmptyCellsAndFullValues(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("z", 400)

	TSV(&buf, [][]string{{"", long, ""}})

	require.Equal(t, "\t"+long+"\t\n", buf.String(), "TSV is never truncated")
}

func TestTSVKeepsOneRowPerLine(t *testing.T) {
	var buf bytes.Buffer

	TSV(&buf, [][]string{{"two\nlines", "a\tb", "ok"}})

	require.Equal(t, "two lines\ta b\tok\n", buf.String())
}

func TestTSVWithNoRowsWritesNothing(t *testing.T) {
	var buf bytes.Buffer

	TSV(&buf, nil)

	require.Empty(t, buf.String())
}

func TestAPipeIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	require.False(t, IsTerminal(w.Fd()))
	require.False(t, IsTerminal(r.Fd()))
	require.Zero(t, Width(w.Fd()), "a pipe has no width, and 0 means unknown")
}
