# CLI Tab Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tab completes the kurze-url CLI's fixed and dynamic values, and `kurze-url completion install|uninstall` sets completion up in the person's shell after asking.

**Architecture:** Value completion is a set of cobra `CompletionFunc`s registered in the existing command constructors. The dynamic ones run through one wrapper, `completeWith`: it builds the App on a muted copy of `Deps`, gives the work a 2-second budget off the main goroutine, and turns every failure into an empty, no-file answer. The shell-file editing is a small pure package, `internal/shellrc`, which owns the block text, adding and removing it, and the atomic write. The `completion install|uninstall` commands only detect the shell, find the file, ask and report.

**Tech Stack:** Go, `spf13/cobra` v1.10.2 (`cobra.Completion`, `cobra.CompletionWithDesc`, `cobra.CompletionFunc`, `cobra.FixedCompletions`, `cobra.NoFileCompletions`), testify, POSIX sh, PowerShell 5.1 and 7.

**Spec:** `docs/superpowers/specs/2026-10-24-cli-completion-design.md`

## Global Constraints

- All Go work is in `apps/cli`, its own module. No API change, and no new API call beyond `Me`, `Folders`, `Domains`, `ListLinks`.
- Run Go commands from `apps/cli`: `go vet ./...`, `gofmt -l .` (empty), `go test -race -count=1 ./...`, `GOOS=windows GOARCH=amd64 go build ./...`, `GOOS=darwin GOARCH=arm64 go build ./...`.
- Completion is silent: it writes nothing to stderr and nothing to stdout but cobra's protocol. It never opens a browser and never prompts. It answers within the budget, `completionTimeout = 2 * time.Second`.
- An empty or failed dynamic answer uses the directive `cobra.ShellCompDirectiveNoFileComp`.
- Everything a command prints to a person goes through `a.Cat.T("literal.key")` with the key defined in both `internal/i18n/locales/en.json` and `de.json`. `TestEveryUsedKeyIsDefined` and `TestCatalogsHaveTheSameKeys` enforce this. Help texts (`Short`, `Long`, flag usage) stay English.
- The marker line is exactly `# kurze-url completion (added by kurze-url completion install)`.
- Repository rules:
  - git writes go through GitButler only (`but commit -b <branch> -m … <ids>`), never `git add`, `git commit` or `git reset`;
  - run `pnpm format` from the repository root before each commit;
  - no Co-Authored-By trailer;
  - subagents never push, open PRs or tag.

## Review Focus

1. **Answering `n`, or an empty stdin that is not a terminal:** nothing is written, and the file's modification time is unchanged. Task 4 pins it.
2. **A `.bashrc` that is a symlink** (dotfile managers): writing must not replace the link with a regular file. `shellrc.WriteFile` resolves the link and writes the target. Task 3 pins it.
3. **`ZDOTDIR` set:** `.zshrc` lives there, not in `$HOME`. Task 4 pins it.
4. **A slug typed in another case** (`SOMM`): completion matches the stored lower-case slugs. Task 2 pins it.
5. **The API answering slower than the budget:** completion returns empty within the budget plus a margin, not after the HTTP client's 30 s. Task 2 pins it with a short test budget.

---

### Task 1: Completion foundation and fixed values

**Files:**

- Create: `apps/cli/internal/cli/complete.go`
- Modify: `apps/cli/internal/cli/deps.go` (one field), `config_cmds.go`, `qr_cmd.go`, `link_cmds.go`, `stats_cmd.go`, `lookup.go`, `auth_cmds.go`, `team_cmds.go`, `version.go`
- Test: `apps/cli/internal/cli/complete_test.go`

**Interfaces:**

- Produces:
  - `Deps.CompletionTimeout time.Duration`. `Default` sets it to `completionTimeout` (2 s), and the harness keeps 2 s unless a test changes it.
  - `func completeWith(d *Deps, list func(ctx context.Context, a *App, cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, error)) cobra.CompletionFunc`. It copies `*d` into a muted `Deps`:
    - `Stdout`, `Stderr` = `io.Discard`;
    - `Stdin` = empty reader, `StdinTTY` = false;
    - `OpenBrowser` returns an error.

    It then calls `newApp` on the muted copy and runs `list` in a goroutine under `context.WithTimeout(context.Background(), d.CompletionTimeout)`, selecting on the result and on `ctx.Done()`. Any error, a timeout or a panic in `list` (recovered) gives `nil, cobra.ShellCompDirectiveNoFileComp`.

  - `func noValues(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective)` is `cobra.NoFileCompletions`, named for readability.
  - `func fixedValues(values ...string) cobra.CompletionFunc` gives `cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)`.
  - `func withPrefix(values []cobra.Completion, typed string) []cobra.Completion` keeps the entries whose value (before any `\t`) starts with `strings.ToLower(typed)`, compared on the lower-cased value.
- Test helper (in `complete_test.go`): `func (h *harness) complete(args ...string) (values []string, directive int)`. It runs `h.run(append([]string{"__complete"}, args...)...)` and parses stdout: candidate lines, then `:<n>`. Values keep their `\t` description.

- [ ] **Step 1: Write the failing tests** in `complete_test.go`

```go
func TestConfigKeysComplete(t *testing.T) {
	h := newHarness(t)
	values, directive := h.complete("config", "set", "")
	require.Equal(t, []string{"language", "team", "folder", "domain", "qr.format", "qr.size", "qr.fg", "qr.bg"}, values)
	require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive)
	for _, sub := range []string{"get", "unset"} {
		got, _ := h.complete("config", sub, "q")
		require.Equal(t, []string{"qr.format", "qr.size", "qr.fg", "qr.bg"}, got, sub)
	}
}

func TestConfigSetValuesComplete(t *testing.T) {
	h := newHarness(t)
	got, _ := h.complete("config", "set", "language", "")
	require.Equal(t, []string{"en", "de"}, got)
	got, _ = h.complete("config", "set", "qr.format", "")
	require.Equal(t, []string{"svg", "png"}, got)
	for _, key := range []string{"qr.size", "qr.fg", "qr.bg"} {
		got, directive := h.complete("config", "set", key, "")
		require.Empty(t, got, key)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, key)
	}
}

func TestQRFormatCompletes(t *testing.T) {
	h := newHarness(t)
	got, _ := h.complete("link", "qr", "sommerfest", "--format", "")
	require.Equal(t, []string{"svg", "png"}, got)
}

func TestFreeTextOffersNoFiles(t *testing.T) {
	cases := [][]string{
		{"link", "create", ""},
		{"link", "create", "https://example.org", "--slug", ""},
		{"link", "list", "--search", ""},
		{"link", "list", "--limit", ""},
		{"link", "qr", "x", "--size", ""},
		{"link", "qr", "x", "--fg", ""},
		{"link", "qr", "x", "--bg", ""},
		{"login", ""}, {"logout", ""}, {"whoami", ""}, {"team", "list", ""}, {"version", ""},
	}
	for _, args := range cases {
		h := newHarness(t)
		got, directive := h.complete(args...)
		require.Empty(t, got, args)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, args)
	}
}

func TestQROutputStillCompletesFiles(t *testing.T) {
	h := newHarness(t)
	_, directive := h.complete("link", "qr", "x", "--output", "")
	require.Equal(t, int(cobra.ShellCompDirectiveDefault), directive)
}

func TestCompletionWritesNothingToStderr(t *testing.T) {
	h := newHarness(t)
	h.complete("config", "set", "")
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}
```

The last test only checks that no failure line appears. Cobra prints its own `Completion ended with directive` note to stderr, so do not assert an empty stderr.

- [ ] **Step 2: Run them to see them fail**

Run: `cd apps/cli && go test ./internal/cli -run 'Complete|Completes|NoFiles|Completion' -count=1` Expected: FAIL. Keys complete as nothing, and `link create` offers files (directive 0).

- [ ] **Step 3: Implement**

`complete.go` holds the interfaces above. Register in the constructors:

- `config get|set|unset` `ValidArgsFunction`:
  - first argument: the key names from `config.Keys()`, through `withPrefix`;
  - `set`'s second argument: `language` gives `en`, `de`; `qr.format` gives `svg`, `png`; `qr.size`, `qr.fg`, `qr.bg` give nothing;
  - `team`, `folder`, `domain` are left to Task 2 (nothing for now);
  - beyond the arguments a command takes, nothing.
- `link qr`: `RegisterFlagCompletionFunc("format", fixedValues("svg", "png"))`, and `noValues` for `size`, `fg` and `bg`.
- `link create`: `ValidArgsFunction: noValues`, and `slug` gets `noValues`.
- `link list`: `search` and `limit` get `noValues`.
- `login`, `logout`, `whoami`, `team list`, `version`: `ValidArgsFunction: noValues`.

- [ ] **Step 4: Run the tests**

Run: `cd apps/cli && go test -race -count=1 ./...` Expected: PASS.

- [ ] **Step 5: Commit** (`pnpm format` first) on branch `feat/cli-completion`: `feat(cli): complete config keys and fixed values`

---

### Task 2: Dynamic values

**Files:**

- Modify: `apps/cli/internal/cli/complete.go`, `config_cmds.go`, `link_cmds.go`, `lookup.go`
- Test: `apps/cli/internal/cli/complete_test.go`

**Interfaces:**

- Consumes: `completeWith`, `withPrefix`, `Deps.CompletionTimeout` (Task 1); `App.me`, `membershipOf`, `App.configuredTeam`, `a.API.Folders`, `a.API.Domains`, `a.API.ListLinks`, `a.Env.SharedHost`, `verifiedStatus` (existing).
- Produces:
  - `func completionTeam(ctx context.Context, a *App, cmd *cobra.Command, useDefault bool) (apiclient.Membership, bool)`. It takes the `--team` flag's value, else the configured team when `useDefault`, folded with `strings.ToLower` and matched through `membershipOf`. It gives `false` when there is no team or no membership.
  - `func completeTeams(d *Deps) cobra.CompletionFunc`: `CompletionWithDesc(slug, name)` per membership.
  - `func completeFolders(d *Deps, useDefault bool) cobra.CompletionFunc`: folder names.
  - `func completeDomains(d *Deps, useDefault bool) cobra.CompletionFunc`: the verified hostnames, then `a.Env.SharedHost`.
  - `func completeLinkSlugs(d *Deps) cobra.CompletionFunc`: for the first argument only. Nothing when `toComplete` holds `/` or `:`. Otherwise `ListLinks(team, ListLinksQuery{Q: toComplete, PerPage: 100})`, keeping slugs with the lower-cased prefix, as `CompletionWithDesc(slug, destination)`.

  All four are run through `completeWith` and filtered through `withPrefix`.

Registration:

- `--team` on every command that has it gets `completeTeams`.
- `link create --folder`, `link list --folder` get `completeFolders(d, true)`.
- `link create --domain` and the lookup `--domain` (`addLookupFlags`) get `completeDomains(d, true)`. `addLookupFlags` therefore takes `d *Deps`.
- `link qr` and `link stats` `ValidArgsFunction` get `completeLinkSlugs(d)`.
- `config set`'s second argument: `team` gives teams; `folder` and `domain` give `completeFolders(d, false)` and `completeDomains(d, false)`. The `false` means `--team` on the line is required, because the command refuses those keys without it.

- [ ] **Step 1: Write the failing tests.** Fixtures come from the harness: `h.signIn()`, `h.serveMe(teamGruenwald, teamMuster)`, `h.writeConfig("team: sv-gruenwald\n")`, and `h.api.respond(...)` for `/v1/teams/{id}/folders`, `/domains` and `/links`. Use the paths the existing tests use.

```go
func TestTeamsComplete(t *testing.T)                // link list --team "" gives "sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster"; prefix "tc" gives only tc-muster
func TestFoldersCompleteForTheDefaultTeam(t *testing.T) // folders "Sommerfest 2026", "Presse" on sv-gruenwald; `link list --folder ""` gives both, "Sommerfest 2026" as ONE value
func TestFoldersFollowTheTeamOnTheLine(t *testing.T)    // `link list --team tc-muster --folder ""` asks tc-muster's folders, not the default team's
func TestDomainsCompleteVerifiedAndShared(t *testing.T) // domains links.sv.de verified, neu.sv.de pending: gives "links.sv.de", then the harness's shared host; no "neu.sv.de"
func TestLinkSlugsComplete(t *testing.T)            // links sommerfest, sommer-2025, newsletter-sommer: `link qr "somm"` sends q=somm, per_page=100, and gives "sommerfest\t<dest>", "sommer-2025\t<dest>" only
func TestLinkSlugsIgnoreCase(t *testing.T)          // `link stats "SOMM"` gives the same two slugs (Review Focus 4)
func TestShortURLsAreNotCompleted(t *testing.T)     // `link qr "go.kurze-url.app/so"` and "https:" make no API call and give nothing
func TestConfigSetTeamValuesComplete(t *testing.T)  // `config set team ""` gives the two slugs; `config set folder ""` with no --team makes no API call and gives nothing; with --team sv-gruenwald gives its folders
func TestUnknownTeamGivesNothing(t *testing.T)      // `link list --team nope --folder ""`: nothing, and no folder request (Review Focus: spec 5)
func TestCompletionIsSilentWhenSignedOut(t *testing.T) // no h.signIn(): empty, directive NoFileComp, nothing opened in the browser, no "kurze-url:" on stderr, no API call
func TestCompletionIsSilentOnAPIErrors(t *testing.T)   // /v1/me answers 500: empty, NoFileComp, no "kurze-url:" on stderr
func TestCompletionRefreshesAnExpiredToken(t *testing.T) // stored token expired: the stub auth refresh is called once, the new tokens are saved, values come back
func TestCompletionGivesUpAtTheBudget(t *testing.T)  // h.deps.CompletionTimeout = 100ms, /v1/me sleeps 2s: returns empty within 1s (time the h.complete call)
```

Each test asserts the exact value list, and the directive is `ShellCompDirectiveNoFileComp`.

- [ ] **Step 2: Run them to see them fail**

Run: `cd apps/cli && go test ./internal/cli -run 'Complete|Completion|Teams|Folders|Domains|Slugs|ShortURLs|UnknownTeam' -count=1` Expected: FAIL. Nothing is registered yet, so every value list is empty.

- [ ] **Step 3: Implement** the interfaces and the registration above.

- [ ] **Step 4: Run the whole suite**

Run: `cd apps/cli && go test -race -count=1 ./... && go vet ./...` Expected: PASS. The budget test must not be flaky under `-race`: assert "under 1 s", not "under 150 ms".

- [ ] **Step 5: Commit**: `feat(cli): complete teams, folders, domains, slugs`

---

### Task 3: `internal/shellrc`, the block and the file

**Files:**

- Create: `apps/cli/internal/shellrc/shellrc.go`, `apps/cli/internal/shellrc/shellrc_test.go`

**Interfaces:**

- Produces:
  - `const Marker = "# kurze-url completion (added by kurze-url completion install)"`
  - `type Shell string` with `Zsh = "zsh"`, `Bash = "bash"`, `Fish = "fish"`, `PowerShell = "powershell"`, and `func ParseShell(s string) (Shell, bool)`, which also accepts `pwsh` as `PowerShell`.
  - `func Command(s Shell) string`, the one line after the marker:
    - zsh: `if command -v kurze-url >/dev/null 2>&1; then (( $+functions[compdef] )) || { autoload -Uz compinit && compinit; }; source <(kurze-url completion zsh); fi`
    - bash: `if command -v kurze-url >/dev/null 2>&1; then eval "$(kurze-url completion bash)"; fi` (`eval`, because bash 3.2's `source <(…)` silently reads nothing)
    - fish: `command -q kurze-url; and kurze-url completion fish | source`
    - powershell: `if (Get-Command kurze-url -ErrorAction SilentlyContinue) { kurze-url completion powershell | Out-String | Invoke-Expression }`
  - `func Block(s Shell) string` returns `"\n" + Marker + "\n" + Command(s) + "\n"`. The leading newline both ends a last line that lacks one and is exactly what removal takes back, which is how uninstall restores the file byte for byte.
  - `func OwnFile(s Shell) string` returns `Marker + "\n" + Command(s) + "\n"`. This is fish's whole file.
  - `type State int` with `Absent`, `Installed`, `ByHand`. `func Inspect(content []byte) State`: the content holds `Marker`, else holds `kurze-url completion`, else neither.
  - `func Add(content []byte, s Shell) []byte` returns `content + Block(s)`. The caller has checked `Inspect` first.
  - `func Remove(content []byte) ([]byte, bool)` removes the first exact `"\n"+Marker+"\n"+<line>+"\n"`. If someone edited it, it falls back to removing the marker line and the line after it. It returns `false` when the marker is absent.
  - `func WriteFile(path string, data []byte, perm os.FileMode) error` resolves a symlink to its target (`filepath.EvalSymlinks` when it exists) and writes through `os.CreateTemp` in the target's directory, then `os.Rename`. It keeps an existing file's mode and uses `perm` for a new one, creating missing parent directories with `0o755`.

- [ ] **Step 1: Write the failing tests**

```go
func TestAddThenRemoveRestoresTheFile(t *testing.T) {
	for _, original := range []string{"", "export A=1\n", "export A=1" /* no trailing newline */} {
		added := Add([]byte(original), Zsh)
		require.Contains(t, string(added), "\n"+Marker+"\n")
		removed, ok := Remove(added)
		require.True(t, ok)
		require.Equal(t, original, string(removed), "%q", original)
	}
}

func TestBlockStartsOnALineOfItsOwn(t *testing.T) {
	got := string(Add([]byte("export A=1"), Bash))
	require.Equal(t, "export A=1\n"+Marker+"\n"+Command(Bash)+"\n", got)
}

func TestInspect(t *testing.T) {
	require.Equal(t, Absent, Inspect([]byte("export A=1\n")))
	require.Equal(t, Installed, Inspect(Add(nil, Zsh)))
	require.Equal(t, ByHand, Inspect([]byte("source <(kurze-url completion zsh)\n")))
}

func TestRemoveSurvivesAnEditedBlock(t *testing.T) {
	edited := "a\n" + Marker + "\nsomething else\nb\n"
	got, ok := Remove([]byte(edited))
	require.True(t, ok)
	require.Equal(t, "a\nb\n", string(got))
}

func TestRemoveWithoutMarker(t *testing.T) {
	_, ok := Remove([]byte("source <(kurze-url completion zsh)\n"))
	require.False(t, ok)
}

func TestWriteFileKeepsModeAndFollowsSymlinks(t *testing.T) {
	// a 0600 target behind a symlink: after WriteFile the link is still a link,
	// the target holds the data and still has mode 0600 (Review Focus 2)
}

func TestWriteFileCreatesDirectories(t *testing.T) {
	// a path under two missing directories is created with perm
}

func TestParseShell(t *testing.T) {
	// zsh, bash, fish, powershell, and pwsh => PowerShell; sh and "" are not shells
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd apps/cli && go test ./internal/shellrc -count=1` Expected: FAIL. The package does not exist yet.

- [ ] **Step 3: Implement** `shellrc.go` with the interfaces above.

- [ ] **Step 4: Run the tests**

Run: `cd apps/cli && go test -race -count=1 ./internal/shellrc && GOOS=windows GOARCH=amd64 go build ./...` Expected: PASS, and the Windows build succeeds.

- [ ] **Step 5: Commit**: `feat(cli): edit shell config files for completion`

---

### Task 4: `completion install` and `completion uninstall`

**Files:**

- Create: `apps/cli/internal/cli/completion_cmds.go`, `apps/cli/internal/cli/completion_cmds_test.go`
- Modify:
  - `apps/cli/internal/cli/root.go`: add both subcommands to cobra's `completion` group after `InitDefaultCompletionCmd`, in the existing loop;
  - `deps.go`: new fields and `Default`;
  - `harness_test.go`: give the new `Deps` fields test values;
  - `internal/i18n/locales/en.json`, `de.json`.

**Interfaces:**

- Consumes: `shellrc` (Task 3); `App.notice`, `App.result`, `Deps.StdinTTY`, the prompt style of `askTeam` (`auth_cmds.go`).
- Produces, as new `Deps` fields set in `Default`:
  - `GOOS string` (`runtime.GOOS`);
  - `HomeDir func() (string, error)` (`os.UserHomeDir`);
  - `LookPath func(string) (string, error)` (`exec.LookPath`);
  - `RunCommand func(ctx context.Context, name string, args ...string) ([]byte, error)`: `exec.CommandContext(...).Output()` with a 10-second timeout.

  The harness sets `HomeDir` to a temporary directory, `GOOS` to `"linux"`, `LookPath` to "not found", and `RunCommand` to a recorder the test programs.

- Commands:
  - `install [zsh|bash|fish|powershell|pwsh]` with `--yes`;
  - `uninstall [zsh|bash|fish|powershell|pwsh]` with `--yes`;
  - both take `Args: usageArgs(cobra.MaximumNArgs(1))`, and `ValidArgsFunction` lists the four shell names.
- **Shell:** the argument; else, on `GOOS == "windows"`, `powershell`; else `shellrc.ParseShell(filepath.Base(Getenv("SHELL")))`. When there is none, the error `completion.unknownShell` names the four and the argument form. Its exit code is 2 when the argument is wrong (`UsageError`), and 1 when `$SHELL` is unknown.
- **Targets:**

  | Shell | File |
  | --- | --- |
  | zsh | `Getenv("ZDOTDIR")` or home, plus `.zshrc` |
  | bash | home plus `.bashrc` |
  | fish | `Getenv("XDG_CONFIG_HOME")` or home plus `.config`, then `fish/completions/kurze-url.fish` (own file) |
  | powershell | for each of `pwsh`, and also `powershell` when `GOOS == "windows"`, found by `LookPath`: `RunCommand(name, "-NoProfile", "-NonInteractive", "-Command", "$PROFILE.CurrentUserCurrentHost; Get-ExecutionPolicy")`. Line 1 is the profile path, line 2 the policy. None found gives `completion.noPowerShell` |

- **Install flow:**
  1. Read every target; a missing file reads as empty.
  2. Every target `Installed`: `completion.alreadyInstalled`, exit 0.
  3. Any target `ByHand`: `completion.byHand` naming the file, and that target is skipped.
  4. Otherwise ask `completion.confirmInstall` (shell, files joined by `, `). Without `--yes` and with `!StdinTTY()`: `completion.needsYes`, exit 1, nothing written.
  5. Answer: empty, `y`, `yes`, `j`, `ja` (case-insensitive, trimmed) is yes. Anything else is no: `completion.declined`, exit 0, nothing written.
  6. Write with `shellrc.WriteFile`: rc files `0o644`, the fish file `0o644`.
  7. Report `completion.installed` per file, then the shell's next step:
     - zsh, bash: `completion.reloadSource` (`source <file>`);
     - fish: `completion.reloadNewShell`;
     - powershell: `completion.reloadProfile` (`. '<file>'`).
  8. Add `completion.bashPackage` for bash. Add `completion.policyBlocks` when a profile's policy is `Restricted` or `AllSigned`.
- **Uninstall flow:** the same targets and the same question, `completion.confirmUninstall`. `shellrc.Remove`, or for fish delete the file when `Inspect` is `Installed`. Nothing marked gives `completion.nothingToRemove`, exit 0. A `ByHand` target gives `completion.byHandLeft` and is left alone.
- **i18n keys** (en and de, `{placeholders}` in both):

  | Key                           | Placeholders         |
  | ----------------------------- | -------------------- |
  | `completion.unknownShell`     | `{shell}`            |
  | `completion.noPowerShell`     |                      |
  | `completion.confirmInstall`   | `{shell}`, `{files}` |
  | `completion.confirmUninstall` | `{files}`            |
  | `completion.needsYes`         |                      |
  | `completion.declined`         |                      |
  | `completion.alreadyInstalled` | `{files}`            |
  | `completion.byHand`           | `{file}`             |
  | `completion.byHandLeft`       | `{file}`             |
  | `completion.installed`        | `{file}`             |
  | `completion.reloadSource`     | `{file}`             |
  | `completion.reloadNewShell`   |                      |
  | `completion.reloadProfile`    | `{file}`             |
  | `completion.bashPackage`      |                      |
  | `completion.policyBlocks`     | `{file}`, `{policy}` |
  | `completion.removed`          | `{file}`             |
  | `completion.nothingToRemove`  |                      |

  The prompts end with `[Y/n] ` in English and `[J/n] ` in German, with no newline.

- [ ] **Step 1: Write the failing tests** in `completion_cmds_test.go`. Each uses the harness, with `HomeDir` a temporary directory and `SHELL` set through `h.env`.

```go
func TestInstallAsksAndWritesTheZshBlock(t *testing.T)   // SHELL=/bin/zsh, existing ~/.zshrc "export A=1" (no newline), stdin TTY, answer "\n": file == "export A=1\n"+Marker+"\n"+Command(Zsh)+"\n"; prompt shown; exit 0
func TestInstallHonoursZDOTDIR(t *testing.T)            // ZDOTDIR=<tmp>/z: writes <tmp>/z/.zshrc, home untouched (Review Focus 3)
func TestInstallTwiceWritesOnce(t *testing.T)            // --yes twice: one marker; second run prints alreadyInstalled, asks nothing
func TestInstallLeavesAHandMadeSetupAlone(t *testing.T)  // .zshrc holds "source <(kurze-url completion zsh)": unchanged, byHand message, exit 0
func TestInstallDeclined(t *testing.T)                   // answer "n": file bytes and mtime unchanged, declined message, exit 0 (Review Focus 1)
func TestInstallWithoutTerminalNeedsYes(t *testing.T)    // StdinTTY false, no --yes: exit 1, needsYes, file not created (Review Focus 1)
func TestInstallAcceptsJa(t *testing.T)                  // LANG=de_DE.UTF-8, answer "j": written; prompt ends with "[J/n] "
func TestInstallFishWritesItsOwnFile(t *testing.T)       // SHELL=/usr/bin/fish, XDG_CONFIG_HOME set: <xdg>/fish/completions/kurze-url.fish == OwnFile(Fish)
func TestInstallBashMentionsThePackage(t *testing.T)     // bash: ~/.bashrc written, bashPackage text present
func TestInstallPowerShellWritesEveryProfile(t *testing.T) // GOOS=windows, LookPath finds pwsh and powershell, RunCommand returns "<tmp>/a.ps1\nRemoteSigned" and "<tmp>/b.ps1\nRestricted": both files get the block, one prompt lists both, policyBlocks names b.ps1
func TestInstallUnknownShell(t *testing.T)               // SHELL=/bin/tcsh: exit 1, unknownShell; argument "tcsh": exit 2
func TestUninstallRestoresTheFile(t *testing.T)          // install then uninstall --yes: .zshrc byte-for-byte as before (trailing-newline-less original)
func TestUninstallFishDeletesItsFile(t *testing.T)       // the file is gone; a fish file without the marker is left and byHandLeft printed
func TestUninstallWithNothingToRemove(t *testing.T)      // exit 0, nothingToRemove, no prompt
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd apps/cli && go test ./internal/cli -run 'Install|Uninstall' -count=1` Expected: FAIL. `unknown command "install" for "kurze-url completion"`, exit 2.

- [ ] **Step 3: Implement** the commands, the `Deps` fields, the harness defaults and both catalogs.

- [ ] **Step 4: Run the whole module's checks**

Run: `cd apps/cli && go vet ./... && test -z "$(gofmt -l .)" && go test -race -count=1 ./... && GOOS=windows GOARCH=amd64 go build ./... && GOOS=darwin GOARCH=arm64 go build ./...` Expected: all succeed, including `TestEveryUsedKeyIsDefined` and `TestCatalogsHaveTheSameKeys`.

- [ ] **Step 5: Manual smoke** in a scratch `HOME`:

  ```bash
  HOME=$(mktemp -d) SHELL=/bin/zsh go run ./cmd/kurze-url completion install
  ```

  Answer Enter, then `cat` the file. Then run `completion uninstall --yes`.

- [ ] **Step 6: Commit**: `feat(cli): add completion install and uninstall`

---

### Task 5: Install scripts, README and docs

**Files:**

- Modify:
  - `apps/cli/install.sh`, `apps/cli/scripts/install_test.sh`;
  - `apps/cli/install.ps1`, `apps/cli/scripts/install_test.ps1`;
  - `README.md`, `CLAUDE.md`;
  - `docs/superpowers/specs/2026-10-23-cli-design.md`.

**Interfaces:**

- Consumes: the command names from Task 4.

- [ ] **Step 1: Failing installer tests**
  - `install_test.sh`: a case asserting that stderr holds `kurze-url completion install` after a successful install, in English, and with `LANG=de_DE.UTF-8` the German line. Stdout is still exactly the version line.
  - `install_test.ps1`: an `Invoke-Case 'suggests_completion'` with `Assert-Output -Text 'kurze-url completion install'`. The existing "stdout ends with the version line" cases must stay green.

- [ ] **Step 2: Run to see them fail**

Run: `cd apps/cli && sh scripts/install_test.sh` and `pwsh -NoProfile -File scripts/install_test.ps1` Expected: the new cases FAIL, the rest pass.

- [ ] **Step 3: Implement**
  - `install.sh`: a message key `completion_hint` in both languages:
    - en: `To turn on tab completion, run: kurze-url completion install`
    - de: `Um die Tab-Vervollständigung einzuschalten, führe aus: kurze-url completion install`

    Print it through `msg` (stderr) as the last thing `main` does, after the PATH hint.

  - `install.ps1`: the same text under a `completion_hint` key in both message tables, printed with `Write-Host` last. `Write-Host`, never output, keeps stdout to the version line.

- [ ] **Step 4: Run both harnesses**

Run: `cd apps/cli && sh scripts/install_test.sh && pwsh -NoProfile -File scripts/install_test.ps1` Expected: all cases `ok`. The Windows-only cases are skipped on macOS and Linux.

- [ ] **Step 5: Docs**
  - **`README.md`:** a `### Tab completion` part in the "Command-line tool" section, after "First steps":
    - `kurze-url completion install` (asks first, `--yes` to skip) and `kurze-url completion uninstall`;
    - the line to add by hand per shell: zsh `source <(kurze-url completion zsh)`, bash `eval "$(kurze-url completion bash)"`, fish `kurze-url completion fish | source` in `~/.config/fish/completions/kurze-url.fish`, PowerShell `kurze-url completion powershell | Out-String | Invoke-Expression` in `$PROFILE`;
    - one sentence on the dynamic values: teams, folders, domains and slugs need a `login`, and Tab offers nothing when signed out or offline.
  - **`CLAUDE.md`:** in the CLI entry ("The CLI is a thin `/v1` client…"), a bold-led part:
    - completion never prompts, prints or blocks, and gives up after `completionTimeout` (2 s);
    - it may refresh the token inside that budget, an accepted risk (spec);
    - `completion install` asks, writes a marked block (`shellrc.Marker`) and is idempotent, and `uninstall` removes exactly that block;
    - the block runs `completion <shell>` at shell start, so an update needs no reinstall.
  - **CLI spec (`2026-10-23-cli-design.md`):**
    - the command block near `kurze-url completion bash|zsh|fish|powershell` gains `kurze-url completion install [shell]` and `kurze-url completion uninstall [shell]`;
    - the "Testing" manual checklist gains a step: for zsh, bash, fish and PowerShell, `completion install`, open a new shell, check that `kurze-url <Tab>`, `kurze-url link qr <Tab>` and a folder with a space complete, then `completion uninstall`.

- [ ] **Step 6: Verify formatting**

Run from the repository root: `pnpm format && git diff --stat` Expected: only the intended files changed.

- [ ] **Step 7: Commit**: `docs(cli): document tab completion`. It includes the two installer changes, or those go as a separate `feat(cli): suggest completion after installing` commit before it.
