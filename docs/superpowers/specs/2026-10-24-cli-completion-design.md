# kurze-url CLI: tab completion — Design

**Status:** agreed in conversation on 2026-10-24, awaiting review of this written form.

**Amends:**

- `README.md`: the "Command-line tool" section gets a "Tab completion" part.
- `CLAUDE.md`: the CLI entry under "Non-obvious constraints" says what `completion install` writes, and that completion never blocks, prompts or prints errors.
- `docs/superpowers/specs/2026-10-23-cli-design.md`: its command list gains `completion install` and `completion uninstall`, and its manual checklist ("Testing") a completion step for each shell.

## Why

The maintainer typed `kurze-url` and `kurze-url lin` in zsh on macOS, pressed Tab and got nothing. They expected the list of possible entries.

Command and flag completion already exists. Cobra's `kurze-url completion bash|zsh|fish|powershell` prints a working script (`internal/cli/root.go`, `InitDefaultCompletionCmd`), and `kurze-url __complete ""` lists every command. Nobody can find it, though: the README never mentions it, and neither install script sets it up. Beyond commands, Tab completes no values at all. After `--format`, a slug or a team, zsh falls back to file names.

## Goal

After one `kurze-url completion install`, Tab completes:

- commands and flags (this already works);
- fixed values: config keys, `language`, `qr.format` and `--format`;
- values the CLI has to ask kurze-url for: teams, folders, domains and link slugs.

This works in zsh, bash, fish and PowerShell.

## Non-goals

- The install scripts do not run `completion install` themselves. Under `curl … | sh` the script is stdin, so there is nobody to ask, and the maintainer wants to be asked.
- No cache of dynamic values. Each Tab asks the API again.
- No completion of short URLs (`go.kurze-url.app/sommerfest`). Slugs only.
- No Homebrew or Scoop packages (still deferred, see `CLAUDE.md`).

## Anti-goals

These would make it a failure even if it technically worked:

- **Completion that interrupts.** A Tab that prints an error, opens a browser, asks for `login`, or hangs the shell for more than about two seconds.
- **Config written without asking.** A shell configuration file changed without the person agreeing first.
- **Duplicate entries.** A second `completion install` that adds the line twice.
- **Leftovers.** An `uninstall` that leaves its block behind or touches anything else.

## Values completed

### Fixed (no network)

| Where | Values |
| --- | --- |
| `config get`, `config set`, `config unset`: the key | every key from `config.Keys()`, in that order |
| `config set language` | `en`, `de` |
| `config set qr.format`, `link qr --format` | `svg`, `png` |
| `--size`, `--fg`, `--bg`, `--limit`, `--slug`, `--search`, `link create`'s URL, `config set qr.size\|qr.fg\|qr.bg` | nothing, and no file names either (`ShellCompDirectiveNoFileComp`) |
| `link qr --output` | file names (unchanged) |

### Dynamic (through the API)

| Where | Values | Shown beside each value |
| --- | --- | --- |
| every `--team`, `config set team` | the slugs of the person's memberships (`GET /v1/me`) | the team's name |
| every `--folder`, `config set folder` | the team's folder names | nothing |
| every `--domain`, `config set domain` | the team's verified domains, plus the shared host (`go.kurze-url.app` on Production) | nothing |
| `link qr <slug>`, `link stats <slug>` | slugs of the team's links that start with what was typed | the destination URL |

**Which team:** a `--team` already on the command line wins, otherwise the default team from the config. That is `resolveTeam`'s rule. `config set folder|domain` is the exception: the command itself refuses those keys without `--team` (`CodeKeyNeedsTeam`) and has no default-team fallback, so Tab offers their values only when `--team` is on the line (my call: offering the default team's values would suggest a command that then fails). With no team at all, folder, domain and slug completion offer nothing, and so does a `--team` naming a team the person is not a member of. Team completion needs no team.

**Slugs:** `ListLinks` with `q` set to the typed text, then only the slugs that start with it. The API's `q` is a substring match over slug and destination, so the filter happens in the CLI. One page of the API's maximum, 100, is enough. A typed word that contains `/` or `:` is a short URL being typed and gets nothing.

**Descriptions:** Cobra's `value\tdescription` form. zsh, fish and PowerShell show the description, bash shows only values.

**Values that are not offered:** a value holding a control character, or any of `$`, a backtick, `\`, `;`, `&`, `|`, `<`, `>` and `!`. A control character would reach the terminal or break the protocol's lines and tabs. The other nine are shell syntax: of several candidates, Cobra's bash script inserts the part they share unescaped, so a folder name anybody in the team chose would land on the command line as code. `x$(cmd)a` beside `x$(cmd)b` is a live command substitution. `x;touch pwned;a` beside `x;touch pwned;b` puts `x;touch pwned;` there, and the next Enter runs it; `&` and `|` work the same way, `<` and `>` redirect (and can truncate a file), and `!` triggers history expansion. Parentheses, quotes and spaces stay offered: without a command separator they cannot run code, and a folder such as "Sommerfest (2026)" must still complete. The trade-off is that a folder named with one of the dropped characters, such as "Kasse & Beiträge", is not offered by Tab and has to be typed by hand. Decided by the maintainer on 2026-10-24: first for `$`, the backtick and the backslash after the final review, then for the other six after a re-review found the separators, redirections and history expansion. A description may hold all of them, because it is shown and never inserted, and it is cleaned, not dropped.

## How completion behaves when it cannot answer

The rule behind every case below: completion is silent, fast and never interactive.

- **No usable session** (`ErrLoginRequired`, no stored tokens): it offers nothing. A browser login never happens.
- **An expired access token:** completion refreshes it with the stored refresh token, inside the time budget below (the maintainer's decision). The risk was weighed and accepted: on a very slow network the server may rotate the refresh token while the client has already given up, and the new token is then never saved. One such loss is absorbed, because the server still accepts the stored token as the parent of the newest one and `Session` re-reads the store. Two in a row leave a grandparent, and the next command asks for `login`.
- **A broken config, a network error, any API error, a 401 after refresh:** it offers nothing. Nothing reaches the terminal: no stderr output, no notice, no hint.
- **Time budget:** one deadline of about 2 seconds for the whole completion call, covering the refresh and every request. Once it passes, it offers nothing. Keychain reads do not honour a context and can block (a locked keychain), so the work runs off the main goroutine and the process answers empty when the deadline passes, whatever is still running (my call).
- **Directive:** an empty dynamic answer is `ShellCompDirectiveNoFileComp`, so zsh does not fall back to file names.

The App is built lazily inside the completion function, the way commands build it. Its failures are swallowed there, and they never reach `describe`.

## `kurze-url completion install [shell]` and `uninstall [shell]`

Two new subcommands under Cobra's existing `completion` group. The four script subcommands (`completion zsh` and so on) stay as they are.

**Shell:** the argument, else read from the environment. On macOS and Linux that is the base name of `$SHELL`. On Windows it is `powershell`. A shell that is not one of the four, or cannot be told, fails with a message naming the four and the argument form.

**What it writes:**

| Shell | Where | What |
| --- | --- | --- |
| zsh | `${ZDOTDIR:-$HOME}/.zshrc` | a marked block at the end that sources `kurze-url completion zsh` at shell start. It runs `autoload -Uz compinit && compinit -i` first when `compdef` is not defined yet, because a `.zshrc` without `compinit` would otherwise get a block that does nothing (my call), and sources the script only when `compdef` exists then. `-i` and the second check were decided on 2026-10-24, after the final review reproduced the question a bare `compinit` asks at every shell start ("insecure directories … [y/n]") when an `fpath` directory is group- or world-writable, as a Homebrew shared between accounts leaves one; without a terminal it aborts, and the script then printed `command not found: compdef`. A missing `kurze-url` on `PATH` prints nothing |
| bash | `~/.bashrc` | a marked block at the end that sources `kurze-url completion bash`, guarded the same way |
| fish | `${XDG_CONFIG_HOME:-~/.config}/fish/completions/kurze-url.fish` | a file of its own, carrying the marker, that runs `kurze-url completion fish \| source` |
| PowerShell | the current user's all-hosts profile (`$PROFILE.CurrentUserAllHosts`), as PowerShell itself reports it. All hosts and not the current one (decided 2026-10-24, after the final review): VS Code's PowerShell terminal and the ISE load profiles of their own, and every host loads this one | a marked block at the end that runs `kurze-url completion powershell \| Out-String \| Invoke-Expression` |

The block is generated at shell start, never pasted in as a snapshot. A CLI update then brings its own completions along, with no second install.

The PowerShell profile path must come from PowerShell, not be assembled in Go. `Documents` is often redirected (OneDrive), and Windows PowerShell 5.1 and PowerShell 7 keep separate profiles. `install` cannot tell which edition the person uses, so `powershell` means every edition found on `PATH` (`pwsh`, and on Windows `powershell`). It asks each one for `$PROFILE.CurrentUserAllHosts` and writes the block into each profile, with one question that lists all the files (my call). A `$SHELL` ending in `pwsh` on macOS or Linux counts as `powershell`. On Windows, when the effective execution policy is `Restricted` or `AllSigned`, a profile is never loaded, so `install` says so after writing. It does not change the policy. Asking PowerShell means starting a process, so `Deps` gains a way to run one, and tests fake it. A PowerShell that fails is named, with the first line of what it wrote to stderr as the reason.

**The marker** is a comment line naming the tool, for example `# kurze-url completion (added by kurze-url completion install)`, followed by the block. It is how a second `install` recognises an existing block, and how `uninstall` finds what to remove.

**A setup made by hand** has no marker: a `source <(kurze-url completion zsh)` line someone added themselves, or a fish file written with Cobra's own `kurze-url completion fish > …` advice. When `install` finds `kurze-url completion` already in the target file, or a fish file without the marker, it reports that completion is already set up, writes nothing and exits 0. `uninstall` leaves such setups alone and says so (my call).

**Asking first (the maintainer's decision):**

1. `install` shows the file and asks, in the person's language, "Add tab completion for zsh to /Users/…/.zshrc? [Y/n]". Enter means yes.
2. `--yes` skips the question.
3. Without a terminal on stdin and without `--yes`, it refuses and names `--yes`, because nobody can answer.
4. When the block is already there, it says so, asks nothing and exits 0.

**After writing**, it names the file and says that a new shell (or `source '<file>'`) turns completion on. For bash it also says that bash's completion needs the `bash-completion` package. This repeats Cobra's own note. On macOS it adds, for bash, that Terminal and iTerm open login shells, which read `~/.bash_profile`, so that file has to source `~/.bashrc` for the block to load (decided 2026-10-24, after the final review).

**`uninstall`** asks the same way, removes exactly the marked block, or the fish file when it carries the marker, and leaves every other byte of the file alone. When there is nothing to remove, it says so, names the files it checked and how to name another shell, and exits 0, because the shell came from `$SHELL` and the person may have installed for another one.

**Writing safely:** the file is rewritten through a temporary file in the same directory and an atomic rename, keeping its permissions. A file that does not exist yet is created (for zsh, bash, PowerShell, and the fish completions directory).

**Language:** prompts and results go through the CLI's i18n catalogs (`internal/i18n/locales/en.json`, `de.json`) like everything else a command prints. Help texts stay English, as for every command.

## Install scripts and README

- **`install.sh`** ends with a line suggesting `kurze-url completion install` to turn on tab completion. It changes no shell file, as before.
- **`install.ps1`** prints the same suggestion. It already edits the user `Path` (decided 2026-10-24), and it still does not touch the profile.
- **README, "Tab completion":**
  - `kurze-url completion install` as the way in, and `completion uninstall` as the way out;
  - the line to add by hand per shell, for people who keep their own configuration;
  - one sentence on what completes dynamically, and that it needs a `login`.

## Constraints

- Everything here lives in `apps/cli`, its own Go module. The CLI talks to the API over HTTP only, so no API change is needed: every value comes from endpoints the CLI already calls (`Me`, `Folders`, `Domains`, `ListLinks`). `internal/apiclient/contract_test.go` therefore covers them already.
- **Windows:** CI cross-builds the Windows target, and `install script (cli, windows)` runs `install.ps1`'s harness under Windows PowerShell 5.1 and 7. The suggestion line must keep both green.
- **Performance:** completion runs on every Tab press. It must not load anything the answer does not need. Fixed values never touch the network.

## Left to the builder

- **Names:** of functions and files, and the exact wording of the marker and messages. The rules above still apply.
- **Prompt input:** whether `[Y/n]` also accepts the German `j`, so the hint can read `[J/n]` in German.
- **Wiring:** how completion functions reach the lazily built App, and how tests fake the API. Follow the existing command tests.
- **Bash on macOS:** how far bash's ancient system version (3.2) is supported. At least, the command must not break the person's `.bashrc`.

## Testing

Go tests pin the `__complete` output, both fixed and dynamic, against a faked API. They also pin the block text, and `install`/`uninstall` on temporary files for each shell. The real shells, including zsh's and bash's quoting of a value with a space, are checked by hand: the manual checklist in the CLI spec gains a completion step for each of the four shells. No CI job starts a real shell (my call: that would cost four jobs and flakiness for what Cobra's generated scripts already handle).

## Review focus

The inputs most likely to bite a person, which a plan's tests should pin:

1. **A folder name with a space** (`Sommerfest 2026`) must come out of `__complete` as one candidate, and complete as one quoted word in zsh and bash (the manual check).
2. **A `.zshrc` without a trailing newline:** the block starts on a line of its own, and `uninstall` restores the file byte for byte.
3. **`install` run twice, then `uninstall` once:** no block left, nothing else changed.
4. **Completion while signed out or offline:** no output on stderr, nothing on stdout but the completion protocol, no browser, and it returns within the budget.
5. **`--team` on the line naming a team the person is not a member of:** folder, domain and slug completion offer nothing rather than another team's values.
