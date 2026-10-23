# kurze-url CLI — Design

**Status:** approved in conversation 2026-10-23, awaiting review of this written form.

**Amends:**

- `docs/planning/01-architecture.md`: the CLI line names the binary `kurze-url` and its commands. The CLI authentication section records the consent page and the token storage.
- `docs/planning/06-api-design.md`: the CLI note's command names.
- `docs/planning/07-repo-structure-and-tooling.md`: `ci-cli.yml` and `release-cli.yml` stop being plans, and the install script gets a home.
- `CLAUDE.md`: a CLI entry under "Non-obvious constraints" once the CLI exists. It covers the consent page, the redirect ports, the audience question as settled, and the local-by-default sign-out with its "everywhere" option.
- `README.md`: installation and the commands.

`apps/cli` holds only a `.gitkeep`. Everything around a CLI was decided in September and then left:

- a Go module of its own;
- login through Supabase's OAuth 2.1 server with Authorization Code, PKCE and a loopback redirect;
- releases through goreleaser on a SemVer tag.

The maintainer wants the CLI so that creating a short link does not mean opening the web app. Looking at a link's numbers comes second.

## Goal

A maintainer or a Verein member installs one binary and runs `kurze-url login` once. From then on:

- `kurze-url link create <url>` prints a short URL;
- `kurze-url link stats <slug>` shows the last 7 and 30 days;
- `kurze-url link list` lists recent links;
- `kurze-url link qr <slug>` writes a QR code file.

All four act with the person's own rights in their own teams. They speak English or German, and per-team defaults live in a YAML config file.

## Decisions taken in conversation

| Question | Decision | Why |
| --- | --- | --- |
| Binary name | `kurze-url`, not `short` as the planning docs had it | The maintainer's choice |
| Main job | `link create <url>` prints only the short URL | Usable in a pipe (`\| pbcopy`) and in scripts |
| Options on create | `--slug`, `--folder <name>`, plus `--team` and `--domain` | What the maintainer fills in on the web: mostly a slug, sometimes a folder |
| Which team | `--team <slug>`, else the default team from the config | Several teams per person are normal |
| Statistics | Clicks and unique visitors, last 7 and last 30 days, humans first, then everything including bots | Newsletter links are fetched by mail scanners, so both numbers matter |
| `link list`, `link qr` | Both in v1 | The maintainer's choice |
| Languages | English and German. The system locale decides until the user sets `language` in the config | Golden rule 6 applies to the CLI too |
| Config | YAML, with global defaults, per-team defaults and QR settings | YAML can be rewritten by `config set` without losing the user's comments (`go.yaml.in/yaml/v3` node API); TOML libraries re-encode and drop them |
| Token storage | The OS keychain, with a `0600` file as the fallback | The refresh token is a long-lived credential |
| Consent | A consent page at `/oauth/consent` in the web app, as mocked up and approved | Supabase's OAuth server requires the app to host it |
| What the consent page promises | "Everything you can do in the web app, in every team you belong to." The API keeps treating a CLI token like any other session (option A) | That is exactly what the token can do. Limiting CLI tokens to the CLI's four operations (option B) would be API work with no concrete need, and stays possible later as hardening |
| Signing out | `kurze-url logout` ends only the CLI's session. The web's sign-out also becomes local by default, with a "Sign out everywhere" option, a checkbox, that ends every session, the CLI's included | Neither side should silently end the other, while "everywhere" still means everywhere |
| Release | Unchanged from 2026-09: goreleaser on `vX.Y.Z`, GitHub Release, `curl \| sh` install script. Homebrew and Scoop stay deferred | Already decided |

## Non-goals (v1)

- Editing or deleting links.
- Tags, passwords, expiry, 301, titles, the analytics switch.
- Domain, folder, member and team administration, and the audit log.
- Breakdowns by country, browser or referrer, and daily series.
- JSON output.
- Self-update.
- Homebrew and Scoop.
- An API key, or any headless login. The 2026-09-01 decision stands: built only when a real CI or headless use shows up.

## Anti-goals

These would make the CLI a failure even if it worked:

- **No CLI-specific API endpoint.** The CLI is a thin client over `/v1`, the principle `06-api-design.md` records. Name resolution such as team slug to UUID, folder name to UUID or slug to link happens in the CLI, from existing endpoints. The web app resolves team slugs the same way (`requireTeamId`).
- **No Supabase access beyond the auth endpoints.** No database, no service role, no secret in the binary. The Supabase project URL and the OAuth client ID are public values.
- **No token in `config.yaml`.** Credentials live in the keychain or in their own `0600` file, never next to settings someone might paste into an issue.
- **No silent fallback to stored tokens on a shared machine.** When the keychain is unavailable, `login` says once that the token goes to a file, and where.
- **No telemetry.**

## Part 1 — Supabase OAuth server and the API

### Supabase configuration

- **Local stack:** `supabase/config.toml` sets `[auth.oauth_server] enabled = true` with `authorization_url_path = "/oauth/consent"`. Dynamic registration stays off.
- **Production and Preview:** the same, set by the maintainer in each project's dashboard (Authentication → OAuth Server). It is a manual step, because those projects are not configured from this repository.
- **The client:** one OAuth client per Supabase project, named "kurze-url CLI".
  - It is **public**, with token endpoint auth method `none`, and PKCE is mandatory.
  - Its redirect URIs are exactly `http://127.0.0.1:53682/callback`, `http://127.0.0.1:53683/callback` and `http://127.0.0.1:53684/callback`. The server matches them exactly; see "Verified" below.
- **Redirect allowlist:** the Production project's "Redirect URLs" also have to admit the magic-link callback with the `next` parameter described in Part 2.

### Verification before anything else is built (risks)

1. **Loopback redirect URIs.** Supabase's docs require "exact, complete URL matches" and say nothing on loopback ports ([getting started](https://supabase.com/docs/guides/auth/oauth-server/getting-started); [supabase/auth#2285](https://github.com/supabase/auth/issues/2285) reports strict validation for the OAuth server).
   - **Test:** register `http://127.0.0.1:<port>/callback` against the local stack and run a full authorization.
   - **If any port is accepted** (RFC 8252 §7.3), the CLI uses an ephemeral port and registers one URI.
   - **Otherwise** it tries the registered fixed ports in order.
   - Whether `localhost` or `127.0.0.1` is accepted is settled by the same test. RFC 8252 prefers `127.0.0.1`.
2. **Token audience.** `internal/auth/jwt.go` verifies issuer and audience, and the audience comes from `SUPABASE_JWT_AUDIENCE`, default `authenticated`.
   - **Test:** decode an access token the OAuth server issued to the CLI client.
   - **If `aud` differs**, the verifier accepts a list of audiences, given as a comma-separated `SUPABASE_JWT_AUDIENCE` that keeps `authenticated` as its default, and `.env.example` and CLAUDE.md say why.
   - **Unchanged by this design:** the API's authorization (team membership and role in Go) applies unchanged. A CLI token carries the person's identity, and the API does not treat it differently.
3. **Refresh and revocation.**
   - **Refresh:** confirm that the token endpoint rotates refresh tokens, and that Supabase's reuse interval tolerates two CLI processes refreshing at the same moment.
   - **Revocation:** find which endpoint ends only the CLI's own session on `logout` (an OAuth revocation endpoint, or `POST /auth/v1/logout?scope=local` with the access token).
   - **Web sign-out:** confirm that the web's `signOut` with `scope: 'global'` ends the CLI's session and that `scope: 'local'` leaves it alone. Part 2's sign-out change relies on both.
   - **Keychain size:** check whether the access and refresh token together fit the Windows Credential Manager's per-entry limit. If not, the keychain holds only the refresh token and the access token is refreshed per run.

Write each result into this spec's "Verified" section before Part 2 starts.

### Verified (2026-10-23)

**How it was measured:** `pnpm probe:oauth` (`scripts/oauth-server-probe.ts`) ran against the local stack, Supabase CLI with GoTrue's OAuth 2.1 server switched on in `supabase/config.toml`, on 2026-10-23. Re-run it after a Supabase upgrade: the server is still in beta.

**Scope:** these are the local stack's answers. Production and Preview run the same server, but their token lifetime and reuse interval are dashboard settings. The release checklist (see "Testing") repeats `login`, `logout` and a command against Production.

| Finding | Measured | What it decides |
| --- | --- | --- |
| Discovery document | `/auth/v1/.well-known/oauth-authorization-server`; the RFC 8414 path `/.well-known/oauth-authorization-server/auth/v1` answers 404. It lists `S256` (and `plain`) | The CLI reads its endpoints from there: authorize `…/auth/v1/oauth/authorize`, token `…/auth/v1/oauth/token`. It always sends S256 |
| Registered loopback URI | `http://127.0.0.1:53682/callback` gets a 302 to `<site_url>/oauth/consent?authorization_id=…` | — |
| Same host, unregistered port (`:53999`) | **400**, no redirect | Ports are matched exactly. **Fixed ports:** the CLI tries 53682, 53683 and 53684 in order, all three registered. It fails with a clear message when none is free |
| `localhost` instead of `127.0.0.1` | **400** | The CLI binds and registers `127.0.0.1` only |
| `aud` / `iss` / `role` of a CLI access token | `authenticated` / `<project>/auth/v1` / `authenticated`. It also carries `client_id` and `session_id` | **API unchanged:** the existing verifier accepts CLI tokens, so Part 1's audience change is not needed. `client_id` would allow scoping CLI tokens later (option B) |
| Access-token lifetime | 3600 s locally (`jwt_expiry`) | The CLI refreshes when fewer than 60 s remain, and on a 401 (my call) |
| Refresh rotation | `rotates: true` | The spec's rule stands: write the new refresh token back at once |
| Old refresh token, used again right after rotating | 200 within the 10 s reuse interval | Two CLI processes refreshing at the same moment both succeed |
| The same old token, 12 s later | **Also 200** | GoTrue still honoured it after the interval, most likely because it was still the parent of the active token. The CLI does not rely on this, but a leaked token is not dead the moment it rotates. That is one more reason for the keychain |
| Second authorize after a consent was given | Lands on `/oauth/consent` again. `getAuthorizationDetails` returns only `redirect_url`, with no `authorization_id` | The consent page tells the two apart by the missing `authorization_id`, and redirects at once (Part 2) |
| CLI `POST /auth/v1/logout?scope=local` with its access token | The CLI's refresh then fails with **400**, and the browser's session still refreshes with **200** | **`logout`** uses exactly this call. No separate revocation endpoint is needed |
| A browser signs out with `scope=local` | The CLI's session still refreshes with **200** | The web's local-by-default sign-out leaves the CLI alone, as Part 2 intends |
| A browser signs out with `scope=global` | The CLI's refresh fails with **400** | "Sign out everywhere" does include the CLI. The checkbox text is accurate |
| Grant revoked (`DELETE /auth/v1/user/oauth/grants?client_id=…`) | The CLI's refresh fails with **400** | Revoking the grant ends the CLI's access too. A "revoke CLI access" control in the web app is possible later, and not in v1 |
| Token sizes | access 952 bytes, refresh 12 bytes, both with the expiry as JSON 1026 bytes, against the 2560-byte Windows limit | **Keychain:** it holds the access token, the refresh token and the expiry in one entry |

### Maintainer setup (Production and Preview)

Do this once in each Supabase project's dashboard before Part 2's e2e can pass. None of these values is secret.

1. **Authentication → OAuth Server:**
   - turn the server on;
   - set the authorization path to `/oauth/consent`;
   - leave dynamic client registration off.

   The consent URL is the project's Site URL plus that path. For Production that is the web app's origin.

2. **Authentication → OAuth Apps → Add a new client:**
   - name `kurze-url CLI`;
   - type **Public**;
   - redirect URIs exactly `http://127.0.0.1:53682/callback`, `http://127.0.0.1:53683/callback` and `http://127.0.0.1:53684/callback`, with no wildcard and no `localhost`;
   - grant types authorization code and refresh token.
3. **Client IDs:** the Production client ID is compiled into the CLI as its default (Part 3). The Preview client ID is what `KURZE_URL_CLIENT_ID` is set to when the CLI talks to Preview, and what the consent e2e uses.
4. **Authentication → URL Configuration:** the magic-link callback must still be admitted when it carries the `next` parameter Part 2 adds (`/auth/callback?next=…`).
   - The local stack lists exact URLs (`http://127.0.0.1:3000/auth/callback`).
   - Part 2 finds out locally whether an exact entry matches with a query string or needs a `**` suffix. Then give Production's and Preview's entries the same form.
5. **Token settings:** keep refresh-token rotation on and the reuse interval at its default (10 s). The findings above were measured with those.

## Part 2 — Consent page in the web app

**Route `/oauth/consent?authorization_id=…`.** It lives outside the team namespace and needs a session but no team.

- **Not signed in:** redirect to `/login`, keeping the full consent URL as `next`.
  - **Callback:** the magic link's `emailRedirectTo` carries `next`. `auth.callback.tsx` redirects there after `exchangeCodeForSession` instead of to `/`.
  - **Open-redirect guard:** `next` is honoured only when it is a same-origin path starting with `/oauth/consent?`. Anything else falls back to `/`.
- **Signed in:**
  - It calls `supabase.auth.oauth.getAuthorizationDetails(authorization_id)`, present in the installed `@supabase/auth-js` 2.118 as `AuthOAuthServerApi`.
  - **Consent already given:** the response then says so, and the page redirects to its `redirect_url` at once.
  - **Otherwise** it renders the approved card:
    - title "Allow CLI access" / "CLI-Zugriff erlauben";
    - the client's name;
    - what access means, worded to the decision above: everything the person can do in the web app, in every team they belong to ("alles, was du in der Web-App darfst, in allen deinen Teams"). It must not suggest a narrower scope than the token has;
    - the warning to allow this only right after running `kurze-url login` yourself;
    - the signed-in address;
    - Deny and Allow buttons. Allow calls `approveAuthorization`, Deny calls `denyAuthorization`, and both follow the returned `redirect_url`.
- **Errors:** an unknown or expired `authorization_id`, or a failing call, renders an error card in both languages. It reports nothing to Sentry that `classifyApiError` would classify as user-visible.
- **i18n:** a new `oauthConsent` key group in `en.json` and `de.json`.
- **Accessibility:** one `<main>` and an `<h1>` inside `CardTitle`, the same as `login.tsx`. Keyboard operable.
- **Stories:** cover the card, the error card and the loading state, under the a11y addon.
- **e2e against the preview, with Playwright and axe:**
  - start an authorization with the Preview project's CLI client, sign in with the existing helper, and assert the card;
  - click Allow and assert navigation to `http://127.0.0.1:<port>/callback?code=…&state=…`, intercepted, so no listener is needed;
  - the same again for Deny.
  - **Setup:** a grant stored by an earlier run would skip the card, so the spec revokes the test user's grant for the CLI client first.
  - **Preview URL:** Supabase redirects to the Preview project's Site URL, not to this pull request's preview, so the spec opens `/oauth/consent?authorization_id=…` on the pull request's own preview itself.

### Web sign-out becomes local, with "sign out everywhere"

**Today:** `signOut` in `apps/web/src/server/auth.ts` calls `supabase.auth.signOut()` without options. auth-js then signs out globally, which ends every session the person has, very likely the CLI's too.

**New behaviour:**

- **Server function:** it takes a `scope` input, `local` by default, `global` when asked.
- **Sidebar footer:** `app-sidebar.tsx` gets a checkbox "Sign out everywhere" / "Überall abmelden" next to the sign-out button. It is unchecked by default and sends `global` when checked.
- **i18n and accessibility:** both strings live in the catalogs, and the checkbox has a real label.
- **Documentation:** CLAUDE.md records that the default is local on purpose and that "everywhere" includes the CLI.

**Tests:**

- unit tests for both scopes on the server function;
- a story for the footer with the checkbox;
- in Part 1 risk 3, confirm that `global` ends the CLI's session and `local` does not.

**Mockup:** two cards, the consent card and the CLI's own confirmation page. It was shown in the companion on 2026-10-23 and approved as is. The text in this spec is authoritative, and the layout follows `login.tsx`: one centred card.

## Part 3 — The CLI

### Module and layout

- **Module:** `apps/cli` is its own module, `github.com/mheob/kurze-url/apps/cli`. Its `go` and `toolchain` lines match `apps/api` (see CLAUDE.md on the `toolchain` line).
- **No imports from `apps/api`.**
- **Suggested layout:** `cmd/kurze-url/main.go` and `internal/{apiclient,auth,config,i18n,output}`. The builder may refine it.

### Commands

```
kurze-url login
kurze-url logout
kurze-url whoami
kurze-url team list
kurze-url link create <url> [--slug S] [--folder NAME] [--domain HOST] [--team SLUG]
kurze-url link list   [--folder NAME] [--search TEXT] [--limit N] [--team SLUG]
kurze-url link stats  <slug|short-url> [--domain HOST] [--team SLUG]
kurze-url link qr     <slug|short-url> [--format svg|png] [--size N] [--fg RRGGBB] [--bg RRGGBB]
                      [--output FILE|-] [--force] [--domain HOST] [--team SLUG]
kurze-url config get [KEY] [--team SLUG]
kurze-url config set KEY VALUE [--team SLUG]
kurze-url config unset KEY [--team SLUG]
kurze-url version
kurze-url completion bash|zsh|fish|powershell
```

**Streams and exit codes:**

- Results go to stdout. Messages, prompts and progress go to stderr.
- Exit codes: 0 for success, 1 for any failure, 2 for a usage error.
- **Errors:** the API's RFC 9457 `detail` is shown in the CLI's language where the CLI knows the case. Those cases are: a slug already taken, an invalid URL, forbidden or not found, a QR contrast refusal, and the rate limit. Any other `detail` is shown as the API wrote it.
- **Stale login:** a 401 that refreshing cannot fix ends with "Please run `kurze-url login` again" / "Bitte erneut `kurze-url login`".

**`login`**

- Opens the browser at the authorization URL: PKCE S256, a random `state`, and the redirect URI of the listener it bound on `127.0.0.1`. When the browser cannot be opened, it prints the URL.
- Waits at most 5 minutes.
- **On the callback:** it checks `state`, exchanges the code and stores the tokens, then serves a small localised HTML page and stops the listener.
- **The page:** one centred card like the web's login page, self-contained, with no external assets.
  - **Allowed:** title "Signed in" / "Angemeldet". Text: "The CLI is now connected to your account. You can close this window and continue in the terminal." / "Die CLI ist jetzt mit deinem Konto verbunden. Du kannst dieses Fenster schließen und im Terminal weitermachen."
  - **Denied or failed:** "Sign-in cancelled. The terminal says how to continue." / "Anmeldung abgebrochen. Im Terminal steht, wie es weitergeht."
- **Teams and default:** it reads `GET /v1/me`.
  - **One team:** it becomes the default.
  - **Several teams on a TTY:** the CLI asks with a selection list.
  - **Several teams without a TTY:** login succeeds but sets no default, and says how to set one.
- Prints "Signed in as <email> · default team: <slug>".

**`logout`** ends only the CLI's own session at Supabase (`scope=local`, through the endpoint found in Part 1 risk 3), then deletes the stored tokens. It never signs the person out of their browsers. If the revocation fails, it says so and deletes the tokens anyway.

**`whoami`** prints the email and the default team. **`team list`** prints a table of slug, name and role, with the default team marked.

**`link create`**

- **Team:** taken from `GET /v1/me` memberships, by `--team` or else the configured default.
- **Folder:** a case-insensitive name match against `GET /v1/teams/{team_id}/folders`. An unknown name is an error, never a new folder.
- **Domain:** taken from `--domain`, else the team's configured `domain`, else none.
  - **No domain:** the API's default, the shared domain, applies.
  - **A hostname:** it is matched against the team's verified domains (`GET /v1/teams/{team_id}/domains`).
  - **The shared hostname:** a hostname equal to the instance's shared hostname sends no `domain_id`. That hostname is `go.kurze-url.app` by default and can be overridden by environment.
- **Request and output:** `POST /v1/teams/{team_id}/links` with `destination_url`, `slug` and `folder_id`. Stdout gets the response's `short_url` and nothing else.

**Finding one link** (`stats`, `qr`)

- **A full short URL:** the argument is split into hostname and slug. Without `--team` it is looked up in every team the person belongs to, because the URL already names the link. With `--team`, only that team is searched. (My call.)
- **A bare slug:** the CLI searches the team with `GET /v1/teams/{team_id}/links?q=<slug>`, paging with `per_page=100`, and keeps exact, case-insensitive slug matches.
- **One match** is the link.
- **Several matches** across domains need `--domain` or the full URL, and the error lists the candidates.
- **No match** is a not-found error.

**`link stats`**

- **Request:** one `GET /v1/links/{link_id}/stats` with `from` set to today − 29 and `to` set to today, both UTC.
- **The two windows:** the 30-day figures are the `totals`. The 7-day figures are the sum of the last 7 `series` entries.
- **Table:** a "Humans" block first (`human_clicks`, `human_unique_visitors`), then "All (incl. bots)" (`clicks`, `unique_visitors`).
- **Footnote:** it says that visitors are summed per day. That is the API's own caveat, because a person returning on three days counts three times.
- **Counting switched off:** with `analytics_enabled: false`, the CLI says counting is switched off instead of printing zeros.

**`link list`**

- **Request:** `GET /v1/teams/{team_id}/links?sort=-created_at&per_page=<limit>`, with `folder_id` and `q` when given. The limit defaults to 20 with a maximum of 100. There is no paging beyond one page.
- **Columns:** short URL, destination and state, the state localised.
- **On a TTY:** aligned columns, and the destination is truncated to the terminal width.
- **Not a TTY:** tab-separated with no truncation, so the output can be filtered.

**`link qr`**

- **Request:** `GET /v1/links/{link_id}/qr` with `format`, `size`, `fg` and `bg`. Each value is resolved by precedence: flag, then the team's `qr` section, then the global `qr` section, then the API's default.
- **`size` with SVG:** `size` is sent only for PNG, because the API answers `size` on SVG with a 422. A configured `qr.size` therefore applies to PNG only.
- **Output file:** `<slug>.<format>` in the working directory, or `--output FILE`.
- **Existing files:** an existing file is not overwritten without `--force`.
- **`--output -`:** writes the bytes to stdout.
- **Contrast:** the API enforces it, and the CLI does not duplicate the rule.

### Config

**Location:** `os.UserConfigDir()/kurze-url/config.yaml`, overridable with `KURZE_URL_CONFIG`.

```yaml
language: de
team: sv-gruenwald

qr:
  format: png
  size: 1024

teams:
  sv-gruenwald:
    folder: newsletter
    domain: links.sv-gruenwald.de
    qr:
      fg: '1a3a6e'
      bg: 'ffffff'
```

**Keys:**

- **Global:** `language` (`en`|`de`), `team`, `qr.format`, `qr.size`, `qr.fg`, `qr.bg`.
- **Per team** under `teams.<slug>`: `folder`, `domain`, `qr.format`, `qr.size`, `qr.fg`, `qr.bg`.

**Precedence:** flag, then team section, then global, then built-in default.

**Editing:**

- `config set` and `config unset` edit the file through the YAML node tree, so comments and order survive. Colours are written quoted.
- `config get` without a key prints every effective value with its source.

**Validation:**

- An unknown key, or a value outside its type or range (format, size 64–2048, colour `rrggbb`), is an error on `set` and on every load. The error names the file and the line.
- **Team sections:** a `teams.<slug>` section for a team the person is not in is ignored with a warning, not an error. Membership changes.

**Language:**

- `language` from the config, else the first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set: German when it starts with `de`, else English.
- **Windows:** the user's UI language. The builder picks the API.

### Credentials

- **Keychain:** `zalando/go-keyring`, service `kurze-url`, one entry per environment, so production and preview logins do not overwrite each other.
- **Fallback file:** `os.UserConfigDir()/kurze-url/credentials-<environment>.json` with mode `0600`. The CLI uses it only when the keychain is unavailable, and `login` says so once.
- **Refresh:** a refreshed refresh token is written back at once, and atomically when it goes to the file.

### Environments

- **Defaults:** production. The API is `https://api.kurze-url.app`. The production Supabase URL, the production CLI client ID and the shared hostname `go.kurze-url.app` are compiled in.
- **Overrides,** for development and preview: `KURZE_URL_API_URL`, `KURZE_URL_SUPABASE_URL`, `KURZE_URL_CLIENT_ID` and `KURZE_URL_SHARED_HOST`.
- **Protected previews:** API previews sit behind Vercel's deployment protection. When `KURZE_URL_VERCEL_BYPASS` is set, every API request carries it as `x-vercel-protection-bypass`. (My call.)
- **Credential key:** the environment name in the credential key comes from the Supabase URL, so switching overrides never reuses another environment's token.

### i18n

- **Catalogs:** `en` and `de` are embedded. A test fails when the two do not have the same keys, or when a key is used but defined in neither, mirroring the web app's discipline.
- **Untranslated:** command names, flags and `--help` text stay English. Cobra generates the help, and translating it doubles the catalog for little gain. Everything a command prints is translated.

### Libraries (builder may substitute with reason)

- `spf13/cobra` for commands and completion;
- `go.yaml.in/yaml/v3` for the config;
- `zalando/go-keyring` for the keychain;
- `golang.org/x/oauth2` for PKCE and the token exchange;
- a small browser opener, or `os/exec` per OS.

### API client

- **Hand-written:** a few typed calls over `net/http`, decoding `application/problem+json` errors.
- **Contract test:** fails when an endpoint, parameter or response field the CLI uses is missing from `apps/api/openapi.json`. The CLI cannot drift from the API unnoticed.
- **Timeouts:** every request has one, and `link create` is not retried automatically.

## Part 4 — CI and release

- **`.github/workflows/ci-cli.yml`**, triggered by `apps/cli/**`, `apps/api/openapi.json` (so the contract test sees the API drift) and the workflow itself:
  - `go vet`, `gofmt`, `golangci-lint`, `go test ./...`, `go mod tidy -diff` and govulncheck;
  - the same change detection as `ci-api.yml`, so the jobs report as skipped on unrelated pull requests.
  - Its job names become required checks in the `main` ruleset, a maintainer step.
- **`.github/workflows/release-cli.yml`** on `push` of tags `v*`:
  - goreleaser with `apps/cli/.goreleaser.yaml` builds linux, darwin and windows for amd64 and arm64;
  - `version`, the commit and the date go in through ldflags;
  - it publishes binaries, archives and `checksums.txt` to a GitHub Release using `GITHUB_TOKEN`.
  - Actions are pinned by SHA like every other workflow. The first release is `v0.1.0`.
- **`apps/cli/install.sh`**, fetched from `main` through raw.githubusercontent:
  - detects OS and architecture;
  - downloads the latest release's archive and `checksums.txt`, and verifies the checksum;
  - installs to `~/.local/bin`, or `$KURZE_URL_INSTALL_DIR`;
  - refuses to continue on a checksum mismatch.
  - On Windows the README points to the release page.
- **Vercel:** nothing changes. The API's ignore command already excludes `apps/cli`, and the web project does not see it.

## Testing

- **CLI unit tests:**
  - config precedence, round-trip with comments and validation;
  - language selection;
  - catalog parity;
  - slug resolution, including the ambiguous case;
  - stats windows, computed from a fixed series;
  - the QR parameter assembly, including no `size` for SVG;
  - the overwrite refusal;
  - the problem+json mapping;
  - the login callback: state mismatch, denial and timeout, through an `httptest` authorization server stub;
  - the contract test against `openapi.json`.
- **Web:** unit tests for the `next` guard, Storybook stories for the consent card states, and the e2e spec described in Part 2.
- **API:** if the audience list is needed (risk 2), tests for both audiences accepted and a foreign audience refused.
- **Manual, once per release candidate:**
  - a real `login` against production, `link create`, `stats`, `list` and `qr`;
  - `logout`, then a command that must ask for login again;
  - on macOS, Linux (keychain and file fallback) and Windows.

## Suggested sequence

Each step is its own pull request:

1. **Verification and wiring:** the Part 1 checks, the local `config.toml` change, the audience change if needed, and a "Verified" section added to this spec. The maintainer configures Production and Preview.
2. **Consent page and the web sign-out change,** with i18n, stories and e2e.
3. **The CLI itself** with `ci-cli.yml`.
4. **Release:** `.goreleaser.yaml`, `release-cli.yml`, `install.sh`, the README and the planning-doc amendments. Tagging `v0.1.0` is the maintainer's call.

## Left to the builder

- Package layout inside `apps/cli`.
- The fixed loopback ports, if fixed ports are needed.
- Table rendering details.
- The Windows locale API.
- The browser opener.
- Exact wording of messages, within the approved meaning.
- Whether `team list` and `whoami` share code.
- The goreleaser archive naming.
