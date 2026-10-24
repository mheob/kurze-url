# CLI Part 4 — Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A SemVer tag on `main` publishes the `kurze-url` binaries for six platforms to a GitHub Release. A person installs the binary with one command that checks the download's checksum: `curl … | sh` on Linux and macOS, `irm … | iex` in PowerShell on Windows. The README and the planning documents describe what was built.

**Architecture:**

- **Builds:** goreleaser builds `apps/cli` from `apps/cli/.goreleaser.yaml`.
- **Releases:** `release-cli.yml` runs goreleaser for real on a `v*` tag.
- **Pull requests:** `ci-cli.yml` gains three jobs.
  - A **release dry run** checks the config, builds a snapshot, and installs the snapshot with `install.sh` to prove the two agree. It hands the Windows archives to the Windows job as an artifact.
  - An **install-script** job runs `install.sh`'s own tests on Linux and macOS.
  - A **Windows install-script** job runs `install.ps1`'s own tests under Windows PowerShell 5.1 and PowerShell 7, then installs the snapshot's Windows archive.
- **Install scripts:** `apps/cli/install.sh` and `apps/cli/install.ps1`, both fetched from `main` through raw.githubusercontent. Both download from GitHub's `releases/latest/download/<asset>`, so asset names carry no version.

**Tech Stack:** goreleaser v2.19.0 through `goreleaser/goreleaser-action` v7.3.0, POSIX `sh` with shellcheck (preinstalled on GitHub's Ubuntu runners), PowerShell 5.1 and 7 with PSScriptAnalyzer (preinstalled on GitHub's Windows runners), and GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-23-cli-design.md`, **Part 4**, plus its "Amends" list (the planning docs and the README) and the release-candidate checklist under "Testing". Parts 1–3 are on `main`. Part 3 landed in #148 (`d764e8a6`): `apps/cli`, `ci-cli.yml`, and the CLI entry in CLAUDE.md.

## Global Constraints

- **Git:** use GitButler only, on branch `feat/cli-release` from `main`, with `but commit -b feat/cli-release -m "…" <ids>`.
  - Commit messages follow Conventional Commits with a subject of at most 50 characters and no co-author or generator footer.
  - Run `pnpm format` before every commit, because `but commit` skips Lefthook. It formats the YAML and Markdown.
  - Never run a git write command, a tag included.
- **Spec, Part 4:**
  - goreleaser with `apps/cli/.goreleaser.yaml` builds linux, darwin and windows for amd64 and arm64. `version`, the commit and the date go in through ldflags.
  - It publishes binaries, archives and `checksums.txt` to a GitHub Release using `GITHUB_TOKEN`.
  - Actions are pinned by SHA like every other workflow.
  - The first release is `v0.1.0`, and tagging it is the maintainer's call.
- **Spec, install script:** `apps/cli/install.sh`, fetched from `main` through raw.githubusercontent.
  - It detects OS and architecture.
  - It downloads the latest release's archive and `checksums.txt` and verifies the checksum, refusing to continue on a mismatch.
  - It installs to `~/.local/bin`, or `$KURZE_URL_INSTALL_DIR`.
  - On Windows the README points to the release page.
- **Windows (maintainer's decision, 2026-10-24, amending the spec's last line above):** Windows gets `apps/cli/install.ps1` as well, run as `irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex`.
  - It **adds the install directory to the user's `Path`** by itself, persistently and for the current session (option A), because Windows users rarely edit `Path` by hand.
  - The release page stays in the README as the manual alternative, so a Windows user can choose.
  - Task 4 records the amendment in the spec's Part 4.
- **Pinned versions:**
  - `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` and `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0`, the same SHAs `ci-cli.yml` uses;
  - `goreleaser/goreleaser-action@a4f02ab5355a589de60d917a659ce617ed19b3de # v7.3.0`, with its input `version: v2.19.0`;
  - `actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2` (the SHA `ci-js.yml` uses) and `actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2`.
- **Asset names** (decided here; the spec leaves archive naming to the builder): `kurze-url_<os>_<arch>.tar.gz`, `.zip` for windows, and `checksums.txt`.
  - There is no version in a name, because `install.sh` downloads `https://github.com/mheob/kurze-url/releases/latest/download/<asset>`, and that URL only works for a stable name.
  - `<os>` is `linux`, `darwin` or `windows`; `<arch>` is `amd64` or `arm64`.
- **Version line:** the ldflags set `main.version={{ .Version }}`, `main.commit={{ .ShortCommit }}` and `main.date={{ .CommitDate }}`.
  - The commit date rather than the build time keeps the build reproducible.
  - `kurze-url version` then prints, for example, `kurze-url 0.1.0 (d764e8a, 2026-10-24T06:49:29Z)`.
- **Release candidates:** a tag with a pre-release suffix, `v0.1.0-rc.1`, becomes a GitHub pre-release (`prerelease: auto`).
  - GitHub's "latest" never points at a pre-release, so `install.sh` keeps installing the last final version while a candidate is being checked.
- **`install.sh`:**
  - It is POSIX `sh` with `set -eu` and is shellcheck-clean.
  - The whole body sits in a `main` function called on the last line, so a truncated `curl | sh` runs nothing.
  - Its messages are English and German, chosen by the CLI's own rule: the first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set, German when it starts with `de`. That is golden rule 6; the CLI's `--help` exception covers generated help text, not an installer's sentences.
- **`install.ps1`:**
  - It runs unchanged under Windows PowerShell 5.1, the default on Windows 10 and 11, and under PowerShell 7.
  - Like `install.sh`, the whole body sits in one function called on the last line.
  - Its messages are English and German by the CLI's Windows rule: `LC_ALL`, `LC_MESSAGES`, `LANG` first, then the UI culture (`(Get-UICulture).TwoLetterISOLanguageName`).
  - It is clean under PSScriptAnalyzer's default rules. A suppression needs a `SuppressMessageAttribute` with a justification.
- **Docs:** the README and the planning documents are English prose in the existing documents' style. The plan file is not rewritten, and the spec changes only where Task 4 says.

## Review Focus

1. **A truncated download of the install script.** A network drop mid-download must not run half a script.
   - Task 2 pipes the first half of `install.sh` into `sh`.
   - Task 3 feeds the first half of `install.ps1` to `Invoke-Expression`.
   - Both assert that nothing is created or downloaded.
2. **The second-class shells.**
   - macOS's BSD userland: `mktemp`, `tar`, `shasum` or `sha256sum`, and `install` all differ from GNU. Task 2's tests run on `macos-latest` as well as Ubuntu.
   - Windows PowerShell 5.1 lacks TLS 1.2 by default on older systems, needs `-UseBasicParsing`, and differs from PowerShell 7 in its web cmdlets. Task 3's tests run under both `powershell` and `pwsh`.
3. **Installing over an existing or running `kurze-url`.**
   - On Linux and macOS, upgrading must replace the binary, not fail with "text file busy": the new file is `mv`ed into place from a temporary file in the same directory.
   - On Windows, a running `.exe` cannot be overwritten but can be renamed: the old one is renamed to `kurze-url.exe.old` before the new one moves in, and the `.old` file is deleted when it can be.
   - Tasks 2 and 3 install twice and check that the second version is in place and no temporary file is left.
4. **`install.sh` in Git Bash on Windows.** `uname -s` gives `MINGW64_NT-…` or `MSYS_NT-…`. The script refuses and points to the PowerShell one-liner and the release page, never installing a Unix binary (Task 2).
5. **`PATH`.**
   - `install.sh` installs, then prints how to add the directory to `PATH`; Task 2 asserts the hint appears exactly when the directory is missing.
   - `install.ps1` adds the directory to the user `Path` once, never twice on a reinstall, ignoring case and a trailing `\`. It also adds it to the current session, so `kurze-url` works in the same window. Task 3 asserts all of that.

---

### Task 1: goreleaser configuration and release workflow

**Files:**

- Create: `apps/cli/.goreleaser.yaml`
- Create: `.github/workflows/release-cli.yml`
- Modify: `.github/workflows/ci-cli.yml`. Add a `release-dry-run` job named "release dry run (cli)", and add `\.github/workflows/release-cli\.yml$` to the change-detection pattern.
- Modify: `apps/cli/cmd/kurze-url/main.go`. Correct the variables' comment: the release config sets the ldflags explicitly, with the short commit and the commit date.

**Interfaces:**

- Produces:
  - Asset names as in Global Constraints.
    - Task 2's `install.sh` derives them as `kurze-url_${os}_${arch}.tar.gz`.
    - Task 3's `install.ps1` derives them as `kurze-url_windows_$arch.zip`.
  - The job id `release-dry-run`, which Task 2 extends with an install step and Task 3 with an artifact upload.

**`.goreleaser.yaml` decisions:**

- `version: 2` and `project_name: kurze-url`.
- One build, `id: kurze-url`:
  - `main: ./cmd/kurze-url` and `binary: kurze-url`;
  - `env: [CGO_ENABLED=0]` and `flags: [-trimpath]`;
  - the ldflags from Global Constraints, plus `-s -w`;
  - `mod_timestamp: "{{ .CommitTimestamp }}"`;
  - `goos: [linux, darwin, windows]` and `goarch: [amd64, arm64]`.
- Archives:
  - `formats: [tar.gz]`, with `format_overrides` giving windows `[zip]`;
  - `name_template: "kurze-url_{{ .Os }}_{{ .Arch }}"`;
  - files: the binary and the repository's `LICENSE` (`../../LICENSE`), with `strip_parent`.
- `checksum.name_template: checksums.txt`, algorithm sha256.
- `snapshot.version_template: "{{ incpatch .Version }}-snapshot"`.
- Changelog, `sort: asc`:
  - include only commits whose subject matches `^\w+\(cli\)!?:`;
  - a monorepo's history is mostly API and web commits, and only `(cli)`-scoped ones describe the binary.
- Release:
  - `prerelease: auto` and `name_template: "kurze-url {{ .Version }}"`;
  - a `footer` with both install one-liners (`install.sh` and `install.ps1`) and a link to the README's "Command-line tool" section (Task 4).

**`release-cli.yml` decisions:**

- Triggers and permissions: `on: push: tags: ['v*']`; workflow `permissions: contents: read` and job `permissions: contents: write`; `concurrency: release-cli-${{ github.ref }}` with `cancel-in-progress: false`.
- Steps, in order:
  1. checkout with `fetch-depth: 0`;
  2. a guard: the tagged commit must be on `main`, checked with `git merge-base --is-ancestor "$GITHUB_SHA" origin/main` after `git fetch origin main`. A tag pushed from an unmerged branch fails here with a message saying so;
  3. setup-go with `go-version-file: apps/cli/go.mod` and `cache-dependency-path: apps/cli/go.sum`;
  4. `go test ./...` in `apps/cli`;
  5. goreleaser-action with `version: v2.19.0`, `args: release --clean` and `workdir: apps/cli`, and `env: GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`.
- The header comment says that tagging is the maintainer's call and that an `-rc.N` tag makes a pre-release.

**`ci-cli.yml` `release-dry-run` job:**

- Its guards match the other CLI jobs: `needs: changes`, `if: needs.changes.outputs.cli == 'true'`, and `working-directory: apps/cli`.
- Steps: checkout with `fetch-depth: 0` (goreleaser reads tags and history), setup-go, `goreleaser check`, then goreleaser `release --snapshot --clean`.
- It ends with a shell step that asserts the snapshot's file set: exactly the six archive names plus `checksums.txt` in `dist/`.

- [ ] **Step 1:** write the shell assertion first, as a script step in the new job, and run the same commands locally to watch it fail before the config exists:
  1. `go run github.com/goreleaser/goreleaser/v2@v2.19.0 release --snapshot --clean` in `apps/cli`;
  2. `command ls apps/cli/dist`, comparing against the expected set.

  Expected: goreleaser fails, because there is no config, or the names differ.

- [ ] **Step 2:** write `.goreleaser.yaml`. Run `go run github.com/goreleaser/goreleaser/v2@v2.19.0 check` in `apps/cli`. Expected: the config is valid, with no deprecation warning.
- [ ] **Step 3:** run the snapshot again, then check four things.
  - `dist/` holds exactly `kurze-url_linux_amd64.tar.gz`, `kurze-url_linux_arm64.tar.gz`, `kurze-url_darwin_amd64.tar.gz`, `kurze-url_darwin_arm64.tar.gz`, `kurze-url_windows_amd64.zip`, `kurze-url_windows_arm64.zip` and `checksums.txt`, plus goreleaser's own metadata files, which the assertion ignores.
  - `tar -tzf` on one archive lists `kurze-url` and `LICENSE` at the top level.
  - The extracted darwin binary for this machine prints `kurze-url <x.y.z-snapshot> (<7-hex>, <RFC 3339 date>)` from `version`.
  - `shasum -a 256 -c checksums.txt` in `dist/` passes.
- [ ] **Step 4:** write `release-cli.yml` and the `ci-cli.yml` job, and fix the `main.go` comment. Checks, each expected to pass or print nothing:
  - `go -C apps/cli test ./...` passes;
  - actionlint, if it is installed (otherwise CI is the check);
  - `pnpm format:check`;
  - `grep -c '@[0-9a-f]\{40\}' .github/workflows/release-cli.yml` counts every `uses:` line.
- [ ] **Step 5:** commit with the message `build(cli): add the goreleaser release`.

### Task 2: The install script and its tests

**Files:**

- Create: `apps/cli/install.sh`, executable (mode `0755` in git)
- Create: `apps/cli/scripts/install_test.sh`, the test harness, executable
- Modify: `.github/workflows/ci-cli.yml`.
  - Add an `install-script` job named "install script (cli, ${{ matrix.os }})" with matrix `os: [ubuntu-latest, macos-latest]`. It runs `shellcheck install.sh scripts/install_test.sh` on Ubuntu only, then `sh scripts/install_test.sh`.
  - Extend `release-dry-run` with one last step that installs the snapshot through `install.sh` and runs it (below).

**Interfaces:**

- Consumes: the asset names and `dist/` from Task 1.
- Produces:
  - **Script contract:** `install.sh` reads `KURZE_URL_INSTALL_DIR` (default `$HOME/.local/bin`) and `KURZE_URL_VERSION` (a tag such as `v0.1.0`; default: latest).
  - **`KURZE_URL_DOWNLOAD_URL`:** the base URL holding `<asset>` and `checksums.txt`. The default is `https://github.com/mheob/kurze-url/releases/latest/download`, or `…/releases/download/$KURZE_URL_VERSION` when a version is given; when set, it wins. It exists for mirrors and for the tests.
  - **Exit status:** 0 on success, 1 on any failure.
  - **Output:** messages go to stderr; the closing line on stdout is the installed `kurze-url version` output.

**`install.sh` behaviour (spec plus Review Focus):**

1. **OS and architecture.**
   - OS from `uname -s`: `Linux` is `linux` and `Darwin` is `darwin`.
     - `MINGW*`, `MSYS*` or `CYGWIN*` refuse with a message showing the PowerShell one-liner (`irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex`) and the release page `https://github.com/mheob/kurze-url/releases/latest`.
     - Anything else is refused as unsupported.
   - Architecture from `uname -m`: `x86_64` and `amd64` are `amd64`, `arm64` and `aarch64` are `arm64`, and anything else is refused.
2. **Tools.**
   - Downloads use `curl -fsSL`, else `wget -qO`, else the script refuses.
   - Hashing uses `sha256sum`, else `shasum -a 256`, else it refuses.
   - It also requires `tar`.
3. **Download.** It works in `mktemp -d`, removed by a `trap` on `EXIT`. It downloads the archive and `checksums.txt` there; a failed download refuses and names the URL.
4. **Checksum.** It finds the line for the archive name in `checksums.txt` and refuses when there is none. It compares hashes and refuses on a mismatch, before anything is extracted or installed.
5. **Install.**
   - It extracts `kurze-url` only and creates the install directory with `mkdir -p`.
   - It copies the binary to a temporary name inside the install directory, `chmod 0755`, then `mv`s it over `kurze-url`. That is atomic, and safe over a running binary.
6. **Report.**
   - It prints the installed path, then runs `"$dir/kurze-url" version` and prints its output on stdout.
   - When the directory is not among `PATH`'s entries, it prints how to add it: `export PATH="<dir>:$PATH"` in the shell's profile.
7. **Language.** Messages are English and German by the rule in Global Constraints, through one `msg <key>` function, so no sentence is written twice in the body.

**Test harness `scripts/install_test.sh`:**

- It is POSIX `sh` and needs no network. Each case gets its own temp dir, which serves as `HOME` and the install directory.
- **Fixture:** a release dir holding a fake `kurze-url` (a shell script printing `kurze-url 9.9.9 (abc1234, 2026-10-24T00:00:00Z)`), packed as `kurze-url_<os>_<arch>.tar.gz` for the runner's platform, plus a matching `checksums.txt`. `KURZE_URL_DOWNLOAD_URL` is `file://<fixture dir>`.
- **Platform simulation:** a `uname` shim placed first on `PATH` takes `FAKE_UNAME_S` and `FAKE_UNAME_M`.
- **Output:** each case prints `ok <name>` or `FAIL <name>: <reason>`. The script exits 1 if any case failed.

- [ ] **Step 1: Write the failing tests** as cases in `install_test.sh`:
  - `installs_into_the_given_dir`: the binary exists, has mode 755 and runs; stdout's last line is the fake version line.
  - `defaults_to_home_local_bin`.
  - `refuses_a_checksum_mismatch`: one byte of the archive is altered; the exit is 1; nothing is in the install dir; stderr names the mismatch.
  - `refuses_an_archive_missing_from_checksums`.
  - `refuses_a_failed_download`: the archive is absent from the fixture; the exit is 1; nothing is installed.
  - `maps_architectures`: `x86_64` gives `amd64` and `aarch64` gives `arm64`. Each run installs from the fixture packed under that name.
  - `refuses_an_unknown_architecture` (`i686`) and `refuses_an_unknown_os` (`FreeBSD`).
  - `points_git_bash_to_powershell`: `MINGW64_NT-10.0` gives exit 1, and stderr contains both `install.ps1 | iex` and `releases/latest`.
  - `reinstalls_over_an_existing_binary`: two runs with different fake versions; the second version is in place and the directory holds only `kurze-url`. This is Review Focus 3.
  - `hints_when_the_dir_is_not_on_path` and `does_not_hint_when_it_is`. This is Review Focus 5.
  - `speaks_german_with_a_german_locale`: `LANG=de_DE.UTF-8` makes the mismatch message German, and `LC_ALL=C` with `LANG=de_DE` keeps it English.
  - `uses_the_version_in_the_url`: a `curl` shim records its arguments and fails. With `KURZE_URL_VERSION=v0.1.0` and no `KURZE_URL_DOWNLOAD_URL`, the recorded URL contains `/releases/download/v0.1.0/kurze-url_`.
  - `a_truncated_script_does_nothing`: the first half of `install.sh`, by line count, piped into `sh` with a recording `curl` shim. The shim is never called and nothing is created. This is Review Focus 1.
- [ ] **Step 2:** run `sh apps/cli/scripts/install_test.sh` against a stub `install.sh` that only exits 1. Expected: every case prints `FAIL` except the truncated-script one, and the exit is 1.
- [ ] **Step 3:** write `install.sh`.
- [ ] **Step 4:** run `sh apps/cli/scripts/install_test.sh`. Expected: every case prints `ok`, and the exit is 0. Then run `shellcheck apps/cli/install.sh apps/cli/scripts/install_test.sh` if shellcheck is installed (CI is the check otherwise). Expected: no findings.
- [ ] **Step 5:** add the `install-script` job. Extend `release-dry-run` with an end-to-end step:
  1. `KURZE_URL_DOWNLOAD_URL="file://$PWD/dist" KURZE_URL_INSTALL_DIR="$RUNNER_TEMP/bin" sh install.sh`;
  2. assert that the output's last line starts with `kurze-url ` and contains `-snapshot`.

  This proves that the asset names `install.sh` derives are the names goreleaser writes.

- [ ] **Step 6:** run `pnpm format:check` and `go -C apps/cli test ./...`, then commit with the message `feat(cli): add the install script`.

### Task 3: The Windows install script and its tests

**Files:**

- Create: `apps/cli/install.ps1`
- Create: `apps/cli/scripts/install_test.ps1`, the test harness
- Modify: `.github/workflows/ci-cli.yml`.
  - `release-dry-run` gains a last step: upload `dist/kurze-url_windows_*.zip` and `dist/checksums.txt` as the artifact `cli-windows-snapshot`.
  - A new job `install-script-windows`, named "install script (cli, windows)", runs on `windows-latest` with `needs: [changes, release-dry-run]` and the same `if:`. Its steps:
    1. checkout and setup-go (the harness builds its fixture binary);
    2. PSScriptAnalyzer over both scripts, failing on any finding;
    3. `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install_test.ps1` and `pwsh -NoProfile -File scripts/install_test.ps1`;
    4. download the artifact;
    5. the end-to-end install below.

**Interfaces:**

- Consumes: the asset names (Task 1) and `release-dry-run` (Task 1).
- Produces the script contract:
  - **The same variables as `install.sh`:**
    - `KURZE_URL_INSTALL_DIR`, default `$env:LOCALAPPDATA\Programs\kurze-url`;
    - `KURZE_URL_VERSION`;
    - `KURZE_URL_DOWNLOAD_URL`, with the same default and the same precedence. A `file://` base is copied with `Copy-Item`, because PowerShell 7's web cmdlets refuse the `file` scheme.
  - **`KURZE_URL_NO_MODIFY_PATH=1`:** the script then leaves the user `Path` alone and prints the hint instead. It is for people who manage `Path` themselves, and for running the tests on a developer's machine.
  - **Failure:** a terminating error with the message, so the exit is non-zero under `-File` or `-Command`.
  - **Output:** messages through `Write-Host`, with a `SuppressMessageAttribute` for `PSAvoidUsingWriteHost` justified as console output of an installer. The closing `kurze-url version` line goes through `Write-Output`.

**`install.ps1` behaviour:**

1. **TLS:** on Windows PowerShell 5.1, add TLS 1.2 to `[Net.ServicePointManager]::SecurityProtocol` (an `-bor`, never a replacement). Web calls use `-UseBasicParsing`.
2. **Architecture:** `$env:PROCESSOR_ARCHITEW6432` if it is set, else `$env:PROCESSOR_ARCHITECTURE`. `AMD64` is `amd64` and `ARM64` is `arm64`; anything else, 32-bit `x86` included, is refused.
3. **Download:**
   - It works in a fresh temp dir (`[IO.Path]::GetTempPath()` plus a GUID), removed in `finally`.
   - It prints the URL it downloads from.
   - It fetches `kurze-url_windows_<arch>.zip` and `checksums.txt`; a failed download refuses and names the URL.
4. **Checksum:** the line for the zip in `checksums.txt`, else refuse. `Get-FileHash -Algorithm SHA256` is compared case-insensitively, and a mismatch refuses before anything is extracted.
5. **Install:**
   1. `Expand-Archive` into the temp dir, then create the install dir.
   2. Copy `kurze-url.exe` there as `kurze-url.exe.new`.
   3. If `kurze-url.exe` exists, remove any older `.old` (ignoring failure) and rename the existing binary to `kurze-url.exe.old`. A running `.exe` can be renamed but not overwritten.
   4. Rename `.new` to `kurze-url.exe`, then try to delete `.old`. Ignore a failure; the next run cleans it.
6. **`Path` (option A):** unless `KURZE_URL_NO_MODIFY_PATH` is `1`:
   - read `[Environment]::GetEnvironmentVariable('Path', 'User')`;
   - when no `;`-separated entry equals the install dir (ignoring case and a trailing `\`), append it with `SetEnvironmentVariable(…, 'User')` and say that new terminals will pick it up;
   - add it to `$env:Path` for the current session either way, if missing.
7. **Report:** the installed path, then `& "<dir>\kurze-url.exe" version` through `Write-Output`.
8. **Language:** one `Get-Message <key>` function, English and German by the rule in Global Constraints.

**Test harness `scripts/install_test.ps1`:**

- It is plain PowerShell (no Pester), runs under 5.1 and 7, and needs no network.
- **Running a case:** each case runs the script the way a person does, in a child process of the same shell: `Get-Content -Raw <install.ps1> | Invoke-Expression`, with the case's environment variables. `LOCALAPPDATA` and the install dir point into the case's temp dir, and `KURZE_URL_NO_MODIFY_PATH=1` is set unless the case is about `Path`.
- **Fixture:** the harness builds the real binary with `go build -ldflags "-X main.version=9.9.9 -X main.commit=abc1234 -X main.date=2026-10-24T00:00:00Z"`, for a second version `9.9.10` where a case needs it. It zips the binary as `kurze-url_windows_<arch>.zip` with a matching `checksums.txt` and uses `KURZE_URL_DOWNLOAD_URL=file:///<fixture dir>`.
- **Offline shim:** where a case must not download, the child defines a function `Invoke-WebRequest` before `Invoke-Expression`. A function shadows the cmdlet. The function records its `-Uri` to a file and throws.
- **Output:** `ok <name>`, `skip <name>: <reason>` or `FAIL <name>: <reason>`; the script exits 1 if any case failed.

- [ ] **Step 1: Write the failing tests** as cases in `install_test.ps1`:
  - `installs_into_the_given_dir`: `kurze-url.exe` exists and the last output line is `kurze-url 9.9.9 (abc1234, 2026-10-24T00:00:00Z)`.
  - `defaults_to_localappdata_programs`: with `KURZE_URL_INSTALL_DIR` unset, the binary lands in `<LOCALAPPDATA>\Programs\kurze-url`.
  - `refuses_a_checksum_mismatch`: one byte of the zip altered; non-zero exit; nothing installed; the message names the mismatch.
  - `refuses_an_archive_missing_from_checksums` and `refuses_a_failed_download`.
  - `maps_architectures`:
    - `PROCESSOR_ARCHITECTURE=ARM64` installs `kurze-url_windows_arm64.zip`;
    - `PROCESSOR_ARCHITECTURE=x86` with `PROCESSOR_ARCHITEW6432=AMD64` installs the amd64 zip;
    - `x86` alone is refused.
  - `reinstalls_over_an_existing_binary`: 9.9.9, then 9.9.10. The second version reports itself, and neither `.new` nor `.old` is left.
  - `reinstalls_while_the_old_binary_is_open`: the case holds the installed exe open with `[IO.File]::Open(…, 'Open', 'Read', 'Read, Delete')` (the share mode Windows gives a running image), then reinstalls. Success, with the new version in place. This is Review Focus 3.
  - `adds_the_dir_to_the_user_path_once`: runs only when `$env:CI -eq 'true'`, otherwise it reports `skip`. It saves the user `Path` and restores it in `finally`. Two installs leave exactly one entry for the dir, even when the dir was given with a trailing `\` and different case. This is Review Focus 5.
  - `leaves_the_path_alone_when_asked`: with `KURZE_URL_NO_MODIFY_PATH=1`, the user `Path` is unchanged and the hint is printed.
  - `speaks_german_with_a_german_locale`: `LANG=de_DE.UTF-8` makes the mismatch message German, and `LC_ALL=C` with `LANG=de_DE` keeps it English.
  - `uses_the_version_in_the_url`: with the offline shim, no `KURZE_URL_DOWNLOAD_URL` and `KURZE_URL_VERSION=v0.1.0`, the recorded URI contains `/releases/download/v0.1.0/kurze-url_windows_`.
  - `a_truncated_script_does_nothing`: the first half of `install.ps1`, by line count, goes through `Invoke-Expression` with the offline shim. The shim records nothing and no install dir is created. This is Review Focus 1.
- [ ] **Step 2:** run the harness against a stub `install.ps1` that only throws. Use `powershell` if this machine has it, or `pwsh` (`brew install --cask powershell` is the maintainer's call; without either, CI on `windows-latest` is the first run). Expected: every case prints `FAIL`, except the truncated-script and `skip` cases, and the exit is 1.
- [ ] **Step 3:** write `install.ps1`.
- [ ] **Step 4:** run the harness under every available PowerShell. Expected: every case prints `ok` or `skip`, and the exit is 0. If `pwsh` runs on this macOS machine, the Windows-only parts (`Get-FileHash`, `Expand-Archive` and the user `Path`) still work there, except the `Path` case, which skips outside CI.
- [ ] **Step 5:** add the `install-script-windows` job and the artifact upload. The end-to-end step, in `powershell`:
  1. sets `KURZE_URL_DOWNLOAD_URL` to the artifact dir as a `file:///` URL, and `KURZE_URL_INSTALL_DIR` under `$env:RUNNER_TEMP`;
  2. runs `Get-Content -Raw install.ps1 | Invoke-Expression`;
  3. asserts that the last output line starts with `kurze-url ` and contains `-snapshot`, and that the user `Path` now contains the install dir.
- [ ] **Step 6:** run `pnpm format:check` (the workflow YAML), then commit with the message `feat(cli): add the windows install script`.

### Task 4: README, planning documents and CLAUDE.md

**Files:**

- Modify: `README.md`. Add a "Command-line tool" section after "What it does".
- Modify: `docs/planning/01-architecture.md`: the CLI line (line 19) and the "CLI authentication" section.
- Modify: `docs/planning/06-api-design.md`: the "CLI note" (line 147).
- Modify: `docs/planning/07-repo-structure-and-tooling.md`: the `ci-cli.yml` bullet (line 87) and the "CLI release process" section.
- Modify: `docs/planning/00-index.md`. Add one "Decisions worth remembering" bullet.
- Modify: `CLAUDE.md`. Append a release paragraph to the existing CLI entry ("The CLI is a thin `/v1` client …").
- Modify: `docs/superpowers/specs/2026-10-23-cli-design.md`, Part 4. The `install.sh` bullet's last line "On Windows the README points to the release page." becomes a bullet of its own for `install.ps1`:
  - `irm … | iex`;
  - the same checksum rule;
  - the install dir `%LOCALAPPDATA%\Programs\kurze-url` or `$KURZE_URL_INSTALL_DIR`;
  - the user `Path` added automatically unless `KURZE_URL_NO_MODIFY_PATH=1`;
  - the release page kept as the manual alternative.

  Mark it "decided by the maintainer on 2026-10-24".

**Content decisions:**

- **README "Command-line tool"** covers, in this order:
  1. Install:
     - the one-liner `curl -fsSL https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.sh | sh`;
     - `KURZE_URL_INSTALL_DIR` and `KURZE_URL_VERSION`;
     - Windows, two ways to choose from:
       - the PowerShell one-liner `irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex`, which adds the install dir to the user `Path` (and `KURZE_URL_NO_MODIFY_PATH=1` to skip that);
       - by hand: download `kurze-url_windows_<arch>.zip` from the release page and put `kurze-url.exe` on `Path` yourself;
     - macOS: a binary downloaded through a browser carries the quarantine flag and needs `xattr -d com.apple.quarantine`, which the script path avoids.
  2. First steps: `login`, then `link create https://…`, `link list`, `link stats <slug>` and `link qr <slug>`.
  3. Config: the file location from `os.UserConfigDir()` (`~/Library/Application Support/kurze-url/config.yaml` on macOS, `~/.config/kurze-url/config.yaml` on Linux, `%AppData%\kurze-url\config.yaml` on Windows), the spec's YAML example, and `config set`.
  4. "Releasing (maintainers)":
     1. tag `vX.Y.Z-rc.N` on a `main` commit and push the tag;
     2. run the spec's manual checklist against that pre-release on macOS, Linux and Windows;
     3. tag `vX.Y.Z`.

     A tag not on `main` is refused, and only `(cli)`-scoped commits reach the changelog.

  `go install` is not offered. The module is nested, so it would need `apps/cli/vX.Y.Z` tags, and without the release ldflags its `version` would print `dev`.

- **`01-architecture.md`:**
  - The CLI line: the binary `kurze-url` with `link create`, `link list`, `link stats` and `link qr`, plus `login`, `logout`, `whoami`, `team list` and `config`.
  - "CLI authentication" gains an "Implemented 2026-10-23/24" paragraph covering:
    - the consent page at `/oauth/consent` in the web app;
    - fixed loopback ports 53682 to 53684 on `127.0.0.1`, because redirect URIs are matched exactly;
    - tokens in the system keychain or a `0600` file;
    - `logout` with `scope=local`;
    - the web's local-by-default sign-out with "Sign out everywhere".

    It links the spec.
- **`06-api-design.md` CLI note:** the command names become `kurze-url link create|list|stats|qr`. `team list` and the team, folder and domain lookups read `GET /v1/me` and the list endpoints, so there is still no CLI-specific endpoint.
- **`07-repo-structure-and-tooling.md`:**
  - **The `ci-cli.yml` bullet:**
    - It always runs, with change detection over `apps/cli/`, `apps/api/openapi.json` and both CLI workflows.
    - Its checks are vet, gofmt, golangci-lint, `go mod tidy -diff`, the cross-builds, `go test -race`, govulncheck, the install-script tests on Linux, macOS and Windows (PowerShell 5.1 and 7), and the release dry run.
    - The contract test is why the OpenAPI file is watched.
  - **"CLI release process"** gains the decided details:
    - the version-less asset names and why;
    - `checksums.txt`;
    - `install.sh` and `install.ps1` in `apps/cli/`, served from `main`;
    - `KURZE_URL_INSTALL_DIR`, and `install.ps1` adding to the user `Path` unless `KURZE_URL_NO_MODIFY_PATH=1`;
    - pre-release `-rc.N` tags;
    - the main-only tag guard;
    - the `(cli)` changelog filter.
- **`00-index.md`** gains this bullet: "**Supabase's OAuth server matches a redirect URI exactly, port included**, so the CLI's loopback login uses three fixed, registered ports rather than an ephemeral one (measured 2026-10-23)."
- **CLAUDE.md**, appended to the CLI entry in its style, as one or two sentences each:
  - **The asset names carry no version** because both install scripts download from `releases/latest/download`.
  - **GitHub's "latest" release is repository-wide.** Publishing any other GitHub Release in this repository would point both scripts at it, so the CLI must stay the only thing released there, or the scripts must look up the newest `v*` release instead.
  - **`install.ps1` writes the user `Path`.** That is the maintainer's 2026-10-24 decision, and `KURZE_URL_NO_MODIFY_PATH=1` opts out. Its tests touch the real user `Path` only under `CI=true` and restore it.
  - **Release candidates** are `-rc.N` pre-releases, which "latest" skips.
  - **`release-cli.yml` refuses a tag** whose commit is not on `main`.

- [ ] **Step 1:** write the README section and every amendment above.
- [ ] **Step 2:** run `pnpm format`. Then check that every path, variable and command the new text names exists in the tree: both install scripts, the `KURZE_URL_*` variables in them, the ports in `apps/cli/internal/auth`, and the job names in `ci-cli.yml`. Expected: each one is found.
- [ ] **Step 3:** commit with the message `docs(cli): document installing and releasing`.

---

## After merge (maintainer, not the executor)

1. **Required checks:** add the four new check names to the `main` ruleset (the controller can do this through the API on request):
   - `release dry run (cli)`;
   - `install script (cli, ubuntu-latest)`;
   - `install script (cli, macos-latest)`;
   - `install script (cli, windows)`.
2. **Release candidate:** tag `v0.1.0-rc.1` on the merge commit and push the tag. `release-cli.yml` publishes a pre-release.
3. **Manual checklist** (spec, "Testing"), on macOS, Linux (keychain and file fallback) and Windows:
   1. a real `login` against production, then `link create`, `link stats`, `link list` and `link qr`;
   2. `logout`, then a command that must ask for `login` again.

   Install the pre-release with `KURZE_URL_VERSION=v0.1.0-rc.1`: through `install.sh` on macOS and Linux, and through `install.ps1` on Windows (`$env:KURZE_URL_VERSION='v0.1.0-rc.1'` first).

4. **Final release:** tag `v0.1.0`.
