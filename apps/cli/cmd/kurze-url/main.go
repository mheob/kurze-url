// Command kurze-url creates short links, lists them and shows their numbers
// from the terminal. It is a thin client over the kurze-url HTTP API.
package main

import (
	"os"

	"github.com/mheob/kurze-url/apps/cli/internal/cli"
)

// Stamped at release time through linker flags (-X main.version=...). The
// names are the ones goreleaser's default ldflags set, so the release
// configuration needs no override; a plain build keeps these placeholders.
var version, commit, date = "dev", "none", "unknown"

func main() {
	d := cli.Default(cli.BuildInfo{Version: version, Commit: commit, Date: date})
	os.Exit(cli.Run(d, os.Args[1:]))
}
