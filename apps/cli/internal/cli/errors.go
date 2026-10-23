package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Exit codes. Scripts branch on these, so they are part of the CLI's
// contract: 1 says "this run failed", 2 says "this invocation was wrong and
// retrying it unchanged cannot help".
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// UsageError marks a mistake in how the command was invoked: an unknown
// command or flag, a wrong number of arguments, or a flag value outside its
// documented set or range. Run turns it into exit code 2 and a pointer to the
// command's help; any other error is a plain failure and exits 1.
//
// It is always used as a pointer. With pointer receivers a bare UsageError
// value does not implement error, so a command that returns one fails to
// compile instead of silently exiting 1, which is what errors.As against
// *UsageError would do to it.
type UsageError struct {
	Err error
}

// Error returns the wrapped error's message unchanged, so a usage error reads
// the way cobra or pflag wrote it.
func (e *UsageError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *UsageError) Unwrap() error {
	return e.Err
}

// usageArgs wraps a positional-argument validator so that its refusal is a
// UsageError.
//
// Every command and every group sets Args through this. A command with no
// Args at all falls back to cobra's legacy check, which reports an unknown
// subcommand as a plain error (exit 1), and a command with a bare cobra
// validator does the same for a wrong argument count.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return &UsageError{Err: err}
		}
		return nil
	}
}

// newGroup returns a command that only holds subcommands, such as "link" or
// "team".
//
// Both halves of its shape are load-bearing. Without Args, cobra reports an
// unknown subcommand as a plain error and the CLI exits 1. With Args but
// without a RunE, cobra treats the command as non-runnable, prints its help
// and returns nil before it ever validates the arguments, so "kurze-url link
// nope" would exit 0. RunE runs only when no subcommand matched and no
// argument is left over; it shows the help on stderr, because it is a
// message and not a result, and reports the missing subcommand.
func newGroup(use, short string) *cobra.Command {
	return asGroup(&cobra.Command{Use: use, Short: short})
}

// asGroup gives an existing command a group's shape. It exists for cobra's own
// "completion" command, which is built by cobra and has the non-runnable shape
// newGroup's comment describes.
func asGroup(cmd *cobra.Command) *cobra.Command {
	cmd.Args = usageArgs(cobra.NoArgs)
	cmd.RunE = missingCommand
	return cmd
}

// missingCommand is the RunE of the root and of every group: the person typed
// a command that only contains others.
func missingCommand(cmd *cobra.Command, _ []string) error {
	cmd.SetOut(cmd.ErrOrStderr())
	if err := cmd.Help(); err != nil {
		return fmt.Errorf("print help: %w", err)
	}
	return &UsageError{Err: fmt.Errorf("missing command for %q", cmd.CommandPath())}
}

// exitCode maps what a command returned to the process's exit code.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return exitUsage
	}
	return exitFailure
}
