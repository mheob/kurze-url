package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

var testBuild = BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-10-23"}

// streams is a Deps whose output lands in buffers the test can read, and
// whose stdin is empty. Nothing here touches the terminal.
type streams struct {
	deps   *Deps
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newStreams() streams {
	s := streams{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	s.deps = &Deps{
		Stdout: s.stdout,
		Stderr: s.stderr,
		Stdin:  &bytes.Buffer{},
		Build:  testBuild,
	}
	return s
}

// run executes the real command tree and returns the exit code.
func (s streams) run(args ...string) int {
	return Run(s.deps, args)
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	s := newStreams()

	code := s.run("version")

	require.Equal(t, 0, code)
	require.Equal(t, "kurze-url 1.2.3 (abc1234, 2026-10-23)\n", s.stdout.String())
	require.Empty(t, s.stderr.String())
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run("nope")

	require.Equal(t, 2, code)
	require.Contains(t, s.stderr.String(), "unknown command")
	require.Empty(t, s.stdout.String())
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run("version", "--nope")

	require.Equal(t, 2, code)
	require.Contains(t, s.stderr.String(), "unknown flag: --nope")
}

func TestExtraArgumentIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run("version", "extra")

	require.Equal(t, 2, code)
	require.Empty(t, s.stdout.String())
}

func TestBareRootIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run()

	require.Equal(t, 2, code)
	// The help is a message, not a result, so it must not reach stdout.
	require.Empty(t, s.stdout.String())
	require.Contains(t, s.stderr.String(), "Available Commands:")
}

func TestUsageErrorPointsAtTheCommandsHelp(t *testing.T) {
	s := newStreams()

	s.run("version", "--nope")

	require.Equal(t,
		"kurze-url: unknown flag: --nope\n"+
			"Run 'kurze-url version --help' for usage.\n",
		s.stderr.String())
}

func TestHelpFlagExitsZeroOnStdout(t *testing.T) {
	s := newStreams()

	code := s.run("--help")

	require.Equal(t, 0, code)
	require.Contains(t, s.stdout.String(), "Available Commands:")
	require.Empty(t, s.stderr.String())
}

func TestCompletionPrintsAZshScript(t *testing.T) {
	s := newStreams()

	code := s.run("completion", "zsh")

	require.Equal(t, 0, code)
	require.Contains(t, s.stdout.String(), "#compdef kurze-url")
}

func TestUnknownCompletionShellIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run("completion", "nope")

	require.Equal(t, 2, code)
	require.Contains(t, s.stderr.String(), "unknown command")
}

func TestBareCompletionIsAUsageError(t *testing.T) {
	s := newStreams()

	code := s.run("completion")

	require.Equal(t, 2, code)
	require.Empty(t, s.stdout.String())
}

// newTestRoot is the real root plus whatever the test mounts on it, so the
// group and failure paths run through the same error handling as production.
func newTestRoot(s streams, mount ...*cobra.Command) *cobra.Command {
	root := newRoot(s.deps)
	root.AddCommand(mount...)
	return root
}

// newThingGroup is a throwaway group with one working child. A function, not
// a variable, because a cobra command can only have one parent.
func newThingGroup() *cobra.Command {
	group := newGroup("thing", "A throwaway group")
	group.AddCommand(&cobra.Command{
		Use:   "child",
		Short: "A throwaway child",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte("child ran\n"))
			return err
		},
	})
	return group
}

func TestGroupWithoutSubcommandIsAUsageError(t *testing.T) {
	t.Run("bare group", func(t *testing.T) {
		s := newStreams()

		code := run(newTestRoot(s, newThingGroup()), s.deps, []string{"thing"})

		require.Equal(t, 2, code)
		require.Empty(t, s.stdout.String())
		require.Contains(t, s.stderr.String(), "child")
		require.Contains(t, s.stderr.String(), "Run 'kurze-url thing --help' for usage.")
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		s := newStreams()

		code := run(newTestRoot(s, newThingGroup()), s.deps, []string{"thing", "nope"})

		require.Equal(t, 2, code)
		require.Contains(t, s.stderr.String(), "unknown command")
	})

	t.Run("known subcommand", func(t *testing.T) {
		s := newStreams()

		code := run(newTestRoot(s, newThingGroup()), s.deps, []string{"thing", "child"})

		require.Equal(t, 0, code)
		require.Equal(t, "child ran\n", s.stdout.String())
	})
}

func TestFailureExitsOneWithoutAUsageHint(t *testing.T) {
	s := newStreams()
	boom := &cobra.Command{
		Use:  "boom",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error { return errors.New("it broke") },
	}

	code := run(newTestRoot(s, boom), s.deps, []string{"boom"})

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: it broke\n", s.stderr.String())
	require.Empty(t, s.stdout.String())
}

func TestUsageErrorWrapsItsCause(t *testing.T) {
	cause := errors.New("bad value")
	err := error(&UsageError{Err: cause})

	var usage *UsageError
	require.ErrorAs(t, err, &usage)
	require.ErrorIs(t, err, cause)
	require.Equal(t, "bad value", err.Error())
}

func TestUsageArgsWrapsAValidatorsError(t *testing.T) {
	validate := usageArgs(cobra.ExactArgs(1))

	require.NoError(t, validate(&cobra.Command{Use: "x"}, []string{"one"}))

	err := validate(&cobra.Command{Use: "x"}, nil)
	var usage *UsageError
	require.ErrorAs(t, err, &usage)
}
