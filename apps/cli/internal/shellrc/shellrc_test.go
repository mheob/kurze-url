package shellrc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// everyShell is the four supported shells, in the order the design lists them.
var everyShell = []Shell{Zsh, Bash, Fish, PowerShell}

// skipOnWindows skips a test that needs a Unix permission model or a symlink
// the current user may create. Windows only has a read-only bit, and creating
// a symlink there takes a privilege an ordinary account lacks.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs Unix file modes and symlinks")
	}
}

func TestCommandIsTheLineTheDesignSpecifies(t *testing.T) {
	// These are literals on purpose: the line is what runs in every new shell
	// of the person's, so a change to it has to be a change to this table.
	want := map[Shell]string{
		Zsh:        `if command -v kurze-url >/dev/null 2>&1; then (( $+functions[compdef] )) || { autoload -Uz compinit && compinit; }; source <(kurze-url completion zsh); fi`,
		Bash:       `if command -v kurze-url >/dev/null 2>&1; then eval "$(kurze-url completion bash)"; fi`,
		Fish:       `command -q kurze-url; and kurze-url completion fish | source`,
		PowerShell: `if (Get-Command kurze-url -ErrorAction SilentlyContinue) { kurze-url completion powershell | Out-String | Invoke-Expression }`,
	}
	for _, s := range everyShell {
		require.Equal(t, want[s], Command(s), string(s))
	}
}

func TestMarkerIsTheLineTheDesignSpecifies(t *testing.T) {
	require.Equal(t, "# kurze-url completion (added by kurze-url completion install)", Marker)
}

func TestBlockAndOwnFileShapes(t *testing.T) {
	for _, s := range everyShell {
		require.Equal(t, "\n"+Marker+"\n"+Command(s)+"\n", Block(s), string(s))
		require.Equal(t, Marker+"\n"+Command(s)+"\n", OwnFile(s), string(s))
	}
}

func TestAddThenRemoveRestoresTheFile(t *testing.T) {
	for _, s := range everyShell {
		for _, original := range []string{"", "export A=1\n", "export A=1" /* no trailing newline */, "export A=1\n\n", "a\r\nb\r\n"} {
			added := Add([]byte(original), s)
			require.Contains(t, string(added), "\n"+Marker+"\n")
			removed, ok := Remove(added)
			require.True(t, ok)
			require.Equal(t, original, string(removed), "%s %q", s, original)
		}
	}
}

func TestBlockStartsOnALineOfItsOwn(t *testing.T) {
	got := string(Add([]byte("export A=1"), Bash))
	require.Equal(t, "export A=1\n"+Marker+"\n"+Command(Bash)+"\n", got)
}

func TestAddAndRemoveLeaveTheirInputAlone(t *testing.T) {
	// A slice with spare capacity is the case where append would write into
	// the caller's backing array.
	backing := make([]byte, 0, 4096)
	backing = append(backing, "export A=1\n"...)
	original := string(backing)

	added := Add(backing, Zsh)
	require.Equal(t, original, string(backing))
	spare := backing[len(backing):cap(backing)]
	require.Equal(t, make([]byte, len(spare)), spare, "Add wrote into its input's spare capacity")

	// The block has to sit before other bytes, or there is no tail for Remove
	// to shift into the gap.
	withTail := append(append([]byte(nil), added...), "export B=2\n"...)
	snapshot := string(withTail)
	removed, ok := Remove(withTail)
	require.True(t, ok)
	require.Equal(t, snapshot, string(withTail), "Remove wrote into its input")
	require.Equal(t, original+"export B=2\n", string(removed))
}

func TestInspect(t *testing.T) {
	require.Equal(t, Absent, Inspect([]byte("export A=1\n")))
	require.Equal(t, Absent, Inspect(nil))
	require.Equal(t, Installed, Inspect(Add(nil, Zsh)))
	require.Equal(t, Installed, Inspect([]byte(OwnFile(Fish))))
	require.Equal(t, ByHand, Inspect([]byte("source <(kurze-url completion zsh)\n")))
	require.Equal(t, ByHand, Inspect([]byte("kurze-url completion fish | source\n")))
}

func TestInspectPrefersTheMarkerOverAHandWrittenLine(t *testing.T) {
	// The marker line itself contains "kurze-url completion", so a file that
	// has both must read as installed, or install would report a setup it
	// did not make and uninstall would refuse to touch it.
	content := "source <(kurze-url completion zsh)\n" + Block(Zsh)
	require.Equal(t, Installed, Inspect([]byte(content)))
}

func TestRemoveTakesOnlyTheFirstBlock(t *testing.T) {
	content := "a\n" + Block(Zsh) + "b\n" + Block(Zsh)
	got, ok := Remove([]byte(content))
	require.True(t, ok)
	require.Equal(t, "a\nb\n"+Block(Zsh), string(got))
}

func TestRemoveRecognisesTheBlockOfAnyShell(t *testing.T) {
	// install writes the block of the shell it was run for, but uninstall is
	// given no hint and the same file can be sourced by more than one shell.
	for _, s := range everyShell {
		got, ok := Remove([]byte("x\n" + Block(s) + "y\n"))
		require.True(t, ok, string(s))
		require.Equal(t, "x\ny\n", string(got), string(s))
	}
}

func TestRemoveEmptiesAnOwnFile(t *testing.T) {
	// The marker is on the first line, with no newline in front of it.
	got, ok := Remove([]byte(OwnFile(Fish)))
	require.True(t, ok)
	require.Empty(t, string(got))
}

func TestRemoveSurvivesAnEditedBlock(t *testing.T) {
	edited := "a\n" + Marker + "\nsomething else\nb\n"
	got, ok := Remove([]byte(edited))
	require.True(t, ok)
	require.Equal(t, "a\nb\n", string(got))
}

func TestRemoveSurvivesAnEditedBlockWithWindowsLineEndings(t *testing.T) {
	// An editor on Windows rewrites the block's line endings, which defeats
	// the exact match but not the fallback.
	edited := "a\r\n" + Marker + "\r\nsomething else\r\nb\r\n"
	got, ok := Remove([]byte(edited))
	require.True(t, ok)
	require.Equal(t, "a\r\nb\r\n", string(got))
}

func TestRemoveSurvivesAMarkerWithNoLineAfterIt(t *testing.T) {
	for _, edited := range []string{"a\n" + Marker, "a\n" + Marker + "\n"} {
		got, ok := Remove([]byte(edited))
		require.True(t, ok, "%q", edited)
		require.Equal(t, "a\n", string(got), "%q", edited)
	}
}

func TestRemoveWithoutMarker(t *testing.T) {
	_, ok := Remove([]byte("source <(kurze-url completion zsh)\n"))
	require.False(t, ok)

	_, ok = Remove(nil)
	require.False(t, ok)
}

func TestParseShell(t *testing.T) {
	for in, want := range map[string]Shell{
		"zsh":        Zsh,
		"bash":       Bash,
		"fish":       Fish,
		"powershell": PowerShell,
		"pwsh":       PowerShell,
	} {
		got, ok := ParseShell(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "sh", "dash", "ksh", "nu", "ZSH", "/bin/zsh", "pwsh.exe", " zsh"} {
		got, ok := ParseShell(in)
		require.False(t, ok, "%q", in)
		require.Empty(t, string(got), "%q", in)
	}
}

func TestWriteFileKeepsModeAndFollowsSymlinks(t *testing.T) {
	skipOnWindows(t)

	for name, linkTo := range map[string]func(target string) string{
		"absolute link": func(target string) string { return target },
		"relative link": func(string) string { return filepath.Join("dotfiles", "zshrc") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "dotfiles", "zshrc")
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte("old\n"), 0o600))
			link := filepath.Join(dir, ".zshrc")
			require.NoError(t, os.Symlink(linkTo(target), link))

			require.NoError(t, WriteFile(link, []byte("new\n"), 0o644))

			// The link is still a link, to the same place.
			info, err := os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink, "the symlink was replaced by a regular file")
			dest, err := os.Readlink(link)
			require.NoError(t, err)
			require.Equal(t, linkTo(target), dest)

			// The target holds the data, and perm was ignored for a file that
			// already had a mode.
			got, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "new\n", string(got))
			info, err = os.Stat(target)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

			// Reading through the link gives the same bytes, and the
			// temporary file did not stay behind in the target's directory.
			got, err = os.ReadFile(link)
			require.NoError(t, err)
			require.Equal(t, "new\n", string(got))
			require.Equal(t, []string{"zshrc"}, namesIn(t, filepath.Dir(target)))
		})
	}
}

func TestWriteFileKeepsTheModeOfARegularFile(t *testing.T) {
	skipOnWindows(t)

	path := filepath.Join(t.TempDir(), ".bashrc")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o640))

	require.NoError(t, WriteFile(path, []byte("new\n"), 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestWriteFileUsesPermForANewFile(t *testing.T) {
	skipOnWindows(t)

	// 0640 is neither os.CreateTemp's 0600 nor the usual 0644, so only a
	// deliberate chmod can produce it, and a umask cannot change it.
	path := filepath.Join(t.TempDir(), ".zshrc")

	require.NoError(t, WriteFile(path, []byte("new\n"), 0o640))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	require.Equal(t, []string{".zshrc"}, namesIn(t, filepath.Dir(path)))
}

func TestWriteFileCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".config", "fish", "kurze-url.fish")

	require.NoError(t, WriteFile(path, []byte("new\n"), 0o640))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	for _, dir := range []string{filepath.Join(root, ".config"), filepath.Dir(path)} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir(), dir)
		if runtime.GOOS != "windows" {
			// Owner can enter and write, whatever the umask removed from the
			// rest.
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm()&0o700, dir)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	}
}

func TestWriteFileReplacesTheWholeContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	require.NoError(t, os.WriteFile(path, []byte("a much longer old content\n"), 0o600))

	require.NoError(t, WriteFile(path, []byte("short\n"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "short\n", string(got))
}

func TestWriteFileLeavesNoTemporaryFileWhenItFails(t *testing.T) {
	// A directory where the file should be: the rename cannot succeed, and
	// the temporary file written before it has to be cleaned up.
	root := t.TempDir()
	path := filepath.Join(root, ".zshrc")
	require.NoError(t, os.Mkdir(path, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "inside"), []byte("x"), 0o600))

	require.Error(t, WriteFile(path, []byte("new\n"), 0o644))

	require.Equal(t, []string{".zshrc"}, namesIn(t, root))
}

func TestWriteFileFailsWhenADirectoryCannotBeCreated(t *testing.T) {
	// A regular file where a directory is needed.
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	require.Error(t, WriteFile(filepath.Join(blocker, "sub", "file"), []byte("x"), 0o644))
}

// namesIn lists the entries of dir by name, sorted as os.ReadDir sorts them.
func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
