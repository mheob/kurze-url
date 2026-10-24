package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/shellrc"
)

// psCommand is what install asks each PowerShell, word for word: UTF-8 output
// without a byte-order mark, then the all-hosts profile on the first line of
// the answer and the execution policy on the second.
var psCommand = []string{
	"-NoProfile", "-NonInteractive", "-Command",
	"[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); $PROFILE.CurrentUserAllHosts; Get-ExecutionPolicy",
}

// shellHarness is a harness whose $SHELL is shell.
func shellHarness(t *testing.T, shell string) *harness {
	t.Helper()
	h := newHarness(t)
	h.env["SHELL"] = shell
	return h
}

// answer puts a person on stdin's terminal who types text.
func (h *harness) answer(text string) {
	h.stdinTTY = true
	h.setStdin(text)
}

// homeFile is a path in the harness's home directory.
func (h *harness) homeFile(name string) string {
	return filepath.Join(h.home, name)
}

// putFile writes a file the test arranges, creating its directory.
func putFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// fileContent returns a file's content, and whether it exists.
func fileContent(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	require.NoError(t, err)
	return string(data), true
}

// requireEmptyDir fails when dir holds anything: a command wrote where it had
// no business to.
func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "%s was written to", dir)
}

// ---- install ----

func TestInstallAsksAndWritesTheZshBlock(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1")
	h.answer("\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\n"+shellrc.Marker+"\n"+shellrc.Command(shellrc.Zsh)+"\n", got)
	require.Equal(t,
		"Add tab completion for zsh to "+zshrc+"? [Y/n] "+
			"Added tab completion to "+zshrc+".\n"+
			"Open a new shell, or run `source '"+zshrc+"'`, to use it.\n",
		h.stderr.String())
	require.Empty(t, h.stdout.String())
}

func TestInstallHonoursZDOTDIR(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zdotdir := filepath.Join(t.TempDir(), "z")
	h.env["ZDOTDIR"] = zdotdir

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, ok := fileContent(t, filepath.Join(zdotdir, ".zshrc"))
	require.True(t, ok, "the .zshrc in ZDOTDIR was not written")
	require.Equal(t, shellrc.Block(shellrc.Zsh), got)
	requireEmptyDir(t, h.home)
}

func TestInstallTwiceWritesOnce(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")

	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	h.stderr.Reset()
	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, 1, strings.Count(got, shellrc.Marker))
	require.Equal(t, "Tab completion is already set up in "+zshrc+".\n", h.stderr.String())

	// Without --yes, on a terminal, it still asks nothing.
	h.stderr.Reset()
	h.answer("n\n")
	code = h.run("completion", "install")

	require.Equal(t, 0, code)
	require.Equal(t, "Tab completion is already set up in "+zshrc+".\n", h.stderr.String())
}

func TestInstallLeavesAHandMadeSetupAlone(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	const handMade = "source <(kurze-url completion zsh)\n"
	putFile(t, zshrc, handMade)
	h.answer("\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, handMade, got)
	require.Equal(t,
		zshrc+" already sets up tab completion for kurze-url, so it was left as it is.\n",
		h.stderr.String())
}

// Review Focus 1: a no leaves the file exactly as it was, down to its
// modification time.
func TestInstallDeclined(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	past := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, os.Chtimes(zshrc, past, past))
	h.answer("n\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\n", got)
	info, err := os.Stat(zshrc)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(past), "the file was touched: %s", info.ModTime())
	require.True(t, strings.HasSuffix(h.stderr.String(), "[Y/n] Nothing was changed.\n"), h.stderr.String())
}

func TestInstallDeclinesEveryOtherAnswer(t *testing.T) {
	for _, answer := range []string{"no\n", "nein\n", "x\n", "yy\n"} {
		h := shellHarness(t, "/bin/zsh")
		h.answer(answer)

		require.Equal(t, 0, h.run("completion", "install"), answer)
		_, ok := fileContent(t, h.homeFile(".zshrc"))
		require.False(t, ok, "%q counted as yes", answer)
	}
}

func TestInstallAcceptsEveryYes(t *testing.T) {
	for _, answer := range []string{"\n", "y\n", "YES\n", " j \n", "Ja\n", "y"} {
		h := shellHarness(t, "/bin/zsh")
		h.answer(answer)

		require.Equal(t, 0, h.run("completion", "install"), answer)
		got, _ := fileContent(t, h.homeFile(".zshrc"))
		require.Equal(t, shellrc.Block(shellrc.Zsh), got, "%q did not count as yes", answer)
	}
}

// A terminal whose input ends before anything was typed (Ctrl+D) gave no
// answer, and no answer is not a yes.
func TestInstallTreatsAClosedStdinAsNo(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	h.answer("")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	_, ok := fileContent(t, h.homeFile(".zshrc"))
	require.False(t, ok)
	require.True(t, strings.HasSuffix(h.stderr.String(), "[Y/n] \nNothing was changed.\n"), h.stderr.String())
}

// Review Focus 1: nobody can answer, so nothing is read and nothing written.
func TestInstallWithoutTerminalNeedsYes(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	h.setStdin("y\n")

	code := h.run("completion", "install")

	require.Equal(t, 1, code)
	_, ok := fileContent(t, h.homeFile(".zshrc"))
	require.False(t, ok, "the file was created")
	require.Equal(t,
		"kurze-url: There is no terminal to ask for confirmation. Add --yes to go ahead without asking.\n",
		h.stderr.String())
}

func TestInstallAcceptsJa(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	h.env["LANG"] = "de_DE.UTF-8"
	zshrc := h.homeFile(".zshrc")
	h.answer("j\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, shellrc.Block(shellrc.Zsh), got)
	require.True(t, strings.HasPrefix(h.stderr.String(),
		"Tab-Vervollständigung für zsh in "+zshrc+" eintragen? [J/n] Tab-Vervollständigung in "),
		h.stderr.String())
}

func TestInstallFishWritesItsOwnFile(t *testing.T) {
	h := shellHarness(t, "/usr/bin/fish")
	xdg := filepath.Join(t.TempDir(), "xdg")
	h.env["XDG_CONFIG_HOME"] = xdg
	fishFile := filepath.Join(xdg, "fish", "completions", "kurze-url.fish")

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, fishFile)
	require.Equal(t, shellrc.OwnFile(shellrc.Fish), got)
	require.Equal(t,
		"Added tab completion to "+fishFile+".\n"+
			"Open a new shell to use it.\n",
		h.stderr.String())
	requireEmptyDir(t, h.home)
}

func TestInstallFishWithoutXDGUsesDotConfig(t *testing.T) {
	h := shellHarness(t, "/usr/bin/fish")

	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())

	got, _ := fileContent(t, h.homeFile(filepath.Join(".config", "fish", "completions", "kurze-url.fish")))
	require.Equal(t, shellrc.OwnFile(shellrc.Fish), got)
}

// Cobra's help tells people to save its fish script in this very file, and
// that script does not contain the words a hand-made setup is recognised by:
// a fish file without the marker is somebody's setup all the same.
func TestInstallLeavesAnUnmarkedFishFileAlone(t *testing.T) {
	h := shellHarness(t, "/usr/bin/fish")
	fishFile := h.homeFile(filepath.Join(".config", "fish", "completions", "kurze-url.fish"))
	const cobraScript = "# fish completion for kurze-url                     -*- shell-script -*-\n" +
		"function __kurze_url_debug\n    # ...\nend\n"
	putFile(t, fishFile, cobraScript)

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, fishFile)
	require.Equal(t, cobraScript, got)
	require.Equal(t,
		fishFile+" already sets up tab completion for kurze-url, so it was left as it is.\n",
		h.stderr.String())
}

func TestInstallBashMentionsThePackage(t *testing.T) {
	h := shellHarness(t, "/bin/bash")
	bashrc := h.homeFile(".bashrc")

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, bashrc)
	require.Equal(t, shellrc.Block(shellrc.Bash), got)
	require.Equal(t,
		"Added tab completion to "+bashrc+".\n"+
			"Open a new shell, or run `source '"+bashrc+"'`, to use it.\n"+
			"Completion in bash also needs the bash-completion package. "+
			"If it is not installed yet, install it with your system's package manager.\n",
		h.stderr.String())
}

// Terminal and iTerm open login shells, and a login bash reads
// ~/.bash_profile, not ~/.bashrc. On a Mac the block would otherwise sit in a
// file nothing reads, so install says so, and only there.
func TestInstallBashOnMacOSPointsAtBashProfile(t *testing.T) {
	h := shellHarness(t, "/bin/bash")
	h.deps.GOOS = "darwin"
	bashrc := h.homeFile(".bashrc")

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	require.True(t, strings.HasSuffix(h.stderr.String(),
		"install it with your system's package manager.\n"+
			"On macOS, Terminal and iTerm open login shells, which read ~/.bash_profile and not "+bashrc+". "+
			"If your ~/.bash_profile does not load "+bashrc+" yet, add `source '"+bashrc+"'` to it, "+
			"or the block is never read.\n"),
		h.stderr.String())
	_, ok := fileContent(t, h.homeFile(".bash_profile"))
	require.False(t, ok, "install wrote ~/.bash_profile, which it only names")

	// zsh on a Mac is no login bash.
	h = shellHarness(t, "/bin/zsh")
	h.deps.GOOS = "darwin"
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	require.NotContains(t, h.stderr.String(), "bash_profile")

	// A bash already set up gets nothing written, and so no step to take.
	h = shellHarness(t, "/bin/bash")
	h.deps.GOOS = "darwin"
	putFile(t, h.homeFile(".bashrc"), shellrc.OwnFile(shellrc.Bash))
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	require.NotContains(t, h.stderr.String(), "bash_profile")
}

func TestInstallPowerShellWritesEveryProfile(t *testing.T) {
	// $SHELL says zsh, and on Windows it is not asked.
	h := shellHarness(t, "/bin/zsh")
	h.deps.GOOS = "windows"
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.ps1"), filepath.Join(dir, "b.ps1")
	h.programs["pwsh"], h.programs["powershell"] = true, true
	h.outputs["pwsh"] = a + "\nRemoteSigned\n"
	// Windows ends its lines with CRLF.
	h.outputs["powershell"] = b + "\r\nRestricted\r\n"
	h.answer("\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	for _, profile := range []string{a, b} {
		got, _ := fileContent(t, profile)
		require.Equal(t, shellrc.Block(shellrc.PowerShell), got, profile)
	}
	require.Equal(t, [][]string{
		append([]string{"pwsh"}, psCommand...),
		append([]string{"powershell"}, psCommand...),
	}, h.ran)
	require.Equal(t,
		"Add tab completion for powershell to "+a+", "+b+"? [Y/n] "+
			"Added tab completion to "+a+".\n"+
			"Added tab completion to "+b+".\n"+
			"Open a new PowerShell window, or run `. '"+a+"'`, to use it.\n"+
			"Open a new PowerShell window, or run `. '"+b+"'`, to use it.\n"+
			"PowerShell's execution policy is Restricted, which keeps it from loading "+b+
			", so completion stays off until the policy allows the profile to run. The policy was not changed.\n",
		h.stderr.String())
	requireEmptyDir(t, h.home)
}

// Outside Windows there is only PowerShell 7, and $SHELL names it pwsh.
func TestInstallPowerShellOutsideWindowsAsksOnlyPwsh(t *testing.T) {
	h := shellHarness(t, "/usr/local/bin/pwsh")
	profile := filepath.Join(t.TempDir(), "profile.ps1")
	h.programs["pwsh"], h.programs["powershell"] = true, true
	h.outputs["pwsh"] = profile + "\nUnrestricted\n"

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, [][]string{append([]string{"pwsh"}, psCommand...)}, h.ran)
	got, _ := fileContent(t, profile)
	require.Equal(t, shellrc.Block(shellrc.PowerShell), got)
	require.NotContains(t, h.stderr.String(), "execution policy")
}

func TestInstallWithoutPowerShell(t *testing.T) {
	h := newHarness(t)
	h.deps.GOOS = "windows"

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: No PowerShell was found on the PATH.\n", h.stderr.String())
	require.Empty(t, h.ran)
}

// A PowerShell that does not say where its profile is leaves nothing to
// write, and nothing is written for the other one either.
func TestInstallStopsWhenAPowerShellCannotBeAsked(t *testing.T) {
	h := newHarness(t)
	h.deps.GOOS = "windows"
	a := filepath.Join(t.TempDir(), "a.ps1")
	h.programs["pwsh"], h.programs["powershell"] = true, true
	h.outputs["pwsh"] = a + "\nRemoteSigned\n"
	h.outputs["powershell"] = "\n"

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "kurze-url: Could not ask powershell where its profile is: ")
	_, ok := fileContent(t, a)
	require.False(t, ok, "a profile was written although the other could not be found")
}

// A PowerShell that fails is named, with the reason it gave on its stderr,
// which runCommand adds to the error, and nothing is written, for the edition
// that did answer either.
func TestCompletionNamesThePowerShellThatFailed(t *testing.T) {
	for _, sub := range []string{"install", "uninstall"} {
		t.Run(sub, func(t *testing.T) {
			h := newHarness(t)
			h.deps.GOOS = "windows"
			dir := t.TempDir()
			a := filepath.Join(dir, "a.ps1")
			if sub == "uninstall" {
				putFile(t, a, shellrc.OwnFile(shellrc.PowerShell))
			}
			before, _ := fileContent(t, a)
			h.programs["pwsh"], h.programs["powershell"] = true, true
			h.outputs["pwsh"] = a + "\nRemoteSigned\n"
			h.failures["powershell"] = errors.New("exit status 1: Get-ExecutionPolicy : Access is denied")

			code := h.run("completion", sub, "--yes")

			require.Equal(t, 1, code)
			require.Equal(t,
				"kurze-url: Could not ask powershell where its profile is: "+
					"exit status 1: Get-ExecutionPolicy : Access is denied\n",
				h.stderr.String())
			after, _ := fileContent(t, a)
			require.Equal(t, before, after, "the profile that did answer was changed")
			requireEmptyDir(t, h.home)
		})
	}
}

func TestInstallUnknownShell(t *testing.T) {
	h := shellHarness(t, "/bin/tcsh")

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: Tab completion can be set up for zsh, bash, fish, powershell, not for “tcsh”. "+
			"Name one of them: `kurze-url completion install SHELL`.\n",
		h.stderr.String())

	h.stderr.Reset()
	code = h.run("completion", "install", "tcsh", "--yes")

	require.Equal(t, 2, code)
	require.Equal(t,
		"kurze-url: Tab completion can be set up for zsh, bash, fish, powershell, not for “tcsh”. "+
			"Name one of them: `kurze-url completion install SHELL`.\n"+
			"Run 'kurze-url completion install --help' for usage.\n",
		h.stderr.String())
	requireEmptyDir(t, h.home)
}

func TestInstallWithoutShell(t *testing.T) {
	h := newHarness(t)

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: Could not tell which shell you use. "+
			"Name it: `kurze-url completion install SHELL`, where SHELL is one of zsh, bash, fish, powershell.\n",
		h.stderr.String())
}

func TestShellNamesAreReadIgnoringCase(t *testing.T) {
	// $SHELL: the base name, lower-cased, without .exe.
	h := shellHarness(t, "/opt/Tools/Bash.EXE")
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	_, ok := fileContent(t, h.homeFile(".bashrc"))
	require.True(t, ok)

	// The argument, lower-cased, and it wins over $SHELL.
	h = shellHarness(t, "/bin/bash")
	require.Equal(t, 0, h.run("completion", "install", "ZSH", "--yes"), h.stderr.String())
	_, ok = fileContent(t, h.homeFile(".zshrc"))
	require.True(t, ok)
	_, ok = fileContent(t, h.homeFile(".bashrc"))
	require.False(t, ok)
}

// Setting up completion needs no config, the same as printing a script, so a
// config file that does not load must not stop it.
func TestInstallWorksWithABrokenConfig(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	h.writeConfig("language: [\n")

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, h.homeFile(".zshrc"))
	require.Equal(t, shellrc.Block(shellrc.Zsh), got)
}

// The file system's own words are English; the sentence around them is in
// the person's language.
func TestInstallFailureIsInThePersonsLanguage(t *testing.T) {
	h := shellHarness(t, "/usr/bin/fish")
	h.env["LANG"] = "de_DE.UTF-8"
	notADir := filepath.Join(t.TempDir(), "file")
	putFile(t, notADir, "")
	h.env["XDG_CONFIG_HOME"] = notADir

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.True(t, strings.HasPrefix(h.stderr.String(),
		"kurze-url: "+filepath.Join(notADir, "fish", "completions", "kurze-url.fish")+
			" konnte nicht gelesen werden: "),
		h.stderr.String())
}

func TestInstallWriteFailureNamesTheFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	require.NoError(t, os.Chmod(h.home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(h.home, 0o700) })

	code := h.run("completion", "install", "--yes")

	require.Equal(t, 1, code)
	require.True(t, strings.HasPrefix(h.stderr.String(), "kurze-url: Could not write "+zshrc+": "), h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\n", got)
}

// ---- while the question waits ----

// editingStdin is a person who, while the question waits, changes the file in
// another window (or a dotfile sync does) and then answers.
type editingStdin struct {
	edit   func()
	answer *strings.Reader
	edited bool
}

func (r *editingStdin) Read(p []byte) (int, error) {
	if !r.edited {
		r.edited = true
		r.edit()
	}
	return r.answer.Read(p)
}

// answerAfter puts a person on stdin's terminal who runs edit and then types
// text.
func (h *harness) answerAfter(edit func(), text string) {
	h.stdinTTY = true
	h.deps.Stdin = &editingStdin{edit: edit, answer: strings.NewReader(text)}
}

// appendTo adds text at the end of a file, as an editor would.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestInstallKeepsAnEditMadeWhileItAsked(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	h.answerAfter(func() { appendTo(t, zshrc, "export B=2\n") }, "y\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\nexport B=2\n"+shellrc.Block(shellrc.Zsh), got)
}

// What the file holds after the answer decides, and is what is reported.
func TestInstallActsOnWhatTheFileHoldsAfterTheAnswer(t *testing.T) {
	const handMade = "source <(kurze-url completion zsh)\n"
	cases := []struct {
		name, added, want, message string
	}{
		{
			name:    "set up by hand meanwhile",
			added:   handMade,
			want:    "export A=1\n" + handMade,
			message: " already sets up tab completion for kurze-url, so it was left as it is.\n",
		},
		{
			name:    "installed meanwhile",
			added:   shellrc.Marker + "\n" + shellrc.Command(shellrc.Zsh) + "\n",
			want:    "export A=1\n" + shellrc.Marker + "\n" + shellrc.Command(shellrc.Zsh) + "\n",
			message: ".\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := shellHarness(t, "/bin/zsh")
			zshrc := h.homeFile(".zshrc")
			putFile(t, zshrc, "export A=1\n")
			h.answerAfter(func() { appendTo(t, zshrc, tc.added) }, "y\n")

			code := h.run("completion", "install")

			require.Equal(t, 0, code, h.stderr.String())
			got, _ := fileContent(t, zshrc)
			require.Equal(t, tc.want, got)
			require.True(t, strings.HasSuffix(h.stderr.String(), zshrc+tc.message), h.stderr.String())
			require.NotContains(t, h.stderr.String(), "Added tab completion")
		})
	}
}

func TestUninstallKeepsAnEditMadeWhileItAsked(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	h.answerAfter(func() { appendTo(t, zshrc, "export C=3\n") }, "\n")

	code := h.run("completion", "uninstall")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\nexport C=3\n", got)
}

func TestUninstallWhenTheBlockWentWhileItAsked(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	h.stderr.Reset()
	h.answerAfter(func() { putFile(t, zshrc, "export A=1\nexport D=4\n") }, "\n")

	code := h.run("completion", "uninstall")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\nexport D=4\n", got)
	require.True(t, strings.HasSuffix(h.stderr.String(),
		zshrc+" no longer holds the tab completion, so there was nothing to remove from it.\n"),
		h.stderr.String())
}

// ---- PowerShell profiles a change would damage ----

// utf16LE is text as Windows PowerShell 5.1's Out-File and old Notepad save
// it: UTF-16, little-endian, with its byte-order mark.
func utf16LE(text string) string {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(text)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return string(out)
}

// utf16BE is the same, big-endian.
func utf16BE(text string) string {
	out := []byte{0xFE, 0xFF}
	for _, u := range utf16.Encode([]rune(text)) {
		out = append(out, byte(u>>8), byte(u))
	}
	return string(out)
}

// signedProfile is a profile as Set-AuthenticodeSignature leaves it.
const signedProfile = "Set-Alias ll Get-ChildItem\r\n\r\n" +
	"# SIG # Begin signature block\r\n# MIIFuQYJKoZIhvcNAQcCoIIFqjCCBaYCAQExCzAJBgUrDgMCGgUAMGkGCisGAQQB\r\n" +
	"# SIG # End signature block\r\n"

// unsafeProfiles are the three kinds of profile install must not change.
var unsafeProfiles = []struct{ name, content string }{
	{"UTF-16 little-endian", utf16LE("Set-Alias ll Get-ChildItem\r\n")},
	{"UTF-16 big-endian", utf16BE("Set-Alias ll Get-ChildItem\r\n")},
	// UTF-16 without its byte-order mark shows as NUL bytes.
	{"NUL bytes", "S\x00e\x00t\x00-\x00A\x00l\x00i\x00a\x00s\x00"},
	{"signed", signedProfile},
}

// pwshHarness is a harness on Linux whose one PowerShell reports profile.
func pwshHarness(t *testing.T, profile string) *harness {
	t.Helper()
	h := shellHarness(t, "/usr/bin/pwsh")
	h.programs["pwsh"] = true
	h.outputs["pwsh"] = profile + "\nRemoteSigned\n"
	return h
}

func TestInstallRefusesAProfileAChangeWouldDamage(t *testing.T) {
	for _, tc := range unsafeProfiles {
		t.Run(tc.name, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "profile.ps1")
			putFile(t, profile, tc.content)
			h := pwshHarness(t, profile)
			h.answer("y\n")

			code := h.run("completion", "install")

			// It was the only profile, so completion is on nowhere: exit 1.
			require.Equal(t, 1, code)
			got, _ := fileContent(t, profile)
			require.Equal(t, tc.content, got)
			require.Equal(t,
				"kurze-url: "+profile+" is saved as UTF-16 or signed, and changing it from here "+
					"would corrupt it or break its signature, so it was left as it is.\n",
				h.stderr.String())
		})
	}
}

// One profile refused does not stop the other edition's.
func TestInstallWritesTheOtherProfileWhenOneIsRefused(t *testing.T) {
	h := newHarness(t)
	h.deps.GOOS = "windows"
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.ps1"), filepath.Join(dir, "b.ps1")
	putFile(t, b, signedProfile)
	h.programs["pwsh"], h.programs["powershell"] = true, true
	h.outputs["pwsh"] = a + "\nRemoteSigned\n"
	h.outputs["powershell"] = b + "\nAllSigned\n"
	h.answer("\n")

	code := h.run("completion", "install")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, a)
	require.Equal(t, shellrc.Block(shellrc.PowerShell), got)
	got, _ = fileContent(t, b)
	require.Equal(t, signedProfile, got)
	require.True(t, strings.HasPrefix(h.stderr.String(),
		b+" is saved as UTF-16 or signed, and changing it from here would corrupt it or break its signature, "+
			"so it was left as it is.\n"+
			"Add tab completion for powershell to "+a+"? [Y/n] "),
		h.stderr.String())
}

// Taking the block out of a signed profile breaks its signature as surely as
// putting it in.
func TestUninstallLeavesASignedProfileAlone(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile.ps1")
	content := shellrc.OwnFile(shellrc.PowerShell) + signedProfile
	putFile(t, profile, content)
	h := pwshHarness(t, profile)

	code := h.run("completion", "uninstall", "--yes")

	require.Equal(t, 1, code)
	got, _ := fileContent(t, profile)
	require.Equal(t, content, got)
	require.Contains(t, h.stderr.String(), "kurze-url: "+profile+" is saved as UTF-16 or signed")
}

// The path is opened as PowerShell wrote it, so anything that is not a clean
// UTF-8 path is refused before a file is touched.
func TestInstallRefusesAProfilePathThatIsNotClean(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, path, shown string }{
		// A legacy code page's ü (0xFC) is not UTF-8.
		{"not UTF-8", filepath.Join(dir, "J\xfcrgen.ps1"), `\xfc`},
		{"byte-order mark", "\uFEFF" + filepath.Join(dir, "a.ps1"), `\ufeff`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := pwshHarness(t, tc.path)

			code := h.run("completion", "install", "--yes")

			require.Equal(t, 1, code)
			require.True(t, strings.HasPrefix(h.stderr.String(),
				"kurze-url: Could not ask pwsh where its profile is: "), h.stderr.String())
			require.Contains(t, h.stderr.String(), tc.shown)
			requireEmptyDir(t, dir)
		})
	}
}

// ---- uninstall ----

func TestUninstallRestoresTheFile(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1")
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	h.stderr.Reset()

	code := h.run("completion", "uninstall", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1", got)
	require.Equal(t, "Removed tab completion from "+zshrc+".\n", h.stderr.String())
}

// Review Focus 3: install run twice and uninstall run once leave no block and
// nothing else changed, here on a file whose last line has no newline, which
// is the one the block has to end before it starts.
func TestInstallTwiceThenUninstallOnceRestoresTheFile(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			h := shellHarness(t, "/bin/"+shell)
			file := h.homeFile("." + shell + "rc")
			if shell == "powershell" {
				file = filepath.Join(t.TempDir(), "profile.ps1")
				h.programs["pwsh"] = true
				h.outputs["pwsh"] = file + "\nRemoteSigned\n"
			}
			const original = "export A=1"
			putFile(t, file, original)

			require.Equal(t, 0, h.run("completion", "install", shell, "--yes"), h.stderr.String())
			require.Equal(t, 0, h.run("completion", "install", shell, "--yes"), h.stderr.String())
			got, _ := fileContent(t, file)
			require.Equal(t, 1, strings.Count(got, shellrc.Marker), "the second install added a block")

			require.Equal(t, 0, h.run("completion", "uninstall", shell, "--yes"), h.stderr.String())

			got, _ = fileContent(t, file)
			require.Equal(t, original, got)
		})
	}
}

func TestUninstallAsksFirst(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	installed, _ := fileContent(t, zshrc)

	// Nobody to ask.
	h.stderr.Reset()
	require.Equal(t, 1, h.run("completion", "uninstall"))
	require.Contains(t, h.stderr.String(), "Add --yes to go ahead without asking.")

	// A no.
	h.stderr.Reset()
	h.answer("n\n")
	require.Equal(t, 0, h.run("completion", "uninstall"))
	require.Equal(t, "Remove tab completion from "+zshrc+"? [Y/n] Nothing was changed.\n", h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, installed, got)

	// A yes.
	h.stderr.Reset()
	h.answer("\n")
	require.Equal(t, 0, h.run("completion", "uninstall"))
	got, _ = fileContent(t, zshrc)
	require.Empty(t, got)
}

func TestUninstallFishDeletesItsFile(t *testing.T) {
	h := shellHarness(t, "/usr/bin/fish")
	fishFile := h.homeFile(filepath.Join(".config", "fish", "completions", "kurze-url.fish"))
	require.Equal(t, 0, h.run("completion", "install", "--yes"), h.stderr.String())
	h.stderr.Reset()

	code := h.run("completion", "uninstall", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	_, ok := fileContent(t, fishFile)
	require.False(t, ok, "the fish file is still there")
	require.Equal(t, "Removed tab completion from "+fishFile+".\n", h.stderr.String())

	// One without the marker is somebody else's.
	const cobraScript = "# fish completion for kurze-url\n"
	putFile(t, fishFile, cobraScript)
	h.stderr.Reset()

	code = h.run("completion", "uninstall", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	got, _ := fileContent(t, fishFile)
	require.Equal(t, cobraScript, got)
	require.Equal(t,
		fishFile+" sets up tab completion for kurze-url without the marker `completion install` adds, "+
			"so it was left as it is. Remove that setup by hand if you no longer want it.\n"+
			"There is no tab completion set up by `completion install` in "+fishFile+" to remove. "+
			"For another shell, run `kurze-url completion uninstall SHELL`.\n",
		h.stderr.String())
}

func TestUninstallWithNothingToRemove(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")
	zshrc := h.homeFile(".zshrc")
	putFile(t, zshrc, "export A=1\n")
	h.answer("\n")

	code := h.run("completion", "uninstall")

	require.Equal(t, 0, code, h.stderr.String())
	// It names where it looked, because $SHELL chose the file and the
	// person may have installed for another shell.
	require.Equal(t,
		"There is no tab completion set up by `completion install` in "+zshrc+" to remove. "+
			"For another shell, run `kurze-url completion uninstall SHELL`.\n",
		h.stderr.String())
	got, _ := fileContent(t, zshrc)
	require.Equal(t, "export A=1\n", got)

	// A file that is not there has nothing in it either, and stays away.
	h = shellHarness(t, "/bin/zsh")
	require.Equal(t, 0, h.run("completion", "uninstall", "--yes"))
	requireEmptyDir(t, h.home)
}

// Every profile it asked about is named, in both languages.
func TestUninstallWithNothingToRemoveNamesEveryProfile(t *testing.T) {
	h := newHarness(t)
	h.env["LANG"] = "de_DE.UTF-8"
	h.deps.GOOS = "windows"
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.ps1"), filepath.Join(dir, "b.ps1")
	h.programs["pwsh"], h.programs["powershell"] = true, true
	h.outputs["pwsh"] = a + "\nRemoteSigned\n"
	h.outputs["powershell"] = b + "\nRemoteSigned\n"

	code := h.run("completion", "uninstall", "--yes")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"In "+a+", "+b+" gibt es keine von `completion install` eingerichtete Tab-Vervollständigung, "+
			"die sich entfernen ließe. Für eine andere Shell führe `kurze-url completion uninstall SHELL` aus.\n",
		h.stderr.String())
	requireEmptyDir(t, dir)
}

// ---- the shell argument ----

func TestCompletionShellArgumentCompletes(t *testing.T) {
	h := newHarness(t)
	for _, sub := range []string{"install", "uninstall"} {
		got, directive := h.complete("completion", sub, "")
		require.Equal(t, []string{"zsh", "bash", "fish", "powershell"}, got, sub)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, sub)

		got, _ = h.complete("completion", sub, "P")
		require.Equal(t, []string{"powershell"}, got, sub)

		got, directive = h.complete("completion", sub, "zsh", "")
		require.Empty(t, got, sub)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, sub)
	}
}

func TestCompletionInstallTakesOneShell(t *testing.T) {
	h := shellHarness(t, "/bin/zsh")

	code := h.run("completion", "install", "zsh", "bash")

	require.Equal(t, 2, code)
	requireEmptyDir(t, h.home)
}
