package cli

import (
	"io"
	"os"
)

// BuildInfo identifies the binary. The release build stamps it in through
// linker flags on the variables in cmd/kurze-url, and an unstamped build
// (go run, go install from a checkout) reports the placeholders there.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Deps is everything a command reaches outside the process through: its
// streams today, and later the environment, the clock, the browser, HTTP and
// the credential store.
//
// Commands never read os.Stdout, os.Getenv or a package-level client
// directly, so a test hands them buffers, a fake server and an in-memory
// store instead, and none of it touches the terminal, the keychain or the
// network. Each field added here gets its production value in Default, which
// keeps cmd/kurze-url's main free of wiring.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Build  BuildInfo
}

// Default returns the production wiring: the process's own streams.
func Default(b BuildInfo) *Deps {
	return &Deps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Build:  b,
	}
}
