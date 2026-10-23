# CLI Part 3 — The Go CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `kurze-url` binary, built from `apps/cli`. After one `kurze-url login`, it creates short links, lists them, shows their 7- and 30-day numbers and writes their QR codes, in English or German. Its CI workflow guards it.

**Architecture:**

- **Module:** `apps/cli` is its own Go module. It imports nothing from `apps/api` and talks to `/v1` over HTTP. Login is Supabase's OAuth 2.1 server, with a loopback redirect on fixed ports.
- **Packages:** small and single-purpose, so each can be tested alone:
  - `i18n`: catalogs and language detection;
  - `config`: the YAML file, read and edited through the node tree;
  - `environment`: compiled-in values and their overrides;
  - `credentials`: the keychain, with a file as the fallback;
  - `apiclient`: typed `/v1` calls;
  - `auth`: discovery, the login flow, refresh and logout;
  - `output`: tables;
  - `cli`: the cobra commands.
- **Dependencies through `Deps`:** every command reaches the outside world (streams, environment, clock, browser, HTTP and credentials) through one `cli.Deps` value. Command tests therefore run against `httptest` servers and an in-memory credential store.

**Tech Stack:** Go 1.27, with:

- `github.com/spf13/cobra` v1.10.2;
- `go.yaml.in/yaml/v3` v3.0.5;
- `github.com/zalando/go-keyring` v0.2.8;
- `golang.org/x/oauth2` v0.37.0, `golang.org/x/term` v0.47.0 and `golang.org/x/sys` v0.49.0;
- govulncheck v1.9.0 as a `tool` directive.

These are the newest releases as of 2026-10-23. Take newer ones if they exist when you add them, and say so in the report.

**Spec:** `docs/superpowers/specs/2026-10-23-cli-design.md`. This plan implements **Part 3**, plus `ci-cli.yml` and the CLAUDE.md entry from Part 4. The "Verified (2026-10-23)" table in Part 1 is binding for everything about login, refresh and logout. Release (`.goreleaser.yaml`, `release-cli.yml`, `install.sh`, README and the planning-doc amendments) is Part 4, not here.

## Global Constraints

- **Git:** GitButler only, on branch `feat/cli` from `main` (`but commit -b feat/cli -m "…" <ids>`). Commits follow Conventional Commits with scope `cli`, the subject is at most 50 characters, and there is no co-author or generator footer. Run `pnpm format` before every commit, because `but commit` skips Lefthook and `vp fmt` checks the JSON and YAML files this plan adds.
- **Module:** `module github.com/mheob/kurze-url/apps/cli`, with `go 1.27.0` and `toolchain go1.27.3`, exactly as in `apps/api/go.mod`. Nothing imports `github.com/mheob/kurze-url/apps/api/...`.
- **Code style:** match `apps/api`:
  - every exported identifier and every non-obvious function has a doc comment that explains why;
  - errors are wrapped with `%w`, and `errors.Is` and `errors.As` are used, never string matching;
  - tests use `testing` with `github.com/stretchr/testify/require` (as `apps/api` does);
  - no test touches the real keychain, the real config directory or the network beyond `httptest`.
- **Commands:** run Go from the repository root with `go -C apps/cli …`.
- **Streams:** results go to stdout. Every message, warning, prompt and progress line goes to stderr.
- **Exit codes:** 0 for success, 1 for any failure, 2 for a usage error. A usage error is cobra's argument or flag validation, an unknown command or subcommand, or a flag value outside its documented set or range.
- **i18n (golden rule 6):**
  - every string a command prints comes from `internal/i18n/locales/{en,de}.json`;
  - English and German are informal ("du"), matching the web app;
  - command names, flags and `--help` text stay English;
  - so does the `version` line, and so does cobra's own usage-error text.
- **Compiled-in production values:** API `https://api.kurze-url.app`; Supabase `https://yfgzuzxgsghieoavpluy.supabase.co`; shared host `go.kurze-url.app`; and the Production publishable key and OAuth client ID. The last two are public values the maintainer supplies at this plan's handoff. The controller writes them into the ledger and into Task 4's dispatch.
- **OAuth facts (Verified):**
  - the discovery path is `<supabase>/auth/v1/.well-known/oauth-authorization-server`, and the S256 method must be listed;
  - the redirect URI is `http://127.0.0.1:<port>/callback`, with ports 53682, 53683 and 53684 tried in that order, and never `localhost`;
  - every Supabase auth call carries the `apikey: <publishable key>` header;
  - the client is public: `client_id` goes in the form body, with no secret;
  - a rotated refresh token is saved at once;
  - logout is `POST <supabase>/auth/v1/logout?scope=local` with the access token.
- **Secrets:**
  - no token, refresh token or authorization code is ever printed, logged or put into an error message;
  - tokens never go into `config.yaml`;
  - the loopback listener binds `127.0.0.1` only.
- **Retries:**
  - the API client sends a request a second time only after a 401, once, with a freshly refreshed token, because a 401 means the API did nothing;
  - nothing is retried after a network error, a timeout or a 5xx, and `link create` is no exception.

## Review Focus

1. **Pipe purity.** `kurze-url link create https://example.org | pbcopy` must put exactly the short URL and one newline on stdout. That holds even when a warning fires in the same run, such as a stale `teams.<slug>` section or the keychain fallback. Task 9 tests it with a stale section present.
2. **A refresh token another process already rotated.** Two terminals run commands at the same moment. Each refresh writes back the new refresh token, and a process holding an older token re-reads the store once before it says "login again". Task 6 tests both orders.
3. **A hand-edited config file.**
   - `fg: 000000` unquoted (YAML reads it as an integer) and `size: '1024'` quoted are accepted, because validation reads the scalar's text.
   - A duplicate key and `teams: {sv-gruenwald: ~}` fail with the file and the line.
   - An unknown team section only warns.

   Task 3 tests the file side, and Task 7 tests the warning.

4. **The many spellings of one link.** `SommerFest`, `GO.KURZE-URL.APP/sommerfest`, `https://go.kurze-url.app/sommerfest/` and `https://go.kurze-url.app/sommerfest?qr=1` all resolve to the link stored as `sommerfest` on `go.kurze-url.app`. Task 10 tests each.
5. **QR settings from the right team.** A link found by its full URL in a team that is not the default takes `teams.<that team>.qr`, not the default team's section. Task 10 tests it.

---

### Task 1: Module, root command and CI

**Files:**

- Create: `apps/cli/go.mod`, `apps/cli/go.sum` and `apps/cli/.golangci.yml`. The lint file is a copy of `apps/api/.golangci.yml` without the `internal/db/` exclusion and without `sqlclosecheck`.
- Create: `apps/cli/cmd/kurze-url/main.go`
- Create: `apps/cli/internal/cli/{deps.go,root.go,errors.go,version.go}`
- Test: `apps/cli/internal/cli/root_test.go`
- Create: `.github/workflows/ci-cli.yml`
- Modify: `lefthook.yml` (pre-push jobs)
- Delete: `apps/cli/.gitkeep`

**Interfaces:**

- Produces:
  - `cli.BuildInfo{Version, Commit, Date string}`.
  - `cli.Deps{Stdout, Stderr io.Writer; Stdin io.Reader; Build BuildInfo}`. Later tasks add fields. Each addition gets a default in `cli.Default`, so `main.go` never changes.
  - `cli.Default(b BuildInfo) *Deps` and `cli.Run(d *Deps, args []string) int`. `Run` returns the exit code.
  - `cli.UsageError{Err error}`, with `Error()` and `Unwrap()`.
  - `usageArgs(cobra.PositionalArgs) cobra.PositionalArgs` wraps a validator's error in `UsageError`.
  - Every command and every group in later tasks sets `Args: usageArgs(…)`. A group sets `usageArgs(cobra.NoArgs)`, so `kurze-url link nope` is a usage error.
  - `main.go` holds `var version, commit, date = "dev", "none", "unknown"`. Those names are what goreleaser's default ldflags set in Part 4.
  - `Run` sets `SilenceErrors` and `SilenceUsage`, routes flag errors through `SetFlagErrorFunc` into `UsageError`, and prints failures to stderr as `kurze-url: <message>`.
    - A usage error gets a second line: `Run 'kurze-url <command path> --help' for usage.`.
    - From Task 7 on, `<message>` comes from `describe(err)`. Here it is `err.Error()`.

- [ ] **Step 1: Write the failing tests** in `root_test.go`. Each one runs `Run` with `bytes.Buffer` streams and `BuildInfo{"1.2.3", "abc1234", "2026-10-23"}`.
  - `TestVersionPrintsBuildInfo`: `version` exits 0, and stdout is exactly `kurze-url 1.2.3 (abc1234, 2026-10-23)\n`.
  - `TestUnknownCommandIsAUsageError`: `nope` exits 2, and stderr contains `unknown command`.
  - `TestUnknownFlagIsAUsageError`: `version --nope` exits 2.
  - `TestExtraArgumentIsAUsageError`: `version extra` exits 2.
  - `TestCompletionPrintsAZshScript`: `completion zsh` exits 0, and stdout contains `#compdef kurze-url`.
- [ ] **Step 2:** create `go.mod` (module, `go`, `toolchain`), then run `go -C apps/cli get github.com/spf13/cobra@v1.10.2 github.com/stretchr/testify@latest` and `go -C apps/cli test ./internal/cli/`. Expected: FAIL to compile, because `Run` is undefined.
- [ ] **Step 3:** implement `main.go`, `deps.go`, `root.go`, `errors.go` and `version.go`. Keep cobra's default `completion` command for bash, zsh, fish and powershell.
- [ ] **Step 4:** run `go -C apps/cli test ./internal/cli/`. Expected: PASS.
- [ ] **Step 5:** add govulncheck as a tool with `go -C apps/cli get -tool golang.org/x/vuln/cmd/govulncheck@v1.9.0`, then run `go -C apps/cli mod tidy` and `go -C apps/cli tool govulncheck ./...`. Expected: `No vulnerabilities found.`
- [ ] **Step 6: Write `.github/workflows/ci-cli.yml`** after `ci-api.yml`:
  - **Same as `ci-api.yml`:** the header comment's reasoning about not filtering with `on.paths`, `on`, `permissions`, a `concurrency` group `ci-cli-${{ github.ref }}`, and the same pinned action SHAs.
  - **`changes` job** ("detect changes (cli)"): the same base-SHA logic, with output `cli` and the pattern `^(apps/cli/|apps/api/openapi\.json$|\.github/workflows/ci-cli\.yml$)`.
  - **`static` job** ("vet & lint (cli)"), with `working-directory: apps/cli` and `setup-go` reading `apps/cli/go.mod`. It runs:
    - `go vet ./...`;
    - the `gofmt` block;
    - `golangci-lint-action`, `version: v2.15.0`, `working-directory: apps/cli`;
    - `go mod tidy -diff`;
    - `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=darwin GOARCH=arm64 go build ./...`. Add a comment: the Windows locale code is build-tagged and `go test` runs on Linux only.
  - **`test` job** ("go test (cli)"): `go test -race -count=1 ./...`. There is no Supabase stack, and a comment says why: every test fakes its server with `httptest`.
  - **`vuln` job** ("govulncheck (cli)"): `go tool govulncheck ./...`.
  - Every job except `changes` has `if: needs.changes.outputs.cli == 'true'`.
  - Job names carry `(cli)` so they never collide with the API's required check names.
- [ ] **Step 7:** in `lefthook.yml`'s `pre-push`, add `go vet (cli)` and `gofmt (cli)` jobs. They mirror the `apps/api` pair, with glob `apps/cli/**/*.go` and root `apps/cli/`.
- [ ] **Step 8:** run `go -C apps/cli vet ./...`, `golangci-lint run` in `apps/cli` (if it is installed locally; CI is the gate) and the two cross-builds. Expected: no output, exit 0.
- [ ] **Step 9:** commit with the message `build(cli): scaffold the go module and ci`.

### Task 2: Catalogs and language detection

**Files:**

- Create: `apps/cli/internal/i18n/{i18n.go,detect.go,system_windows.go,system_other.go}`
- Create: `apps/cli/internal/i18n/locales/{en,de}.json`. These are flat maps of dotted keys to strings, with placeholders written `{name}`.
- Test: `apps/cli/internal/i18n/i18n_test.go` and `apps/cli/internal/i18n/usage_test.go`

**Interfaces:**

- Produces:
  - `type Lang string` with `EN Lang = "en"` and `DE Lang = "de"`, and `type Args map[string]string`.
  - `Load(l Lang) *Catalog` reads from `//go:embed locales/*.json`.
  - `(*Catalog).T(key string, args ...Args) string` replaces `{name}` placeholders. An unknown key returns the key itself; the tests make that unreachable.
  - `(*Catalog).Lang() Lang`.
  - `Detect(configured string, getenv func(string) string, system func() string) Lang`. A `configured` value of `en` or `de` wins. Otherwise the first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is non-empty decides: `DE` when it starts with `de`, case-insensitively, else `EN`. Only when all three are empty is `system()` asked, with the same prefix rule. Otherwise the result is `EN`.
  - `SystemLanguage() string`:
    - on Windows, the first entry of `windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)`, for example `de-DE`;
    - elsewhere, `""`.
    - Environment variables are still checked first on Windows, because Git Bash sets `LANG`.
- **The calling rule:** call `T` only with a string literal as the key. `usage_test.go` enforces it.

- [ ] **Step 1: Write the failing tests:**
  - `TestCatalogsHaveTheSameKeys`: each catalog's key set equals the other's.
  - `TestCatalogsHaveTheSamePlaceholders`: for every key, the set of `{name}` tokens matches between `en` and `de`.
  - `TestTReplacesPlaceholders`: an unexported `newCatalog(EN, map[string]string{"greeting": "Hello {name}"})` makes `T("greeting", Args{"name": "Ada"})` return `Hello Ada`. No test key goes into the shipped catalogs, which start as `{}`.
  - `TestDetect` is a table test:
    - configured `de` wins over `LANG=en_US.UTF-8`;
    - `LC_ALL=de_DE.UTF-8` gives `DE`;
    - `LC_ALL=C` with `LANG=de_DE` gives `EN`, because the first variable that is set decides;
    - only `LC_MESSAGES=de_AT` gives `DE`;
    - nothing set with system `de-DE` gives `DE`;
    - nothing set with system `""` gives `EN`;
    - configured `fr` is ignored, so `LANG=de_DE` gives `DE`.
  - `TestEveryUsedKeyIsDefined` in `usage_test.go`:
    - it parses every non-test `.go` file under the module root (`../..`) with `go/parser`;
    - for every method call named `T` with at least one argument, a non-literal first argument fails the test with `file:line`;
    - a literal key missing from `en.json` fails as well.
- [ ] **Step 2:** run `go -C apps/cli test ./internal/i18n/`. Expected: FAIL to compile.
- [ ] **Step 3:** implement the package. Run `go -C apps/cli get golang.org/x/sys@v0.49.0` for the Windows file, and give the two system files the build tags `windows` and `!windows`.
- [ ] **Step 4:** run `go -C apps/cli test ./internal/i18n/` and `GOOS=windows go -C apps/cli build ./...`. Expected: PASS, then a clean build.
- [ ] **Step 5:** commit with the message `feat(cli): add english and german catalogs`.

### Task 3: The YAML config file

**Files:**

- Create: `apps/cli/internal/config/{config.go,keys.go,validate.go,edit.go}`
- Test: `apps/cli/internal/config/config_test.go`

**Interfaces:**

- Produces:
  - **Keys:** `type Key string`, with the constants `Language` `"language"`, `Team` `"team"`, `Folder` `"folder"`, `Domain` `"domain"`, `QRFormat` `"qr.format"`, `QRSize` `"qr.size"`, `QRFG` `"qr.fg"` and `QRBG` `"qr.bg"`.
    - `ParseKey(s string) (Key, error)`.
    - `Key.Global() bool`: true for `language`, `team` and `qr.*`. `Key.PerTeam() bool`: true for `folder`, `domain` and `qr.*`.
  - **File access:**
    - `Path(getenv func(string) string) (string, error)`: `KURZE_URL_CONFIG`, else `os.UserConfigDir()/kurze-url/config.yaml`.
    - `Load(path string) (*File, error)`: a missing file is an empty `*File`, not an error.
    - `(*File).Get(k Key, team string) (string, bool)`: the raw value in that one scope (`team == ""` is global).
    - `(*File).Set(k Key, team, value string) error` and `(*File).Unset(k Key, team string) error`.
    - `(*File).Save() error`: it creates the directory with mode `0700` and writes the file with mode `0644` through a temporary file and a rename in the same directory.
    - `(*File).TeamSections() []string`.
  - **Resolution:** `(*File).Resolve(k Key, team string) (value string, source Source, ok bool)`, with `type Source string` (`SourceTeam`, `SourceGlobal`). For a per-team key it looks in `teams.<team>` first, then globally where `k.Global()`. Built-in defaults are the caller's business.
  - **Errors:** `*ValidationError{Path string; Line int; Code Code; Key, Value string}`, with `Error()` returning `path:line: code` for logs. The codes are `CodeSyntax`, `CodeNotAMapping`, `CodeUnknownKey`, `CodeDuplicateKey`, `CodeInvalidLanguage`, `CodeInvalidTeamSlug`, `CodeInvalidFormat`, `CodeInvalidSize`, `CodeInvalidColour`, `CodeInvalidHostname`, `CodeEmptyValue`, `CodeKeyNeedsTeam` and `CodeKeyIsGlobal`. Task 7 translates each code.

**Rules (spec, "Config"):**

- **Top level:** only `language`, `team`, `qr` and `teams`.
- **`language`:** `en` or `de`.
- **`team` and every `teams.<slug>` key:** the team slug rule `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, with 3 to 40 characters.
- **`qr`:**
  - `format` is `svg` or `png`;
  - `size` is an integer from 64 to 2048;
  - `fg` and `bg` are `^[0-9a-fA-F]{6}$`.
  - Each is checked on the scalar's **text**, whatever its YAML tag, so `000000` passes.
- **Per team:** `folder` is a non-empty string; `domain` is lowercase letters, digits, dots and hyphens, non-empty; `qr` follows the global rules.
- **Shapes:**
  - a non-mapping where a mapping belongs is `CodeNotAMapping`, and that includes `teams: {sv-gruenwald: ~}`;
  - a key appearing twice in one mapping is `CodeDuplicateKey`;
  - YAML syntax errors are `CodeSyntax`, with the line taken from the parser's `yaml: line N:` message when present.
- **`Set` normalisation:**
  - it validates exactly as `Load` does;
  - it strips one leading `#` from a colour and lowercases it;
  - it writes colours single-quoted (`yaml.SingleQuotedStyle`) and `size` as a plain integer;
  - it refuses `folder` and `domain` without a team (`CodeKeyNeedsTeam`), and `language` and `team` with one (`CodeKeyIsGlobal`).
- **`Unset`:** it removes the key, then any `qr`, `teams.<slug>` or `teams` mapping it left empty.

- [ ] **Step 1: Write the failing tests:**
  - `TestLoadMissingFileIsEmpty`.
  - `TestResolvePrefersTeamOverGlobal`: global `qr.format: png` with team `qr.format: svg` resolves to `svg` from `SourceTeam` for that team, and to `png` from `SourceGlobal` for another team.
  - `TestLoadAcceptsColourAndSizeWhateverTheirTag`: `fg: 000000`, `bg: 'FFFFFF'` and `size: '1024'` load cleanly.
  - `TestLoadRejects` is a table, one case per code reachable from a file. Each asserts `Code`, `Line` and `Path`. It includes unknown top-level key, unknown team key, duplicate key, `teams: {sv-gruenwald: ~}`, `size: 63`, `size: 2049`, `format: jpg`, `fg: 12345`, `language: fr`, a team slug `SV` and broken YAML on line 3.
  - `TestSetKeepsCommentsAndOrder`: load a file with a head comment `# my defaults`, `language: de`, then `team: sv-gruenwald` with a line comment. `Set(QRFG, "sv-gruenwald", "#1A3A6E")` and `Save()`. The re-read text still contains both comments, keeps `language` before `team`, and contains `fg: '1a3a6e'`.
  - `TestSetRefusesKeyInWrongScope`: `Set(Folder, "", "x")` returns `CodeKeyNeedsTeam`, and `Set(Language, "sv-gruenwald", "de")` returns `CodeKeyIsGlobal`.
  - `TestUnsetRemovesEmptyParents`: a file whose only content is `teams.sv-gruenwald.qr.fg` saves as an empty document after `Unset(QRFG, "sv-gruenwald")`.
  - `TestPathHonoursOverride`.
  - `TestSaveCreatesDirectoryWith0700`.
- [ ] **Step 2:** run `go -C apps/cli get go.yaml.in/yaml/v3@v3.0.5` and `go -C apps/cli test ./internal/config/`. Expected: FAIL to compile.
- [ ] **Step 3:** implement the package on `yaml.Node` only: `yaml.Unmarshal` into a `yaml.Node`, then an `Encoder` with `SetIndent(2)`. Never decode into a struct, because that drops comments.
- [ ] **Step 4:** run `go -C apps/cli test ./internal/config/`. Expected: PASS.
- [ ] **Step 5:** commit with the message `feat(cli): add the yaml config file`.

### Task 4: Environments and credential storage

**Files:**

- Create: `apps/cli/internal/environment/environment.go`
- Test: `apps/cli/internal/environment/environment_test.go`
- Create: `apps/cli/internal/credentials/{credentials.go,file.go}`
- Test: `apps/cli/internal/credentials/credentials_test.go`

**Interfaces:**

- Produces:
  - **`environment`:**
    - `type Environment struct{ APIURL, SupabaseURL, PublishableKey, ClientID, SharedHost, VercelBypass, Name string }`.
    - The defaults are the constants `DefaultAPIURL`, `DefaultSupabaseURL`, `DefaultPublishableKey`, `DefaultClientID` and `DefaultSharedHost`, with the values from Global Constraints.
    - `FromEnv(getenv func(string) string) (Environment, error)` reads `KURZE_URL_API_URL`, `KURZE_URL_SUPABASE_URL`, `KURZE_URL_SUPABASE_PUBLISHABLE_KEY`, `KURZE_URL_CLIENT_ID`, `KURZE_URL_SHARED_HOST` and `KURZE_URL_VERCEL_BYPASS`.
      - An empty variable means the default.
      - The two URLs must be absolute `http`/`https` URLs, and a trailing `/` is trimmed.
      - `SharedHost` is lowercased.
    - `Name` is derived from `SupabaseURL`: the lowercased `host[:port]`, with every character outside `[a-z0-9]` replaced by `-`.
  - **`credentials`:**
    - `type Tokens struct{ AccessToken string \`json:"access_token"\`; RefreshToken string \`json:"refresh_token"\`; ExpiresAt time.Time \`json:"expires_at"\` }`.
    - `ErrNotFound`.
    - `type Store interface{ Load() (Tokens, error); Save(Tokens) error; Delete() error }`.
    - `type FallbackReporter interface{ FellBack() bool; FilePath() string }`: `FellBack` is true after a `Save` that went to the file.
    - `New(envName, dir string) *Chain`, where `*Chain` implements `Store` and `FallbackReporter`.
    - `NewMemory() *Memory` is an in-memory `Store` and `FallbackReporter` for other packages' tests. Its exported `FallbackPath string`, when non-empty, makes `FellBack()` true and is what `FilePath()` returns. It lives in a non-test file, because Task 6 and Task 7 import it.

**`Chain` behaviour (spec, "Credentials"):**

- **Keychain entry:** `zalando/go-keyring`, with service `kurze-url`, user `envName` and the tokens as JSON in one entry.
- **`Save`:**
  - keychain first;
  - on any keychain error, the file `dir/credentials-<envName>.json`, written with mode `0600` through a temporary file in the same directory and a rename, after creating the directory with mode `0700`;
  - after a successful keychain write, it deletes a leftover fallback file, so a stale token never sits on disk.
- **`Load`:** keychain first. On `keyring.ErrNotFound` or any keychain error it falls back to the file, and a missing file is `ErrNotFound`.
- **`Delete`:** it removes both stores, ignoring not-found.

- [ ] **Step 1: Write the failing tests:**
  - **`environment`:**
    - `TestFromEnvDefaults`: every field equals its constant, and `Name` is `yfgzuzxgsghieoavpluy-supabase-co`.
    - `TestFromEnvOverrides`: every variable set takes effect, `http://127.0.0.1:54321/` gives `Name` `127-0-0-1-54321` and the URL without the trailing slash, and `SHORT.INVALID` becomes `short.invalid`.
    - `TestFromEnvRejectsARelativeURL`.
    - `TestDefaultsAreFilledIn`: `DefaultClientID` and `DefaultPublishableKey` are non-empty.
  - **`credentials`,** using `keyring.MockInit()` or `keyring.MockInitWithError(errors.New("no secret service"))` and `t.TempDir()`:
    - `TestSaveUsesTheKeychain`: the round trip works, `FellBack()` is false and no file exists.
    - `TestSaveFallsBackToA0600File`: the round trip works, `FellBack()` is true, the file mode is `0600` and the directory holds no temporary file.
    - `TestLoadWithNothingStoredIsErrNotFound`.
    - `TestDeleteRemovesBoth`.
    - `TestEnvironmentsDoNotShareTokens`: a `Save` under `a` is invisible to a `Load` under `b`.
    - `TestKeychainSaveRemovesAStaleFile`.
- [ ] **Step 2:** run `go -C apps/cli get github.com/zalando/go-keyring@v0.2.8` and `go -C apps/cli test ./internal/environment/ ./internal/credentials/`. Expected: FAIL to compile.
- [ ] **Step 3:** implement both packages.
- [ ] **Step 4:** run the same tests. Expected: PASS.
- [ ] **Step 5:** check the publishable key against gitleaks. Run `gitleaks detect --no-git --source apps/cli` if it is installed; otherwise CI's "Secret scan" is the check. If the compiled-in publishable key matches `generic-api-key`, add its fingerprint to `.gitleaksignore`. The entry's comment says that this is Supabase's publishable key, that the web app ships it to every browser, and that the spec's anti-goals allow it. Never suppress anything else.
- [ ] **Step 6:** commit with the message `feat(cli): store credentials per environment`.

### Task 5: The API client and its contract test

**Files:**

- Create: `apps/cli/internal/apiclient/{client.go,types.go,problem.go,paths.go}`
- Test: `apps/cli/internal/apiclient/client_test.go` and `apps/cli/internal/apiclient/contract_test.go`

**Interfaces:**

- **Consumes:** nothing from earlier tasks. Tokens arrive through an interface.
- **Produces:**
  - **Options:**
    - `type TokenSource interface{ AccessToken(ctx context.Context) (string, error); Refresh(ctx context.Context) (string, error) }`.
    - `type Options struct{ BaseURL string; HTTPClient *http.Client; Tokens TokenSource; VercelBypass, UserAgent string }`.
    - `New(o Options) *Client`. A nil `HTTPClient` becomes one with `Timeout: 30 * time.Second`.
  - **Calls:**
    - `(*Client).Me(ctx) (Me, error)`;
    - `Folders(ctx, teamID string) ([]Folder, error)` and `Domains(ctx, teamID string) ([]Domain, error)`. Both page with `per_page=100` until `total_count` items have been read or a page comes back empty;
    - `CreateLink(ctx, teamID string, r CreateLinkRequest) (Link, error)`;
    - `ListLinks(ctx, teamID string, q ListLinksQuery) (Page[Link], error)`;
    - `LinkStats(ctx, linkID, from, to string) (LinkStats, error)`, where the dates are `YYYY-MM-DD`;
    - `LinkQR(ctx, linkID string, q QRQuery) ([]byte, error)`.
  - **Types** (JSON names as in `apps/api/openapi.json`):
    - `Me{UserID, Email string; Memberships []Membership}` and `Membership{TeamID, Name, Slug, Role string}`;
    - `Folder{ID, Name string}` and `Domain{ID, Hostname, VerificationStatus string}`;
    - `Link{ID, TeamID, DomainID, Hostname, Slug, ShortURL, DestinationURL, State string}`;
    - `Page[T]{Items []T; Page, PerPage, TotalCount int}`;
    - `CreateLinkRequest{DestinationURL string; Slug, FolderID, DomainID string}`, the last three with `omitempty`;
    - `ListLinksQuery{Q, FolderID, Sort string; Page, PerPage int}`, where empty or zero values are omitted from the query;
    - `LinkStats{AnalyticsEnabled bool; Totals StatCounts; Series []StatDay}`, `StatCounts{Clicks, UniqueVisitors, HumanClicks, HumanUniqueVisitors int64}` and `StatDay{Date string; Clicks, UniqueVisitors, HumanClicks, HumanUniqueVisitors int64}`;
    - `QRQuery{Format string; Size int; FG, BG string}`, where empty or zero values are omitted.
  - **Problems:** `*ProblemError{Status int; Title, Detail string; Errors []ProblemDetail}` with `ProblemDetail{Location, Message string; Value any}`, and `(*ProblemError).HasDetail(location, value string) bool`.
  - **Paths:** `paths.go` holds the path templates (`PathMe = "/v1/me"`, `PathTeamLinks = "/v1/teams/{team_id}/links"` and so on) and the query parameter names as exported constants. The client builds requests only from those constants, and the contract test reads the same ones.

**Behaviour:**

- **Every request sends:**
  - `Authorization: Bearer <AccessToken()>`;
  - `User-Agent`;
  - `Accept: application/json`, or `image/*` for the QR call;
  - `x-vercel-protection-bypass` when `VercelBypass` is non-empty.
- **On a 401:** the client calls `Refresh` once and sends the same request again, rebuilding the body from bytes. An error from `Refresh` is returned wrapped, so `errors.Is` still finds it.
- **Problem decoding:** any status outside 200 to 299 is decoded as `application/problem+json` into `*ProblemError`. A body that is not JSON leaves `Detail` empty, and `Status` is always set.

- [ ] **Step 1: Write the failing tests** in `client_test.go`, each against an `httptest.Server`:
  - `TestRequestsCarryTheBearerTokenAndBypass`.
  - `TestA401RefreshesOnceAndRetries`: the first call answers 401 and the second 201. `Refresh` is called once, and the POST body arrives intact both times.
  - `TestASecond401IsReturned`: a `*ProblemError` with `Status` 401, and `Refresh` called exactly once.
  - `TestA500IsNotRetried`: the server is called exactly once.
  - `TestProblemJSONIsDecoded`: a 422 with `errors: [{location: "query.fg", value: "low_contrast"}]` makes `HasDetail("query.fg", "low_contrast")` true.
  - `TestFoldersPagesUntilTotalCount`: a server holding 150 folders is called with `page=1` and then `page=2`, both with `per_page=100`.
  - `TestListLinksOmitsEmptyQueryValues`.
  - `TestLinkQRReturnsTheBytes`.
- [ ] **Step 2: Write the failing contract test** `TestContractMatchesOpenAPI` in `contract_test.go`:
  - it reads `../../../api/openapi.json`;
  - for each operation the client uses (method plus path constant), it asserts the path and method exist;
  - it asserts every query parameter constant the client sends is listed for that operation;
  - it walks each request and response Go type by reflection, following `$ref`, arrays and `Page[T]`'s `items`, and asserts every `json` tag name exists in the schema's `properties`.

  A failure names the operation and the missing name.

- [ ] **Step 3:** run `go -C apps/cli test ./internal/apiclient/`. Expected: FAIL to compile.
- [ ] **Step 4:** implement the package.
- [ ] **Step 5:** run `go -C apps/cli test ./internal/apiclient/`. Expected: PASS. Then temporarily rename `short_url` in a scratch copy of the schema and confirm the contract test fails. Do not commit that copy.
- [ ] **Step 6:** commit with the message `feat(cli): add the api client and contract test`.

### Task 6: OAuth login, refresh and logout

**Files:**

- Create: `apps/cli/internal/auth/{discovery.go,login.go,listener.go,page.go,page.html.tmpl,session.go,logout.go}`
- Test: `apps/cli/internal/auth/{login_test.go,session_test.go,logout_test.go}` and a shared stub `authserver_test.go`
- Modify: both catalogs (`login.page.*` and `login.*` keys)

**Interfaces:**

- **Consumes:**
  - from `credentials` (Task 4): `Tokens`, `Store` and `NewMemory`;
  - from `i18n` (Task 2): `*Catalog`.
- **Produces:**
  - **Discovery:** `type Endpoints struct{ Authorization, Token string }` and `Discover(ctx, hc *http.Client, supabaseURL, apikey string) (Endpoints, error)`. It fails with `ErrNoS256` when `code_challenge_methods_supported` lacks `S256`.
  - **Defaults:** `DefaultPorts = []int{53682, 53683, 53684}` and `DefaultLoginTimeout = 5 * time.Minute`.
  - **The login flow:**
    - `type Flow struct{ Endpoints Endpoints; ClientID, PublishableKey string; HTTPClient *http.Client; Ports []int; Timeout time.Duration; OpenBrowser func(string) error; Stderr io.Writer; Catalog *i18n.Catalog }`.
    - `(Flow).Login(ctx) (credentials.Tokens, error)`.
    - Its sentinels are `ErrPortsBusy`, `ErrStateMismatch`, `ErrDenied` and `ErrTimeout`.
  - **The listener:** `bindFirstFree(ports []int) (net.Listener, int, error)` on `127.0.0.1`. A port of `0` means an ephemeral port, which tests use.
  - **The session:**
    - `type Session struct{ Store credentials.Store; Endpoints func(ctx context.Context) (Endpoints, error); ClientID, PublishableKey string; HTTPClient *http.Client; Now func() time.Time }`.
    - It implements `apiclient.TokenSource`, through `AccessToken` and `Refresh`.
    - `ErrLoginRequired` covers both nothing stored and a refresh that cannot recover.
  - **Logout:** `Logout(ctx, hc *http.Client, supabaseURL, apikey, accessToken string) error`.

**Behaviour:**

- **OAuth calls:** they use `golang.org/x/oauth2` with `AuthStyle: oauth2.AuthStyleInParams`, `oauth2.GenerateVerifier`, `S256ChallengeOption` and `VerifierOption`. They pass an `http.Client` through `oauth2.HTTPClient` whose transport adds `apikey` to every request. Discovery and logout carry `apikey` too.
- **`Login`:**
  1. It binds the first free port and builds the redirect URI `http://127.0.0.1:<port>/callback`. `state` is 32 random bytes, base64url-encoded.
  2. It calls `OpenBrowser` with the authorize URL. On error it prints `login.openManually` with the URL to `Stderr`; it always prints `login.waiting` first.
  3. It serves only `GET /callback`; anything else is a 404. It takes the first callback whose `state` matches.
     - **State mismatch:** it serves the failure page and returns `ErrStateMismatch`.
     - **`error` present:** it serves the failure page and returns `ErrDenied`.
     - **Otherwise:** it exchanges the code. On success it serves the success page and returns the tokens; on an exchange error it serves the failure page and returns the wrapped error.
  4. It then shuts the server down. After `Timeout` it returns `ErrTimeout`.
- **Callback pages:** `page.html.tmpl` is rendered with `html/template` and contains:
  - `<html lang="en|de">`;
  - inline CSS only: one centred card, system font and a `prefers-color-scheme: dark` block;
  - one `<main>` and one `<h1>`.

  The copy is the spec's, in both catalogs:
  - `login.page.successTitle` "Signed in" / "Angemeldet";
  - `login.page.successBody` "The CLI is now connected to your account. You can close this window and continue in the terminal." / "Die CLI ist jetzt mit deinem Konto verbunden. Du kannst dieses Fenster schließen und im Terminal weitermachen.";
  - `login.page.failureTitle` "Sign-in cancelled" / "Anmeldung abgebrochen";
  - `login.page.failureBody` "The terminal says how to continue." / "Im Terminal steht, wie es weitergeht.".

- **`AccessToken`:** a stored token with more than 60 s left is returned as is. Otherwise it refreshes.
- **Refresh** (Verified rows "Refresh rotation", "parent" and "grandparent"):
  1. It calls the token endpoint with the stored refresh token and saves the result at once.
  2. On an `*oauth2.RetrieveError` with status 400 or 401, it re-reads the store once.
     - If the stored refresh token differs, another process rotated it. It returns that stored access token if more than 60 s are left, else it refreshes once with the new refresh token.
     - Otherwise it returns `ErrLoginRequired`.
  3. Network errors and 5xx are returned wrapped, never as `ErrLoginRequired`.
- **`Logout`:** `POST <supabaseURL>/auth/v1/logout?scope=local` with `Authorization: Bearer` and `apikey`. Any 2xx is success; anything else is an error carrying only the status.

- [ ] **Step 1: Write the failing tests.** The stub authorization server in `authserver_test.go` serves discovery, the token endpoint (code exchange and refresh with rotation) and logout. It records the `apikey` header and the form values of each call.
  - **Discovery:**
    - `TestDiscoverReadsTheEndpoints`.
    - `TestDiscoverRefusesWithoutS256`.
    - `TestDefaultPorts`: equals `[]int{53682, 53683, 53684}`.
  - **The listener:**
    - `TestBindSkipsABusyPort`: hold a listener on port `p`; `bindFirstFree([]int{p, 0})` returns another port.
    - `TestBindFailsWhenEveryPortIsBusy`: `bindFirstFree([]int{p})` returns `ErrPortsBusy`.
  - **The login flow.** The fake browser is an `OpenBrowser` that parses the URL and asserts `code_challenge_method=S256`, the `client_id` and `redirect_uri` on `127.0.0.1`. It then GETs the redirect with `code` and the same `state`.
    - `TestLoginExchangesTheCode`: the exchange sends `code_verifier`, `client_id` and `redirect_uri` in the body plus the `apikey` header, and the success page contains "Signed in".
    - `TestLoginPrintsTheURLWhenNoBrowserOpens`: `OpenBrowser` errors; stderr contains the authorize URL, and a test goroutine completing the callback still logs in.
    - `TestLoginRefusesAStateMismatch`.
    - `TestLoginReportsDenial`: `error=access_denied` gives `ErrDenied`, and the page contains "Sign-in cancelled".
    - `TestLoginTimesOut`: `Timeout` 50 ms and a browser that does nothing give `ErrTimeout`.
    - `TestLoginPageIsGerman`: with the `DE` catalog the page contains `lang="de"` and "Angemeldet".
  - **The session:**
    - `TestAccessTokenReturnsAFreshTokenUntouched`: no token endpoint call.
    - `TestAccessTokenRefreshesWithin60Seconds`: the new tokens are in the store.
    - `TestRefreshRereadsTheStoreWhenAnotherProcessRotated`: the stub answers 400 for the old refresh token, and the store holds a newer pair with a fresh access token, which is returned without a second refresh.
    - `TestRefreshRetriesWithTheRotatedToken`: the same, but the stored access token is stale, so exactly one more refresh happens, using the stored refresh token.
    - `TestRefreshAsksForLoginWhenNothingChanged`: `ErrLoginRequired`.
    - `TestA503DuringRefreshIsNotLoginRequired`.
    - `TestNothingStoredIsLoginRequired`.
  - **Logout:** `TestLogoutUsesScopeLocal` checks the method, the path, the query `scope=local`, the bearer and the `apikey`.
- [ ] **Step 2:** run `go -C apps/cli get golang.org/x/oauth2@v0.37.0` and `go -C apps/cli test ./internal/auth/`. Expected: FAIL to compile.
- [ ] **Step 3:** implement the package and add the `login.*` keys to both catalogs: `login.waiting` and `login.openManually` (with `{url}`).
- [ ] **Step 4:** run `go -C apps/cli test -race ./internal/auth/ ./internal/i18n/`. Expected: PASS.
- [ ] **Step 5:** commit with the message `feat(cli): add the oauth login flow`.

### Task 7: The app, `login`, `logout`, `whoami` and `team list`

**Files:**

- Create: `apps/cli/internal/output/{table.go,tty.go}`
- Test: `apps/cli/internal/output/table_test.go`
- Create: `apps/cli/internal/cli/{app.go,describe.go,auth_cmds.go,team_cmds.go,browser.go}`
- Modify: `apps/cli/internal/cli/{deps.go,root.go}` and both catalogs
- Test: `apps/cli/internal/cli/harness_test.go` (shared), `auth_cmds_test.go`, `team_cmds_test.go` and `describe_test.go`

**Interfaces:**

- **Consumes:** every earlier package.
- **Produces:**
  - **`output`:**
    - `Truncate(s string, max int) string` is rune-aware and ends in `…`.
    - `Table(w io.Writer, header []string, rows [][]string, width, shrinkCol int)` writes aligned columns separated by two spaces. When `width > 0` it truncates column `shrinkCol` so each row fits.
    - `TSV(w io.Writer, rows [][]string)` writes no header.
    - `IsTerminal(fd uintptr) bool` and `Width(fd uintptr) int` wrap `golang.org/x/term`.
  - **New `Deps` fields**, each defaulted in `Default`:
    - `Getenv func(string) string`, `Now func() time.Time`, `StdinTTY func() bool`, `StdoutTTY func() bool` and `TermWidth func() int`;
    - `OpenBrowser func(string) error` (`browser.go`: `open` on darwin, `xdg-open` elsewhere, `rundll32 url.dll,FileProtocolHandler` on windows);
    - `HTTPClient *http.Client`, `LoginPorts []int` (default `auth.DefaultPorts`) and `LoginTimeout time.Duration` (default `auth.DefaultLoginTimeout`);
    - `CredentialsDir string` (default `os.UserConfigDir()/kurze-url`);
    - `NewStore func(envName, dir string) credentials.Store`.
  - **`App`:** `type App struct{ Env environment.Environment; Config *config.File; Cat *i18n.Catalog; Store credentials.Store; Session *auth.Session; API *apiclient.Client }`.
    - `newApp(d *Deps) (*App, error)` is called lazily by the commands that need it, never by `version` or `completion`. The language comes from `i18n.Detect(config language, d.Getenv, i18n.SystemLanguage)`.
  - **Membership helpers:**
    - `(*App).me(ctx) (apiclient.Me, error)` caches `/v1/me`. On its first call it warns once on stderr, with `config.staleTeam` (`{slug}`), for every `teams.<slug>` section that is not one of the memberships.
    - `(*App).resolveTeam(ctx, flag string) (apiclient.Membership, error)` takes the flag, else the config `team`. With neither it fails with `team.noneGiven`; a slug that is not a membership fails with `team.notMember`.
  - **`describe(err error, c *i18n.Catalog) string`** maps each case to a catalog key:
    - `auth.ErrLoginRequired`, and `*apiclient.ProblemError` with status 401: `errors.loginAgain`;
    - `ProblemError` with 403 or 404: `errors.notFound`;
    - 429: `errors.rateLimited`;
    - 422 with `HasDetail("query.fg", "low_contrast")`: `errors.qrLowContrast`;
    - 422 with a detail at `query.fg` or `query.bg` with value `invalid_color`: `errors.qrInvalidColour`;
    - 422 with any detail at `body.destination_url`: `errors.invalidDestination`;
    - any other `ProblemError`: its `Detail`, else `Title`, else the HTTP status text;
    - `auth.ErrPortsBusy`, `ErrStateMismatch`, `ErrDenied` and `ErrTimeout`: one key each;
    - `*config.ValidationError`: `config.invalid.<code>` with `{file}`, `{line}`, `{key}` and `{value}`;
    - `*url.Error` and `net.Error`: `errors.network` with `{host}`;
    - anything else: `err.Error()`.

    The 409 "slug taken" mapping belongs to `link create` (Task 9), because only there is a 409 known to mean that.

  - **Roles,** localised the way the web app does:
    - `team.role.owner` "Owner" / "Eigentum";
    - `team.role.admin` "Admin" / "Admin";
    - `team.role.editor` "Editor" / "Bearbeitung";
    - `team.role.viewer` "Viewer" / "Leserecht".

**Commands (spec, "Commands"):**

- **`login`:**
  1. `auth.Discover`, then `Flow.Login` with `d.LoginPorts`, `d.LoginTimeout` and `d.OpenBrowser`, then `Store.Save`.
  2. If the store is a `credentials.FallbackReporter` and `FellBack()` is true, it prints `login.storedInFile` with `{path}` (`FilePath()`) once.
  3. `GET /v1/me`.
  4. **Default team:**
     - an existing config `team` that is still a membership is kept (my call: logging in again should not ask again);
     - one membership becomes the default;
     - several memberships with `StdinTTY()` true get a numbered prompt on stderr reading from `Stdin`: up to 3 attempts, and EOF means no default;
     - several memberships without a TTY get no default and print `login.noDefaultHint`;
     - zero memberships print `login.noTeams`.
  5. It saves the config only if `team` changed.
  6. It prints `login.done` on stderr: "Signed in as {email} · default team: {team}", or `login.doneNoTeam`.
- **`logout`:**
  - with nothing stored, it prints `logout.notSignedIn` and exits 0;
  - otherwise it gets an access token through the session. If that fails with `ErrLoginRequired`, it skips the remote call. If not, it calls `auth.Logout`, and a failure prints `logout.remoteFailed` without failing the command;
  - it always runs `Store.Delete()` and prints `logout.done`: "Signed out of the CLI. Your browser sessions stay signed in." / "Aus der CLI abgemeldet. Deine Browser-Sitzungen bleiben angemeldet.".
- **`whoami`:** stdout gets the email on line 1, then `whoami.defaultTeam` with `{team}`, or `whoami.noDefaultTeam`.
- **`team list`:** the rows are marker (`*` for the default team, else empty), slug, name and localised role. A TTY gets `Table` with a localised header; anything else gets `TSV`. The `team` group itself has `usageArgs(cobra.NoArgs)`.

- [ ] **Step 1: Write the failing tests.**
  - **`output`:**
    - `TestTruncateIsRuneAware`: `Truncate("Grünwald-Sommerfest", 8)` is `Grünwal…`.
    - `TestTableShrinksOneColumn`.
    - `TestTSVHasNoHeader`.
  - **The harness:** `harness_test.go` builds `newHarness(t)`. It has:
    - a fake API (`httptest.Server`) with `h.api.handle(method, path string, fn http.HandlerFunc)`;
    - a stub auth server for discovery, token and logout;
    - `Deps` with buffers, an in-memory store and `KURZE_URL_CONFIG` pointing into `t.TempDir()`;
    - the env overrides pointing at both servers, `LoginPorts: []int{0}`, a fake browser that completes the callback, and `Now` fixed at `2026-10-23T10:00:00Z`;
    - `h.signIn()`, which seeds valid tokens, `h.writeConfig(yaml string)`, and `h.run(args ...string) int`.
  - **Auth commands:**
    - `TestLoginWithOneTeamSetsTheDefault`: the config gets `team: sv-gruenwald`, and stderr contains "Signed in as ada@example.org · default team: sv-gruenwald".
    - `TestLoginKeepsAValidExistingDefault`.
    - `TestLoginAsksOnATTYWithSeveralTeams`: `StdinTTY` is true and stdin is `"2\n"`, so the second team becomes the default.
    - `TestLoginWithoutATTYSetsNoDefault`: no `team` key, and the hint is printed.
    - `TestLoginSaysOnceWhenTheTokenGoesToAFile`: the memory store has `FallbackPath` set, and stderr contains that path exactly once.
    - `TestLogoutDeletesTokensEvenWhenTheServerFails`: logout answers 500, the exit is 0, the store is empty and stderr contains the remote-failure message.
    - `TestLogoutWhenNotSignedIn`.
    - `TestWhoamiPrintsEmailAndDefaultTeam`.
    - `TestCommandsInGermanWhenConfigured`: with `language: de`, `whoami` prints `Standard-Team:`.
  - **Team commands:**
    - `TestTeamListMarksTheDefault` (non-TTY): the TSV rows are `*\tsv-gruenwald\tSV Grünwald\tOwner` and `\ttc-muster\tTC Muster\tViewer`.
    - `TestTeamWithoutSubcommandIsAUsageError`: exit 2.
    - `TestStaleTeamSectionWarnsOnStderrOnly`.
  - **`describe`:**
    - `TestDescribeMapsEveryKnownCase` is a table, one row per mapping above, in English, each asserting the exact catalog text.
    - `TestUnknownProblemShowsTheAPIDetail`.
    - `TestExpiredLoginSaysLoginAgain`: a fake API answering 401 twice, with a refresh that fails, exits 1 and prints "Please run `kurze-url login` again.".
- [ ] **Step 2:** run `go -C apps/cli get golang.org/x/term@v0.47.0` and `go -C apps/cli test ./internal/output/ ./internal/cli/`. Expected: FAIL to compile.
- [ ] **Step 3:** implement `output`, `App` and `describe`, then the four commands. Add every new key to both catalogs. The `errors.*` copy:
  - `errors.loginAgain`: "Please run `kurze-url login` again." / "Bitte erneut `kurze-url login` ausführen.";
  - `errors.notFound`: "Not found, or you have no access to it." / "Nicht gefunden, oder du hast keinen Zugriff darauf.";
  - `errors.rateLimited`: "Too many requests just now. Please wait a minute and try again." / "Gerade zu viele Anfragen. Bitte warte eine Minute und versuche es erneut.";
  - `errors.qrLowContrast`: "The two colours are too close together for a camera to read the code." / "Die beiden Farben liegen zu nah beieinander, eine Kamera kann den Code so nicht lesen.";
  - `errors.invalidDestination`: "The destination must be a complete https:// address." / "Das Ziel muss eine vollständige https://-Adresse sein.".
- [ ] **Step 4:** run `go -C apps/cli test -race ./...`. Expected: PASS, including `TestEveryUsedKeyIsDefined` and the parity tests.
- [ ] **Step 5:** commit with the message `feat(cli): add login, logout, whoami, team list`.

### Task 8: `config get`, `config set` and `config unset`

**Files:**

- Create: `apps/cli/internal/cli/config_cmds.go`
- Modify: both catalogs
- Test: `apps/cli/internal/cli/config_cmds_test.go`

**Interfaces:**

- **Consumes:**
  - from `config` (Task 3): `ParseKey`, `Get`, `Set`, `Unset`, `Save` and `Resolve`;
  - from `cli` (Task 7): `newApp`, the harness and `describe`.
- **Produces:** the commands `config get [KEY] [--team SLUG]`, `config set KEY VALUE [--team SLUG]` and `config unset KEY [--team SLUG]`. None of them calls the API.

**Behaviour:**

- **`config get` without a key** prints rows of key, effective value and localised source for `language`, `team`, `qr.format`, `qr.size`, `qr.fg` and `qr.bg`. For the team given by `--team`, or else the configured `team`, it adds `folder` and `domain`.
  - Rows for unset keys are left out.
  - `language` always appears; its source is `config.source.system` when it came from detection.
  - The sources are `config.source.team` (`{slug}`), `config.source.global` and `config.source.system`.
  - The output goes through `Table` on a TTY, else through `TSV`.
- **`config get KEY`** prints only the effective value. An unset key prints `config.notSet` (`{key}`) on stderr and exits 1, as `git config` does.
- **`set` and `unset`:** an unknown key is a usage error (exit 2); a scope or value error exits 1 with the translated `ValidationError`. On success they print `config.saved` (`{path}`) on stderr.
- **No validation through the API:** a `--team` slug that is not a membership is still written, because membership changes.

- [ ] **Step 1: Write the failing tests:**
  - `TestConfigSetThenGet`: `config set qr.fg '#1A3A6E' --team sv-gruenwald`, then `config get qr.fg --team sv-gruenwald`, prints `1a3a6e\n`.
  - `TestConfigGetListsSources`: global `qr.format: png` with team `qr.size: 1024` lists `qr.format` from global, `qr.size` from the team and `language` from the system.
  - `TestConfigGetUnsetKeyExits1`.
  - `TestConfigSetUnknownKeyIsAUsageError`: exit 2.
  - `TestConfigSetFolderWithoutTeamFails`: exit 1, with the translated `keyNeedsTeam` message.
  - `TestConfigSetBadSizeNamesTheFile`: `config set qr.size 10` exits 1, and stderr names the config path.
  - `TestConfigUnsetRemovesTheKey`.
- [ ] **Step 2:** run `go -C apps/cli test ./internal/cli/ -run TestConfig`. Expected: FAIL.
- [ ] **Step 3:** implement `config_cmds.go` and the new catalog keys.
- [ ] **Step 4:** run `go -C apps/cli test -race ./...`. Expected: PASS.
- [ ] **Step 5:** commit with the message `feat(cli): add the config commands`.

### Task 9: `link create` and `link list`

**Files:**

- Create: `apps/cli/internal/cli/link_cmds.go` (the `link` group, `create` and `list`) and `apps/cli/internal/cli/resolve.go` (folder and domain resolution)
- Modify: both catalogs
- Test: `apps/cli/internal/cli/link_create_test.go` and `link_list_test.go`

**Interfaces:**

- **Consumes:**
  - from `cli` (Task 7): `resolveTeam`, `me`, `describe` and the harness;
  - from `output` (Task 7): `Table` and `TSV`;
  - from `apiclient` (Task 5): `Folders`, `Domains`, `CreateLink` and `ListLinks`.
- **Produces:**
  - `(*App).resolveFolder(ctx, team apiclient.Membership, name string) (id string, err error)`;
  - `(*App).resolveDomain(ctx, team apiclient.Membership, host string) (id string, err error)`, which returns `""` for the shared host;
  - `stateLabel(c *i18n.Catalog, state string) string`, with these labels:
    - `link.state.active` "active" / "aktiv";
    - `link.state.disabled` "disabled" / "deaktiviert";
    - `link.state.expired` "expired" / "abgelaufen";
    - `link.state.flagged` "blocked" / "gesperrt".

**Behaviour (spec, `link create` and `link list`):**

- **`link create <url>`:**
  - **Local check:** before any request, the argument must parse with `url.Parse` and have the scheme `https` and a non-empty host. Otherwise it exits 1 with `errors.invalidDestination` and makes no API call.
  - **Team:** `resolveTeam(--team)`.
  - **Folder:** `--folder`, else the team's config `folder`, else none. It is matched against `Folders` with `strings.EqualFold`. An unknown name gives `link.folderUnknown` (`{name}`, `{team}`, `{available}`), and no folder is ever created.
  - **Domain:** `--domain`, else the team's config `domain`, else none.
    - A value equal, case-insensitively, to `Env.SharedHost` sends no `domain_id`.
    - Otherwise the domain is matched against `Domains`. A match with `verification_status` `verified` is used. A match that is not verified gives `link.domainNotVerified`; no match gives `link.domainUnknown`.
  - **Slug:** `--slug` is sent as given.
  - **409:** prints `link.slugTaken`: "That slug is already taken on this domain." / "Dieser Slug ist auf dieser Domain schon vergeben.".
  - **Output:** stdout is exactly `short_url + "\n"`.
- **`link list`:**
  - `--limit` defaults to 20. A value outside 1 to 100 is a usage error.
  - `--folder` filters only when it is given. The configured `folder` is a default for creating, not a list filter (my call: a silent filter would hide links).
  - **Request:** `ListLinks` with `Sort: "-created_at"`, `PerPage: limit`, `Page: 1`, `Q: --search` and `FolderID`.
  - **Rows:** short URL, destination and `stateLabel`. A TTY gets `Table` with a localised header, shrinking the destination column to `TermWidth()`; anything else gets `TSV`.
  - No items prints `link.noneFound` on stderr and exits 0.

- [ ] **Step 1: Write the failing tests.**
  - `link_create_test.go`:
    - `TestCreatePrintsOnlyTheShortURL`: there is a stale `teams.old-team` section, so the warning goes to stderr, and stdout equals `https://go.kurze-url.app/abc12345\n`.
    - `TestCreateSendsSlugAndFolderID`: `--folder NEWSLETTER` matches the folder `newsletter`, and the request body has `slug` and `folder_id`.
    - `TestCreateUsesTheTeamsConfiguredFolderAndDomain`.
    - `TestCreateOnTheSharedHostSendsNoDomainID`: `--domain GO.KURZE-URL.APP`.
    - `TestCreateRefusesAnUnverifiedDomain`.
    - `TestCreateRefusesAnUnknownFolder`: no POST is made.
    - `TestCreateRejectsNonHTTPSLocally`: the cases `example.org`, `http://example.org` and `https://`, each exiting 1 with no API call.
    - `TestCreateExplainsATakenSlug`: a 409 exits 1 with the English text.
    - `TestCreateUsesTheDefaultTeam` and `TestCreateHonoursTeamFlag`.
  - `link_list_test.go`:
    - `TestListSendsSortLimitAndFilters`;
    - `TestListLimitOutOfRangeIsAUsageError` (0 and 101);
    - `TestListPrintsTSVWhenPiped`;
    - `TestListTruncatesOnATTY`: width 60 and a 120-character destination give no line longer than 60 runes, and a line containing `…`;
    - `TestListLocalisesState`: `flagged` becomes `gesperrt` with `language: de`;
    - `TestListIgnoresTheConfiguredFolder`.
- [ ] **Step 2:** run `go -C apps/cli test ./internal/cli/ -run 'TestCreate|TestList'`. Expected: FAIL.
- [ ] **Step 3:** implement the commands, the resolvers and the catalog keys.
- [ ] **Step 4:** run `go -C apps/cli test -race ./...`. Expected: PASS.
- [ ] **Step 5:** commit with the message `feat(cli): add link create and link list`.

### Task 10: Finding a link, `link stats`, `link qr` and the CLAUDE.md entry

**Files:**

- Create: `apps/cli/internal/cli/{lookup.go,stats_cmd.go,qr_cmd.go}`
- Modify: `apps/cli/internal/cli/link_cmds.go` (register both commands) and both catalogs
- Test: `apps/cli/internal/cli/{lookup_test.go,stats_cmd_test.go,qr_cmd_test.go}`
- Modify: `CLAUDE.md` (a new entry under "Non-obvious constraints", after the local-by-default sign-out entry)

**Interfaces:**

- **Consumes:**
  - from `apiclient`: `ListLinks`, `LinkStats` and `LinkQR`;
  - from `cli`: `resolveTeam`, `me` and `describe`;
  - from `config`: `Resolve`.
- **Produces:** `(*App).findLink(ctx, arg, domainFlag, teamFlag string) (apiclient.Link, apiclient.Membership, error)`.
  - **Parsing the argument:** an argument containing `/` is a URL. Without `://`, `https://` is prefixed. The host is the lowercased hostname, the query is ignored, and the path trimmed of `/` must be exactly one segment, or it is a usage error. The slug is lowercased.
  - **Teams to search:**
    - a URL without `--team` searches every membership;
    - a URL with `--team`, or a bare slug, searches `resolveTeam(--team)`.
  - **Searching:** in each team it pages `ListLinks(Q: slug, PerPage: 100)` until `TotalCount` items have been read or a page comes back empty. It keeps the items whose `Slug` equals the slug case-insensitively and whose `Hostname` matches the URL's host or `--domain`, if either is given. The configured `domain` is not a lookup filter; that is the same call as in Task 9.
  - **Matches:**
    - one match returns the link and its owning membership;
    - none gives `link.notFound` (`{slug}`);
    - several give `link.ambiguous` (`{slug}`, plus `{candidates}` as one short URL per line).

**`link stats` (spec):**

- **Dates:** `to` is `Now().UTC()` as `YYYY-MM-DD`, and `from` is `to` minus 29 days.
- **The two windows:** the 30-day column is `Totals`. The 7-day column is the sum of the last 7 `Series` entries after sorting by `Date`.
- **Counting switched off:** `AnalyticsEnabled` false prints `stats.disabled` to stdout and exits 0.
- **Layout** on stdout, with labels from the catalog and numbers right-aligned:

  ```
  https://go.kurze-url.app/sommerfest

                       7 days   30 days
  Humans
    Clicks                 12        40
    Visitors*              10        33
  All (incl. bots)
    Clicks                 30       100
    Visitors*              25        80

  * Visitors are counted per day and added up: someone who comes back on three days counts three times.
  ```

  The German labels are "7 Tage", "30 Tage", "Menschen", "Klicks", "Besucher*" and "Alle (inkl. Bots)". The footnote reads "* Besucher werden pro Tag gezählt und addiert: Wer an drei Tagen wiederkommt, zählt dreimal."

**`link qr` (spec):**

- **The team:** the team whose `qr` section applies is the link's **owning** membership from `findLink`, not `--team` or the default.
- **Precedence:** each of `format`, `size`, `fg` and `bg` takes the flag, then `teams.<owner>.qr`, then the global `qr`.
  - **Format:** a built-in `svg` (the API's documented default), always sent, so the file name is known before the request.
  - **`--format`** outside `svg|png` is a usage error.
  - **`--size`** outside 64 to 2048 is a usage error, and so is `--size` when the effective format is `svg`. A configured `size` with `svg` is not sent.
  - **Colour flags:** `--fg` and `--bg` must match `^#?[0-9a-fA-F]{6}$` or it is a usage error. They are sent lowercased without `#`.
- **Output:**
  - `--output -` writes the bytes to stdout and nothing else;
  - otherwise the file is `--output` or `<slug>.<format>`. Its existence is checked before the request, and an existing file without `--force` exits 1 with `qr.exists` (`{file}`);
  - writing uses `O_CREATE|O_EXCL` without `--force`, and truncates with it, mode `0644`;
  - on success it prints `qr.written` (`{file}`) on stderr.

- [ ] **Step 1: Write the failing tests:**
  - `lookup_test.go`, with the fake API holding `sommerfest` on `go.kurze-url.app` and on `links.sv-gruenwald.de` in team A, and `fest` in team B (not the default):
    - `TestFindLinkSpellings` covers every input in Review Focus 4, each resolving to the `go.kurze-url.app` link.
    - `TestFindLinkBareSlugAcrossDomainsIsAmbiguous`: the error lists both short URLs.
    - `TestFindLinkDomainFlagDisambiguates`.
    - `TestFindLinkURLSearchesEveryTeam`: `https://go.kurze-url.app/fest` with the default team A still finds team B's link.
    - `TestFindLinkPagesThroughResults`: 230 `q` matches over three pages, with the exact match on page 3.
    - `TestFindLinkNotFound`.
  - `stats_cmd_test.go`:
    - `TestStatsRequestsTheLast30Days`: `from=2026-09-24` and `to=2026-10-23`.
    - `TestStatsWindows`: 30 series days where day _i_ (1 to 30) has `clicks = i`, `human_clicks = i - 1`, visitors half of that rounded down; and `Totals.Clicks = 999`. The 7-day clicks are `189`, and the 30-day clicks show `999`, which proves totals rather than a sum. The humans block comes before the all block.
    - `TestStatsSortsTheSeriesBeforeSumming`: the series arrives shuffled, and the result is the same.
    - `TestStatsWhenCountingIsOff`.
    - `TestStatsInGerman`.
  - `qr_cmd_test.go`:
    - `TestQRWritesSlugDotFormat`: in `t.Chdir(t.TempDir())`, `sommerfest.svg` is written, and the query is `format=svg` without `size`.
    - `TestQRSizeOnlyForPNG`: a configured `qr.size: 1024` with the default svg sends no size; `--format png` sends `size=1024`.
    - `TestQRExplicitSizeWithSVGIsAUsageError`.
    - `TestQRPrecedence`: the flag beats the team, which beats the global.
    - `TestQRUsesTheOwningTeamsSettings`: Review Focus 5.
    - `TestQRRefusesToOverwrite`: no request is made, and with `--force` the file is overwritten.
    - `TestQROutputDashWritesToStdout`.
    - `TestQRExplainsLowContrast`.
- [ ] **Step 2:** run `go -C apps/cli test ./internal/cli/ -run 'TestFindLink|TestStats|TestQR'`. Expected: FAIL.
- [ ] **Step 3:** implement `lookup.go`, `stats_cmd.go` and `qr_cmd.go`, with the catalog keys.
- [ ] **Step 4:** run `go -C apps/cli test -race ./...`, `go -C apps/cli vet ./...`, `go -C apps/cli tool govulncheck ./...` and both cross-builds from Task 1. Expected: all clean.
- [ ] **Step 5:** commit with the message `feat(cli): add link stats and link qr`.
- [ ] **Step 6: Write the CLAUDE.md entry.** It is one bold-led paragraph, in the house style, covering:
  - **The CLI is a thin `/v1` client** in its own module that imports nothing from `apps/api`. Its contract test reads `apps/api/openapi.json`, so `ci-cli.yml` also triggers on that file: an API change that drops a field the CLI reads fails the CLI's checks on the API's own pull request.
  - **Login ports:** login uses the fixed loopback ports 53682 to 53684 on `127.0.0.1`, because the OAuth server matches redirect URIs exactly and refuses `localhost`. Changing a port means changing every Supabase project's client and shipping a release.
  - **Public values:** the Production Supabase URL, publishable key, client ID and shared host are compiled in, are public values, and are overridden by `KURZE_URL_*` for Preview. The credential key derives from the Supabase URL, so environments never share a token.
  - **Refresh:** a rotated refresh token is saved at once. A failed refresh re-reads the store once before asking for login, because two processes refreshing together is normal and the grandparent token is refused after the 10-second reuse interval.
  - **Credentials** live in the keychain, or a `0600` file, never in `config.yaml`.
  - **Config:** it is edited through the YAML node tree so comments survive, and colours are quoted because `000000` is an integer to YAML.
  - **Windows:** the locale code is build-tagged, so only CI's cross-build compiles it.

  Run `pnpm format`, then commit with the message `docs: add the cli entry to claude.md`.
