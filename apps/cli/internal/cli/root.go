// Package cli is the kurze-url command tree: the cobra commands, their
// wiring to the outside world through Deps, and the mapping from what a
// command returned to the process's exit code.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// programName is the binary's name as the person types it. It prefixes every
// failure line and the version line, and it is the root command's Use.
const programName = "kurze-url"

// Run executes one invocation and returns the process's exit code: 0 on
// success, 1 on any failure, 2 on a usage error. The caller passes the
// result to os.Exit; Run itself never exits, so tests can call it.
func Run(d *Deps, args []string) int {
	return run(newRoot(d), d, args)
}

// run executes root with args and reports a failure on d.Stderr. It is split
// from Run so that tests can mount a throwaway command on the real root and
// still go through the real error handling.
func run(root *cobra.Command, d *Deps, args []string) int {
	root.SetArgs(args)

	cmd, err := root.ExecuteC()
	if err != nil {
		report(d, cmd, err)
	}
	return exitCode(err)
}

// report prints a failure the way every command does: one line naming the
// program and the problem, and, for a usage error, a second line pointing at
// the help of the command the person got wrong.
//
// The problem is worded by describe in the person's language. That second
// line stays English with cobra's own usage-error text: command names, flags
// and --help are the one part of the CLI that is not translated.
//
// cmd is the command cobra stopped at, which for an unknown subcommand is its
// parent, so the hint always names a command that exists.
func report(d *Deps, cmd *cobra.Command, err error) {
	// A failed write to stderr has nowhere left to be reported.
	_, _ = fmt.Fprintf(d.Stderr, "%s: %s\n", programName, describe(err, catalogFor(d)))

	var usage *UsageError
	if errors.As(err, &usage) {
		_, _ = fmt.Fprintf(d.Stderr, "Run '%s --help' for usage.\n", cmd.CommandPath())
	}
}

// newRoot builds the command tree.
//
// Cobra's own error and usage printing are silenced, because Run prints
// failures itself in one format and cobra would otherwise add a second
// "Error:" line and the whole usage text to every mistake. The root has the
// shape of a group (see newGroup) for the same reason as any group: without
// it, "kurze-url nope" would exit 0 or 1 instead of 2.
func newRoot(d *Deps) *cobra.Command {
	root := newGroup(programName, "Create and inspect short links from the terminal")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	root.SetIn(d.Stdin)

	// A bad flag is a usage error like a bad argument. Children inherit
	// this from the root.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UsageError{Err: err}
	})

	root.AddCommand(
		newVersionCmd(d),
		newLoginCmd(d),
		newLogoutCmd(d),
		newWhoamiCmd(d),
		newTeamCmd(d),
		newLinkCmd(d),
		newConfigCmd(d),
	)

	// Cobra's own "completion bash|zsh|fish|powershell" stays, but it builds
	// the command with a bare cobra.NoArgs and without a RunE, so
	// "completion nope" would print help and exit 0, and the four shell
	// subcommands report an extra argument as a plain failure (exit 1, no
	// hint). The parent gets a group's shape and each shell keeps its own
	// RunE with a wrapped validator. Cobra adds the command on its own
	// during execution and skips it when one exists, so adding it here,
	// early, is what lets it be changed at all.
	root.InitDefaultCompletionCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() != "completion" {
			continue
		}
		asGroup(cmd)
		for _, shell := range cmd.Commands() {
			shell.Args = usageArgs(cobra.NoArgs)
		}
	}

	return root
}
