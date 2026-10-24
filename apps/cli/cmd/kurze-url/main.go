// Command kurze-url creates short links, lists them and shows their numbers
// from the terminal. It is a thin client over the kurze-url HTTP API.
package main

import (
	"os"

	"github.com/mheob/kurze-url/apps/cli/internal/cli"
)

// Stamped at release time through linker flags (-X main.version=...). The
// release configuration, apps/cli/.goreleaser.yaml, sets them explicitly with
// the short commit and the commit date rather than goreleaser's defaults, the
// full hash and the build time; a plain build keeps these placeholders.
var version, commit, date = "dev", "none", "unknown"

func main() {
	d := cli.Default(cli.BuildInfo{Version: version, Commit: commit, Date: date})
	os.Exit(cli.Run(d, os.Args[1:]))
}
