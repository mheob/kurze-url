package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/shellrc"
)

// shellNames are the shells completion can be set up for, as the argument
// names them. Tab offers them and a refused shell lists them. pwsh is
// accepted for powershell but not offered, because it is the same shell under
// PowerShell 7's own name.
var shellNames = []string{"zsh", "bash", "fish", "powershell"}

// rcFileMode is the mode of a shell configuration file install creates,
// before the umask takes its share. An existing file keeps its own.
const rcFileMode os.FileMode = 0o644

// psProfileQuery is what install asks each PowerShell: the profile it loads
// for this user in this host, and the execution policy that decides whether
// it loads it at all. One process answers both, one per line.
const psProfileQuery = "$PROFILE.CurrentUserCurrentHost; Get-ExecutionPolicy"

// newCompletionInstallCmd writes the completion block into the person's
// shell configuration, after asking them.
func newCompletionInstallCmd(d *Deps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "install [zsh|bash|fish|powershell|pwsh]",
		Short: "Turn on tab completion in your shell's configuration",
		Long: "Adds two lines to your shell's configuration that load kurze-url's tab completion\n" +
			"whenever a shell starts: to ~/.zshrc (or $ZDOTDIR/.zshrc), to ~/.bashrc, to\n" +
			"fish's completions directory as a file of its own, or to every PowerShell profile.\n" +
			"The completion is generated at shell start, so an update of the CLI brings its own.\n\n" +
			"The shell is the argument, else the one $SHELL names, and PowerShell on Windows.\n" +
			"It asks before changing anything; --yes skips the question, and without a terminal\n" +
			"to ask on it is required. Running it twice adds nothing twice, and a setup made by\n" +
			"hand is left alone. `kurze-url completion uninstall` takes the lines out again.",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeShellName,
		RunE: func(cmd *cobra.Command, args []string) error {
			return completionApp(d).installCompletion(cmd, args, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "change the configuration without asking first")
	return cmd
}

// newCompletionUninstallCmd takes the completion block out again.
func newCompletionUninstallCmd(d *Deps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall [zsh|bash|fish|powershell|pwsh]",
		Short: "Remove what `completion install` added",
		Long: "Removes the lines `kurze-url completion install` added to your shell's configuration,\n" +
			"or deletes fish's completion file it wrote, and leaves every other line as it was.\n" +
			"A setup made by hand is left alone.\n\n" +
			"The shell is the argument, else the one $SHELL names, and PowerShell on Windows.\n" +
			"It asks before changing anything; --yes skips the question, and without a terminal\n" +
			"to ask on it is required.",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeShellName,
		RunE: func(cmd *cobra.Command, args []string) error {
			return completionApp(d).uninstallCompletion(cmd, args, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "change the configuration without asking first")
	return cmd
}

// completeShellName offers the shells for the one argument, and nothing
// after it.
func completeShellName(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return noValues(cmd, args, toComplete)
	}
	return withPrefix(shellNames, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completionApp is the App the two commands work with: the person's language
// and the Deps, and nothing else. It is not newApp's, because setting up
// completion needs neither the service nor the config, and a config file that
// does not load or a bad environment override must not stand in its way, for
// the reason `completion zsh` never builds an App. catalogFor forgives such a
// file and falls back to the environment's language. Config, Store, Session
// and API stay nil, and nothing here reaches for them.
func completionApp(d *Deps) *App {
	return &App{Cat: catalogFor(d), d: d}
}

// completionTarget is one file install writes the block into and uninstall
// takes it out of.
type completionTarget struct {
	// path is the file, for a profile as PowerShell reported it.
	path string
	// own says the whole file is ours, which is fish's completions file:
	// install writes all of it and uninstall deletes it.
	own bool
	// policy is the execution policy PowerShell reported with a profile, and
	// "" for every other file.
	policy string

	// content is what the file holds, empty when it does not exist.
	content []byte
	// state is what the file holds of a completion setup.
	state shellrc.State
}

// installCompletion is `completion install`: it finds the files, says what is
// already there, asks, writes what is missing and says how to turn it on.
//
// Nothing is written before the question is answered, and only the files
// whose setup is missing are asked about or touched: a file that already has
// the block is reported and left, so a second install adds nothing, and a
// file somebody set up by hand is reported and left, because adding ours
// would load completion twice.
func (a *App) installCompletion(cmd *cobra.Command, args []string, yes bool) error {
	shell, targets, err := a.completionTargets(cmd, args)
	if err != nil {
		return err
	}

	var pending []completionTarget
	var done []string
	for _, t := range targets {
		switch t.state {
		case shellrc.Installed:
			done = append(done, t.path)
		case shellrc.ByHand:
			a.notice(a.Cat.T("completion.byHand", i18n.Args{"file": t.path}))
		default:
			pending = append(pending, t)
		}
	}
	if len(done) > 0 {
		a.notice(a.Cat.T("completion.alreadyInstalled", i18n.Args{"files": joinPaths(done)}))
	}
	if len(pending) == 0 {
		return nil
	}

	ok, err := a.confirm(yes, a.Cat.T("completion.confirmInstall", i18n.Args{
		"shell": string(shell),
		"files": joinPaths(pathsOf(pending)),
	}))
	if err != nil || !ok {
		return err
	}

	// A failure stops at the file it happened on. The files before it are
	// written and reported, so the person knows which ones have the block.
	for _, t := range pending {
		var data []byte
		if t.own {
			data = []byte(shellrc.OwnFile(shell))
		} else {
			data = shellrc.Add(t.content, shell)
		}
		if err := shellrc.WriteFile(t.path, data, rcFileMode); err != nil {
			return a.fileFailure(t.path, err, false)
		}
		a.notice(a.Cat.T("completion.installed", i18n.Args{"file": t.path}))
	}
	a.nextSteps(shell, pending)
	return nil
}

// nextSteps says how to turn on what install has just written, which a shell
// that is already running has not read.
func (a *App) nextSteps(shell shellrc.Shell, written []completionTarget) {
	switch shell {
	case shellrc.Zsh, shellrc.Bash:
		for _, t := range written {
			a.notice(a.Cat.T("completion.reloadSource", i18n.Args{"file": t.path}))
		}
	case shellrc.Fish:
		// fish reads its completions directory when a command is first
		// completed, so a new shell is all it takes.
		a.notice(a.Cat.T("completion.reloadNewShell"))
	case shellrc.PowerShell:
		for _, t := range written {
			a.notice(a.Cat.T("completion.reloadProfile", i18n.Args{"file": t.path}))
		}
	}

	if shell == shellrc.Bash {
		// Cobra's bash script needs bash-completion, which macOS and some
		// minimal Linux images do not have. Cobra's own help says the same.
		a.notice(a.Cat.T("completion.bashPackage"))
	}
	// These two policies keep PowerShell from loading an unsigned profile,
	// so the block is there and does nothing. The policy is the person's,
	// or their organisation's, to change.
	for _, t := range written {
		if strings.EqualFold(t.policy, "Restricted") || strings.EqualFold(t.policy, "AllSigned") {
			a.notice(a.Cat.T("completion.policyBlocks", i18n.Args{"file": t.path, "policy": t.policy}))
		}
	}
}

// uninstallCompletion is `completion uninstall`: it removes exactly the block
// install wrote, or deletes fish's file when it carries the marker, after
// asking. A setup without the marker is somebody's own and stays.
func (a *App) uninstallCompletion(cmd *cobra.Command, args []string, yes bool) error {
	_, targets, err := a.completionTargets(cmd, args)
	if err != nil {
		return err
	}

	var marked []completionTarget
	for _, t := range targets {
		switch t.state {
		case shellrc.Installed:
			marked = append(marked, t)
		case shellrc.ByHand:
			a.notice(a.Cat.T("completion.byHandLeft", i18n.Args{"file": t.path}))
		}
	}
	if len(marked) == 0 {
		a.notice(a.Cat.T("completion.nothingToRemove"))
		return nil
	}

	ok, err := a.confirm(yes, a.Cat.T("completion.confirmUninstall", i18n.Args{
		"files": joinPaths(pathsOf(marked)),
	}))
	if err != nil || !ok {
		return err
	}

	for _, t := range marked {
		if t.own {
			if err := os.Remove(t.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return a.fileFailure(t.path, err, true)
			}
		} else {
			// Installed means the marker is there, which is all Remove
			// needs to find something to take out.
			data, _ := shellrc.Remove(t.content)
			if err := shellrc.WriteFile(t.path, data, rcFileMode); err != nil {
				return a.fileFailure(t.path, err, false)
			}
		}
		a.notice(a.Cat.T("completion.removed", i18n.Args{"file": t.path}))
	}
	return nil
}

// confirm asks the person, unless --yes already said yes. Without a terminal
// on stdin nobody can answer, so it refuses and names --yes rather than read
// whatever a pipe holds.
//
// The question waits on its own line, printed without a newline. Enter, y,
// yes, j and ja are a yes in either language and in any case, and anything
// else is a no. So is an input that ends before anything was typed (Ctrl+D):
// no answer is not a yes. A last line without its newline is still an answer.
func (a *App) confirm(yes bool, question string) (bool, error) {
	if yes {
		return true, nil
	}
	if !a.d.StdinTTY() {
		return false, errors.New(a.Cat.T("completion.needsYes"))
	}

	_, _ = fmt.Fprint(a.d.Stderr, question)
	line, err := bufio.NewReader(a.d.Stdin).ReadString('\n')
	if err != nil {
		// Leave the question's line, so what follows starts on its own.
		_, _ = fmt.Fprintln(a.d.Stderr)
		if line == "" {
			a.notice(a.Cat.T("completion.declined"))
			return false, nil
		}
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes", "j", "ja":
		return true, nil
	}
	a.notice(a.Cat.T("completion.declined"))
	return false, nil
}

// completionTargets settles the shell, finds its files and reads them. A file
// that does not exist reads as empty, which is a file with no setup in it.
func (a *App) completionTargets(cmd *cobra.Command, args []string) (shellrc.Shell, []completionTarget, error) {
	shell, err := a.completionShell(cmd, args)
	if err != nil {
		return "", nil, err
	}
	targets, err := a.targetsFor(cmd.Context(), shell)
	if err != nil {
		return "", nil, err
	}
	for i := range targets {
		t := &targets[i]
		data, err := os.ReadFile(t.path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", nil, errors.New(a.Cat.T("completion.readFailed", i18n.Args{
				"file":   t.path,
				"reason": describe(err, a.Cat),
			}))
		}
		t.content = data
		t.state = shellrc.Inspect(data)
		// Cobra's own help tells people to save its fish script in this
		// very file, and that script does not contain the words Inspect
		// recognises a hand-made setup by. In a file that is ours alone,
		// anything without the marker is somebody's setup all the same.
		if t.own && exists && t.state != shellrc.Installed {
			t.state = shellrc.ByHand
		}
	}
	return shell, targets, nil
}

// completionShell is the shell to set up: the argument when there is one,
// PowerShell on Windows, and else the one $SHELL names.
//
// Both names are read ignoring case, and $SHELL by its base name without
// ".exe". A wrong argument is a usage error, so the person gets the hint to
// the command's help; a $SHELL that names another shell, or nothing, is not a
// mistake in the command, and is a plain failure.
func (a *App) completionShell(cmd *cobra.Command, args []string) (shellrc.Shell, error) {
	usage := i18n.Args{"command": cmd.CommandPath(), "shells": strings.Join(shellNames, ", ")}
	if len(args) == 1 {
		shell, ok := shellrc.ParseShell(strings.ToLower(args[0]))
		if !ok {
			text := a.Cat.T("completion.unknownShell", usage, i18n.Args{"shell": args[0]})
			return "", &UsageError{Err: errors.New(text)}
		}
		return shell, nil
	}
	if a.d.GOOS == "windows" {
		return shellrc.PowerShell, nil
	}

	value := a.d.Getenv("SHELL")
	if value == "" {
		return "", errors.New(a.Cat.T("completion.noShell", usage))
	}
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(value)), ".exe")
	shell, ok := shellrc.ParseShell(name)
	if !ok {
		return "", errors.New(a.Cat.T("completion.unknownShell", usage, i18n.Args{"shell": name}))
	}
	return shell, nil
}

// targetsFor names the files a shell reads its completion setup from, where
// that shell itself looks for them.
func (a *App) targetsFor(ctx context.Context, shell shellrc.Shell) ([]completionTarget, error) {
	switch shell {
	case shellrc.Zsh:
		dir, err := a.dirOr("ZDOTDIR")
		if err != nil {
			return nil, err
		}
		return []completionTarget{{path: filepath.Join(dir, ".zshrc")}}, nil
	case shellrc.Bash:
		home, err := a.home()
		if err != nil {
			return nil, err
		}
		return []completionTarget{{path: filepath.Join(home, ".bashrc")}}, nil
	case shellrc.Fish:
		dir, err := a.dirOr("XDG_CONFIG_HOME", ".config")
		if err != nil {
			return nil, err
		}
		return []completionTarget{{
			path: filepath.Join(dir, "fish", "completions", programName+".fish"),
			own:  true,
		}}, nil
	}
	// PowerShell, the one shell ParseShell returns that is left.
	return a.powerShellProfiles(ctx)
}

// dirOr is the directory an environment variable names, or else the home
// directory joined with under, as the shells themselves resolve ZDOTDIR and
// XDG_CONFIG_HOME. The home directory is looked up only when it is needed.
func (a *App) dirOr(variable string, under ...string) (string, error) {
	if dir := a.d.Getenv(variable); dir != "" {
		return dir, nil
	}
	home, err := a.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, under...)...), nil
}

// home is the person's home directory, with a failure in their language.
func (a *App) home() (string, error) {
	home, err := a.d.HomeDir()
	if err != nil {
		return "", errors.New(a.Cat.T("completion.noHome", i18n.Args{"reason": describe(err, a.Cat)}))
	}
	return home, nil
}

// powerShellProfiles asks every PowerShell on the PATH for its profile:
// PowerShell 7 (pwsh) everywhere and Windows PowerShell 5.1 (powershell) on
// Windows. The two keep separate profiles, and install cannot tell which one
// the person uses, so it sets up both.
//
// The path comes from PowerShell and is never assembled here: Documents is
// often redirected (OneDrive) and the profile's name depends on the host.
// A PowerShell that is on the PATH and does not answer stops the command
// before anything is written, because writing only the profiles that did
// answer would leave a setup that works in one edition and silently not in
// the other.
func (a *App) powerShellProfiles(ctx context.Context) ([]completionTarget, error) {
	programs := []string{"pwsh"}
	if a.d.GOOS == "windows" {
		programs = append(programs, "powershell")
	}

	var targets []completionTarget
	seen := map[string]bool{}
	for _, program := range programs {
		if _, err := a.d.LookPath(program); err != nil {
			continue
		}
		out, err := a.d.RunCommand(ctx, program, "-NoProfile", "-NonInteractive", "-Command", psProfileQuery)
		if err != nil {
			return nil, a.profileFailure(program, describe(err, a.Cat))
		}
		// Windows ends each line with CRLF, which TrimSpace takes off.
		lines := strings.Split(string(out), "\n")
		profile := strings.TrimSpace(lines[0])
		if !filepath.IsAbs(profile) {
			return nil, a.profileFailure(program, fmt.Sprintf("%q", profile))
		}
		policy := ""
		if len(lines) > 1 {
			policy = strings.TrimSpace(lines[1])
		}
		// Two editions that report one profile share it, and the file is
		// asked about and written once.
		if seen[profile] {
			continue
		}
		seen[profile] = true
		targets = append(targets, completionTarget{path: profile, policy: policy})
	}
	if len(targets) == 0 {
		return nil, errors.New(a.Cat.T("completion.noPowerShell"))
	}
	return targets, nil
}

// profileFailure is the failure for a PowerShell that did not say where its
// profile is. reason is what it answered instead, or why it did not.
func (a *App) profileFailure(program, reason string) error {
	return errors.New(a.Cat.T("completion.profileFailed", i18n.Args{"program": program, "reason": reason}))
}

// fileFailure is the failure for a file that could not be written or
// deleted, in the person's language. The cause is the file system's own
// words, which are English, after it: the person needs them to fix it.
func (a *App) fileFailure(path string, err error, deleting bool) error {
	args := i18n.Args{"file": path, "reason": describe(err, a.Cat)}
	if deleting {
		return errors.New(a.Cat.T("completion.removeFailed", args))
	}
	return errors.New(a.Cat.T("completion.writeFailed", args))
}

// pathsOf returns the targets' paths, in order.
func pathsOf(targets []completionTarget) []string {
	paths := make([]string, len(targets))
	for i, t := range targets {
		paths[i] = t.path
	}
	return paths
}

// joinPaths is how a message lists several files.
func joinPaths(paths []string) string {
	return strings.Join(paths, ", ")
}
