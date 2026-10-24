package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/shellrc"
)

// shellNames are the shells completion can be set up for, as the argument
// names them. Tab offers them and a refused shell lists them. pwsh is
// accepted for powershell but not offered, because it is the same shell under
// PowerShell 7's own name.
var shellNames = []string{"zsh", "bash", "fish", "powershell"}

// rcFileMode is the mode of a shell configuration file install creates.
// shellrc.WriteFile sets it exactly, whatever the umask says, and an existing
// file keeps its own.
const rcFileMode os.FileMode = 0o644

// psProfileQuery is what install asks each PowerShell: the profile it loads
// for this user in every host, and the execution policy that decides whether
// it loads it at all. One process answers both, one per line.
//
// The all-hosts profile and not $PROFILE itself, which is the current host's:
// VS Code's PowerShell terminal and the ISE are hosts of their own, each with
// a profile of its own, and the console's would leave completion off in them.
// Every host loads the all-hosts profile as well as its own.
//
// It first switches the console's output to UTF-8, because Windows
// PowerShell otherwise writes in the system's legacy code page, and a profile
// under a user name with an umlaut would arrive as bytes that name another
// file. The encoder is the one without a byte-order mark: the shorter
// [Text.Encoding]::UTF8 carries one, which Windows PowerShell can write ahead
// of redirected output, and a path that starts with one is refused.
const psProfileQuery = "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); " +
	"$PROFILE.CurrentUserAllHosts; Get-ExecutionPolicy"

// newCompletionInstallCmd writes the completion block into the person's
// shell configuration, after asking them.
func newCompletionInstallCmd(d *Deps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "install [zsh|bash|fish|powershell|pwsh]",
		Short: "Turn on tab completion in your shell's configuration",
		Long: "Adds two lines to your shell's configuration that load kurze-url's tab completion\n" +
			"whenever a shell starts: to ~/.zshrc (or $ZDOTDIR/.zshrc), to ~/.bashrc, to\n" +
			"fish's completions directory as a file of its own, or to the profile every host of\n" +
			"each PowerShell on the PATH loads. The completion is generated at shell start, so\n" +
			"an update of the CLI brings its own.\n\n" +
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
	// profile says the file is a PowerShell profile, which may be saved as
	// UTF-16 or signed, and policy is the execution policy PowerShell
	// reported with it.
	profile bool
	policy  string

	// content is what the file holds, empty when it does not exist.
	content []byte
	// state is what the file holds of a completion setup.
	state shellrc.State
	// unsafe says a change from here would damage the file (see
	// unsafeProfile).
	unsafe bool
}

// signatureBlock opens the signature PowerShell appends to a signed script.
const signatureBlock = "# SIG # Begin signature block"

// unsafeProfile reports whether changing a PowerShell profile from here would
// damage it. A profile Windows PowerShell or Notepad saved as UTF-16 starts
// with its byte-order mark, or holds NUL bytes when it has none, and the
// block's UTF-8 bytes in it would be garbage that PowerShell refuses to run.
// A signed profile is checked against its signature, so any change, even one
// that only adds a line, invalidates it and the profile stops loading under
// AllSigned.
func unsafeProfile(content []byte) bool {
	return bytes.HasPrefix(content, []byte{0xFF, 0xFE}) ||
		bytes.HasPrefix(content, []byte{0xFE, 0xFF}) ||
		bytes.IndexByte(content, 0) >= 0 ||
		bytes.Contains(content, []byte(signatureBlock))
}

// installCompletion is `completion install`: it finds the files, says what is
// already there, asks, writes what is missing and says how to turn it on.
//
// Nothing is written before the question is answered, and only the files
// whose setup is missing are asked about or touched: a file that already has
// the block is reported and left, so a second install adds nothing, a file
// somebody set up by hand is reported and left, because adding ours would
// load completion twice, and a profile a change would damage is refused.
//
// The question can wait for as long as the person takes, and it names the
// files, which invites them to look, or a dotfile sync may change one in the
// meantime. So after the yes, every file it named is read again and planned
// again from what is on disk now: an edit made meanwhile is kept, and a file
// that has changed state is reported as it is now and left. A file the
// question did not name is never written, whatever happened to it.
func (a *App) installCompletion(cmd *cobra.Command, args []string, yes bool) error {
	shell, targets, err := a.completionTargets(cmd, args)
	if err != nil {
		return err
	}

	plan := a.planInstall(targets)
	if err := a.refuseProfiles(plan.refused, len(plan.pending) == 0 && !plan.settled); err != nil {
		return err
	}
	if len(plan.pending) == 0 {
		return nil
	}

	ok, err := a.confirm(yes, a.Cat.T("completion.confirmInstall", i18n.Args{
		"shell": string(shell),
		"files": joinPaths(pathsOf(plan.pending)),
	}))
	if err != nil || !ok {
		return err
	}

	fresh, err := a.reread(plan.pending)
	if err != nil {
		return err
	}
	again := a.planInstall(fresh)

	// A failure stops at the file it happened on. The files before it are
	// written and reported, so the person knows which ones have the block.
	var written []completionTarget
	for _, t := range again.pending {
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
		written = append(written, t)
	}
	a.nextSteps(shell, written)
	return a.refuseProfiles(again.refused, len(written) == 0 && !plan.settled && !again.settled)
}

// installPlan is what install makes of the files' content.
type installPlan struct {
	// pending are the files to write the block into.
	pending []completionTarget
	// refused are the profiles a change would damage.
	refused []string
	// settled says some file already turns completion on, through our
	// block or a setup made by hand.
	settled bool
}

// planInstall sorts the targets by what they hold and reports the files that
// are left as they are: the ones that have the block and the ones set up by
// hand. A file that has a setup is judged by it before its encoding, because
// it is left alone either way.
func (a *App) planInstall(targets []completionTarget) installPlan {
	var plan installPlan
	var done []string
	for _, t := range targets {
		switch {
		case t.state == shellrc.Installed:
			done = append(done, t.path)
		case t.state == shellrc.ByHand:
			a.notice(a.Cat.T("completion.byHand", i18n.Args{"file": t.path}))
			plan.settled = true
		case t.unsafe:
			plan.refused = append(plan.refused, t.path)
		default:
			plan.pending = append(plan.pending, t)
		}
	}
	if len(done) > 0 {
		a.notice(a.Cat.T("completion.alreadyInstalled", i18n.Args{"files": joinPaths(done)}))
		plan.settled = true
	}
	return plan
}

// refuseProfiles reports the profiles left alone because a change would
// damage them. When fatal, they were all there was to do and completion is
// not on anywhere, so the last refusal is the command's failure and the run
// exits 1: a script that ran it must not read success, and the person has to
// do something about the file before completion can work.
func (a *App) refuseProfiles(paths []string, fatal bool) error {
	for i, path := range paths {
		text := a.Cat.T("completion.profileUnsafe", i18n.Args{"file": path})
		if fatal && i == len(paths)-1 {
			return errors.New(text)
		}
		a.notice(text)
	}
	return nil
}

// nextSteps says how to turn on what install has just written, which a shell
// that is already running has not read. Nothing written needs no step.
func (a *App) nextSteps(shell shellrc.Shell, written []completionTarget) {
	if len(written) == 0 {
		return
	}
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
		// Terminal and iTerm start a login shell for every window, and a
		// login bash reads ~/.bash_profile and never ~/.bashrc unless the
		// former sources the latter, which nothing on a Mac sets up.
		// Whether it already does cannot be told reliably from here, as it
		// may go through ~/.profile or a file of the person's own, so this
		// is said and not checked.
		if a.d.GOOS == "darwin" {
			for _, t := range written {
				a.notice(a.Cat.T("completion.bashProfile", i18n.Args{"file": t.path}))
			}
		}
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
// asking. A setup without the marker is somebody's own and stays, and so does
// a block in a profile a change would damage, a signed one above all.
//
// As in install, the files the question named are read and planned again
// after the yes, and only those, so an edit made while it waited is kept.
func (a *App) uninstallCompletion(cmd *cobra.Command, args []string, yes bool) error {
	_, targets, err := a.completionTargets(cmd, args)
	if err != nil {
		return err
	}

	plan := a.planUninstall(targets, false)
	if len(plan.marked) == 0 && len(plan.refused) == 0 {
		// The shell came from $SHELL unless it was named, and a person who
		// installed for another one finds nothing here: the files say where
		// it looked, and the command how to look elsewhere.
		a.notice(a.Cat.T("completion.nothingToRemove", i18n.Args{
			"files":   joinPaths(pathsOf(targets)),
			"command": cmd.CommandPath(),
		}))
		return nil
	}
	if err := a.refuseProfiles(plan.refused, len(plan.marked) == 0); err != nil {
		return err
	}

	ok, err := a.confirm(yes, a.Cat.T("completion.confirmUninstall", i18n.Args{
		"files": joinPaths(pathsOf(plan.marked)),
	}))
	if err != nil || !ok {
		return err
	}

	fresh, err := a.reread(plan.marked)
	if err != nil {
		return err
	}
	again := a.planUninstall(fresh, true)

	for _, t := range again.marked {
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
	return a.refuseProfiles(again.refused, len(again.marked) == 0)
}

// uninstallPlan is what uninstall makes of the files' content.
type uninstallPlan struct {
	// marked are the files to take the block out of.
	marked []completionTarget
	// refused are the profiles that hold the block and that a change would
	// damage.
	refused []string
}

// planUninstall sorts the targets by what they hold and reports the setups
// made by hand, which stay. again says the files are being read a second
// time, after the question: a file whose block went in the meantime is then
// reported, where on the first read it is simply not one of the files.
func (a *App) planUninstall(targets []completionTarget, again bool) uninstallPlan {
	var plan uninstallPlan
	for _, t := range targets {
		switch {
		case t.state == shellrc.Installed && t.unsafe:
			plan.refused = append(plan.refused, t.path)
		case t.state == shellrc.Installed:
			plan.marked = append(plan.marked, t)
		case t.state == shellrc.ByHand:
			a.notice(a.Cat.T("completion.byHandLeft", i18n.Args{"file": t.path}))
		case again:
			a.notice(a.Cat.T("completion.alreadyRemoved", i18n.Args{"file": t.path}))
		}
	}
	return plan
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

// completionTargets settles the shell, finds its files and reads them.
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
		if err := a.readTarget(&targets[i]); err != nil {
			return "", nil, err
		}
	}
	return shell, targets, nil
}

// reread returns copies of the targets with what their files hold now.
func (a *App) reread(targets []completionTarget) ([]completionTarget, error) {
	fresh := slices.Clone(targets)
	for i := range fresh {
		if err := a.readTarget(&fresh[i]); err != nil {
			return nil, err
		}
	}
	return fresh, nil
}

// readTarget reads what a file holds and judges it. A file that does not
// exist reads as empty, which is a file with no setup in it.
func (a *App) readTarget(t *completionTarget) error {
	data, err := os.ReadFile(t.path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.New(a.Cat.T("completion.readFailed", i18n.Args{
			"file":   t.path,
			"reason": describe(err, a.Cat),
		}))
	}
	t.content = data
	t.state = shellrc.Inspect(data)
	// Cobra's own help tells people to save its fish script in this very
	// file, and that script does not contain the words Inspect recognises a
	// hand-made setup by. In a file that is ours alone, anything without the
	// marker is somebody's setup all the same.
	if t.own && exists && t.state != shellrc.Installed {
		t.state = shellrc.ByHand
	}
	t.unsafe = t.profile && unsafeProfile(data)
	return nil
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
		// The path is opened as it came, so it has to be one: valid UTF-8,
		// as asked for, with no byte-order mark in front, and absolute. A
		// path decoded wrongly would name a file that is not the profile.
		// %q shows what came instead, with any byte that is not text
		// escaped.
		if !utf8.ValidString(profile) || strings.HasPrefix(profile, "\uFEFF") || !filepath.IsAbs(profile) {
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
		targets = append(targets, completionTarget{path: profile, profile: true, policy: policy})
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
