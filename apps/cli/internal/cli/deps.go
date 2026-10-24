package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

// BuildInfo identifies the binary. The release build stamps it in through
// linker flags on the variables in cmd/kurze-url, and an unstamped build
// (go run, go install from a checkout) reports the placeholders there.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// credentialsDirName is the directory under the user's config directory that
// holds the credentials fallback file. It is the config file's directory too,
// so one folder holds everything the CLI keeps.
const credentialsDirName = "kurze-url"

// httpTimeout bounds every request of the default client. Without one a
// stalled connection would hang a command forever; the auth calls and the API
// client both use it as handed over.
const httpTimeout = 30 * time.Second

// commandTimeout bounds a program RunCommand starts. The one it starts is
// PowerShell, asked for its profile, which takes a second or two on a cold
// Windows machine and should never take ten.
const commandTimeout = 10 * time.Second

// Deps is everything a command reaches outside the process through: its
// streams, the environment, the clock, the terminal, the browser, HTTP, the
// credential store, the home directory and other programs.
//
// Commands never read os.Stdout, os.Getenv or a package-level client
// directly, so a test hands them buffers, a fake server and an in-memory
// store instead, and none of it touches the terminal, the keychain or the
// network. Each field added here gets its production value in Default, which
// keeps cmd/kurze-url's main free of wiring, and TestDefaultFillsEveryField
// fails for one that does not.
//
// A Deps is built by Default or filled in completely by a test. A command
// that needs a field calls it without a nil check.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Build  BuildInfo

	// Getenv reads an environment variable (os.Getenv). The config location,
	// the instance overrides and the language all come through it.
	Getenv func(string) string
	// Now is the clock, which the stats windows and the token expiry read.
	Now func() time.Time

	// StdinTTY, StdoutTTY and TermWidth describe the terminal. A prompt
	// needs a person on stdin, a table needs one reading stdout, and a
	// table's width is the terminal's. TermWidth is 0 when unknown.
	StdinTTY  func() bool
	StdoutTTY func() bool
	TermWidth func() int

	// OpenBrowser opens a URL in the person's browser. login falls back to
	// printing the URL when it returns an error.
	OpenBrowser func(string) error

	// HTTPClient sends every request to the API and to the authorization
	// server. It has a timeout and leaves CheckRedirect alone, because the
	// API client and the auth calls refuse redirects only when the client
	// they are given sets no policy of its own.
	HTTPClient *http.Client

	// LoginPorts are the loopback ports login tries, in order, and
	// LoginTimeout is how long it waits for the browser. The ports are the
	// ones registered on the OAuth client; a test passes 0 for "any".
	LoginPorts   []int
	LoginTimeout time.Duration

	// CredentialsDir is where the credentials fallback file goes, and
	// NewStore builds the store for one environment. A test returns an
	// in-memory store from it.
	CredentialsDir string
	NewStore       func(envName, dir string) credentials.Store

	// CompletionTimeout is the one deadline a Tab press gets, for building
	// the App, refreshing the token and every request together. A test that
	// needs it to pass shortens it.
	CompletionTimeout time.Duration

	// GOOS is the operating system the binary runs on (runtime.GOOS).
	// `completion install` reads it to choose PowerShell on Windows.
	GOOS string
	// HomeDir is the person's home directory (os.UserHomeDir), where the
	// shell configuration files live. A test hands out a directory of its
	// own, so no command reaches the real ones.
	HomeDir func() (string, error)
	// LookPath finds a program on the PATH (exec.LookPath), and RunCommand
	// starts one and returns what it printed to stdout, giving up after
	// commandTimeout. `completion install` asks PowerShell for its profile
	// through them, and a test fakes both, so none is ever started.
	LookPath   func(string) (string, error)
	RunCommand func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Default returns the production wiring: the process's own streams,
// environment, clock and terminal, a real browser opener, the keychain-backed
// credential store, a client with a 30 second timeout, a two second budget
// for tab completion, and the real home directory and PATH.
func Default(b BuildInfo) *Deps {
	return &Deps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Build:  b,

		Getenv: os.Getenv,
		Now:    time.Now,

		StdinTTY:  func() bool { return output.IsTerminal(os.Stdin.Fd()) },
		StdoutTTY: func() bool { return output.IsTerminal(os.Stdout.Fd()) },
		TermWidth: func() int { return output.Width(os.Stdout.Fd()) },

		OpenBrowser: openBrowser,
		HTTPClient:  &http.Client{Timeout: httpTimeout},

		// A copy, so editing the Deps cannot edit the package's own list.
		LoginPorts:   slices.Clone(auth.DefaultPorts),
		LoginTimeout: auth.DefaultLoginTimeout,

		CredentialsDir: credentialsDir(os.UserConfigDir),
		NewStore: func(envName, dir string) credentials.Store {
			return credentials.New(envName, dir)
		},

		CompletionTimeout: completionTimeout,

		GOOS:       runtime.GOOS,
		HomeDir:    os.UserHomeDir,
		LookPath:   exec.LookPath,
		RunCommand: runCommand,
	}
}

// runCommand starts a program and returns its standard output, within
// commandTimeout. Its standard error is not shown: what a failing program
// says there travels in the *exec.ExitError.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// credentialsDir is the user's config directory plus "kurze-url", or "" when
// the machine has no config directory (no home, no XDG_CONFIG_HOME). The
// empty string is deliberate: the credential store refuses an empty
// directory with an error of its own, which is better than a token file
// written into whatever directory the person happens to be in.
func credentialsDir(userConfigDir func() (string, error)) string {
	dir, err := userConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, credentialsDirName)
}
