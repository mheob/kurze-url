# CLI Part 1 — OAuth Server Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn on Supabase's OAuth 2.1 server in the local stack, measure the three risks the CLI spec names against it, record the results in the spec, and change the API's audience check only if the measured tokens require it.

**Architecture:**

- **Probe:** a re-runnable Node script, `scripts/oauth-server-probe.ts`, drives a complete public-client flow against the local Supabase stack over plain HTTP:
  - register a client;
  - authorize;
  - approve consent as a signed-in user;
  - exchange the code;
  - refresh;
  - sign out in each scope. It prints its findings as one JSON object.
- **Spec:** the findings go into a "Verified" section of the spec, together with a runbook for the maintainer's dashboard steps.
- **API:** the API changes in Task 4 only if the measured `aud` claim is not `authenticated`.

**Tech Stack:**

- Supabase CLI local stack (GoTrue OAuth 2.1 server, beta);
- Node 24 with type stripping and `fetch`;
- Go 1.27 with `golang-jwt/jwt/v5`, for the conditional Task 4.

**Spec:** `docs/superpowers/specs/2026-10-23-cli-design.md`, Part 1, "Verification before anything else is built (risks)".

## Global Constraints

- **Commits:** all git writes go through GitButler (`but commit -b <branch> -m "…" <ids>`), never `git add`/`commit`/`push`. Conventional Commits, subject at most 50 characters, no co-author or generator footer. Run `pnpm format` before every commit, because `but commit` skips the Lefthook hooks.
- **Branch:** `feat/oauth-server` for every task; one pull request at the end.
- **Local only:** the probe talks to the local stack and nothing else. It refuses any API URL whose host is not `127.0.0.1` or `localhost`, before the first request.
- **No secrets in output:** keys, tokens, authorization codes and PKCE verifiers never reach stdout, stderr or a file. The probe reads the local stack's keys from `supabase status -o json` at runtime. Production and Preview keys are never used.
- **Fixed values from the spec:**
  - client name `kurze-url CLI`;
  - public client, `token_endpoint_auth_method: "none"`, PKCE S256;
  - registered redirect URI `http://127.0.0.1:53682/callback`;
  - consent path `/oauth/consent`;
  - Windows Credential Manager limit 2560 bytes per entry.
- **Local auth config, as checked in:** `site_url = "http://127.0.0.1:3000"`, `enable_refresh_token_rotation = true`, `refresh_token_reuse_interval = 10` (seconds), `jwt_expiry = 3600`.
- **Endpoints:** the consent endpoints are those `@supabase/auth-js` 2.118 calls:
  - `GET /auth/v1/oauth/authorizations/{id}`;
  - `POST /auth/v1/oauth/authorizations/{id}/consent` with `{"action":"approve"}`;
  - `GET|DELETE /auth/v1/user/oauth/grants`;
  - `POST /auth/v1/admin/oauth/clients`. Authorize and token endpoints come from the server's discovery document, not from a hardcoded path.

## Review Focus

1. **An API URL that is not local:** for example, a pasted Preview URL. The probe must exit 1 with a clear message before any request. Task 2, step 4 runs it with `--api-url https://example.supabase.co`.
2. **A probe that fails halfway:** it must still delete the user and the client it created. Task 2, step 5 forces a failure and checks with the admin API that nothing is left.
3. **A finding that prints a credential:** the report carries lengths, claim names and claim values that are not credentials, never a token. Task 2, step 4 greps the output for `eyJ` and `sb_`.
4. **An unregistered redirect URI:** Supabase may answer it with a 400, a 302 to an error page, or a 302 to the URI with `error=`. The probe records the status and the redirect target's origin, path and `error` parameter. It never concludes "accepted" from a 302 alone. Task 2's finding `redirect.otherPort` asserts this shape.
5. **Timing in the reuse-interval check:** a reused refresh token is valid inside `refresh_token_reuse_interval` and invalid after it. The probe reads the interval from `supabase/config.toml` with a line match, echoes it in `config.refreshTokenReuseIntervalSeconds`, and waits interval + 2 seconds, not a hardcoded 10. Task 2's finding `refresh.reuseAfterInterval` carries the wait it used.

---

### Task 1: Enable the OAuth server in the local stack

**Files:**

- Modify: `supabase/config.toml`, the `[auth.oauth_server]` block, about line 378

**Interfaces:**

- Produces: a running local stack whose discovery document answers at `http://127.0.0.1:54321/.well-known/oauth-authorization-server/auth/v1`. If that 404s, use `http://127.0.0.1:54321/auth/v1/.well-known/oauth-authorization-server`, and record which path worked in the commit body.

- [ ] **Step 1:** set `enabled = true` and keep `authorization_url_path = "/oauth/consent"` and `allow_dynamic_registration = false`. Add a two-line comment above the block: the CLI logs in through this server (`docs/superpowers/specs/2026-10-23-cli-design.md`). Production and Preview are switched on in their dashboards, not from this file.
- [ ] **Step 2:** restart the stack with `supabase stop`, then `supabase start`, without `--no-backup`, so local data survives. Expected: `supabase start` exits 0.
- [ ] **Step 3:** run `curl -s <discovery URL> | jq '{issuer, authorization_endpoint, token_endpoint, code_challenge_methods_supported}'`. Expected: all four fields present, and `code_challenge_methods_supported` contains `S256`.
- [ ] **Step 4:** run `cd apps/api && TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres go test ./internal/db/... -count=1`. Expected: PASS, so the restart broke nothing.
- [ ] **Step 5:** commit `supabase/config.toml` with the message `feat(auth): enable the local oauth server`.

### Task 2: OAuth server probe

**Files:**

- Create: `scripts/oauth-server-probe.ts`
- Modify: `package.json`, adding the script `"probe:oauth": "node scripts/oauth-server-probe.ts"` next to `check:vite-plus`

**Interfaces:**

- Consumes: Task 1's discovery URL.
- Produces: `pnpm probe:oauth [--api-url URL]` prints one JSON object to stdout and exits 0 when the probe ran to the end. Unexpected findings still exit 0. It exits 1 when setup fails or the URL is not local. Top-level keys are fixed, because Task 3 reads them:
  - `config`: `{ refreshTokenReuseIntervalSeconds, siteUrl }`, both read from `supabase/config.toml` by line match, with no TOML dependency;
  - `discovery`: `{ path, authorizationEndpoint, tokenEndpoint }`;
  - `redirect`: `{ registeredPort, otherPort, localhostName }`. Each is `{ status, locationOrigin, locationPath, error }`; `locationPath` must show `/oauth/consent` for an accepted request;
  - `token`: `{ claims: { aud, iss, role, client_id, session_id_present, lifetimeSeconds }, accessBytes, refreshBytes, combinedJsonBytes }`;
  - `refresh`: `{ rotates, reuseWithinInterval, reuseAfterInterval: { waitedSeconds, status } }`;
  - `consent`: `{ rememberedOnSecondAuthorize, detailsShape }`. `detailsShape` is the sorted key list of the second `getAuthorizationDetails` response;
  - `logout`: `{ cliLocalEndsCli, cliLocalKeepsWeb, webGlobalEndsCli, grantRevokeEndsCli }`. Each is a refresh attempt's HTTP status after the action;
  - `keychain`: `{ fitsWindowsLimit, limitBytes: 2560 }`.

- [ ] **Step 1: Write the script.**
  - **Shape:** a header comment in the style of `scripts/check-vite-plus-versions.ts`: what it proves, how to run it, and that it is local-only. Typed `Readonly` records, no dependencies beyond Node built-ins.
  - **Setup:** read `API_URL`, `ANON_KEY` and `SERVICE_ROLE_KEY` from `supabase status -o json` through `node:child_process` `execFileSync`. `--api-url` overrides only the URL, which exists for the refusal check.
  - **Users:** create the user through `POST /auth/v1/admin/users` with `email_confirm: true` and a random password. Sign in two separate "web" sessions with `POST /auth/v1/token?grant_type=password`. One serves the CLI's consent; the other plays the browser that `webGlobalEndsCli` signs out.
  - **Client:** register it through `POST /auth/v1/admin/oauth/clients` with the spec's values and `grant_types: ["authorization_code","refresh_token"]`, `response_types: ["code"]`.
  - **Authorize:** call it with `redirect: 'manual'`, a fresh S256 pair and a random `state`. Read `authorization_id` from the `Location` query.
  - **Order:** run the flows so that each logout finding starts from a fresh CLI session. A new authorize, approve and exchange for each logout case, which also yields `consent.rememberedOnSecondAuthorize`.
  - **Cleanup:** delete the client and the user in a `finally`.
- [ ] **Step 2:** `pnpm typecheck` and `pnpm check`. Expected: both pass. Root `tsconfig.json` already includes `scripts/**/*.ts`.
- [ ] **Step 3:** run `pnpm probe:oauth > /tmp/oauth-probe.json` with the stack running. Expected: exit 0, and `jq 'keys'` lists exactly `config, consent, discovery, keychain, logout, redirect, refresh, token`.
- [ ] **Step 4 (Review Focus 1, 3):**
  - Run `pnpm probe:oauth --api-url https://example.supabase.co`. Expected: exit 1 and a message naming the refused host. No request leaves the machine.
  - Run `grep -cE 'eyJ|sb_' /tmp/oauth-probe.json`. Expected: `0`.
- [ ] **Step 5 (Review Focus 2):**
  - Temporarily make the token exchange throw, for example with a wrong `code_verifier` behind a `--fail-after-client` flag that stays in the script for this check.
  - Run the probe, expecting exit 1.
  - Then `GET /auth/v1/admin/oauth/clients` and `GET /auth/v1/admin/users`. Expected: no client named `kurze-url CLI` and no probe user left.
- [ ] **Step 6:** commit both files with the message `feat(scripts): add the oauth server probe`.

### Task 3: Record the findings in the spec

**Files:**

- Modify: `docs/superpowers/specs/2026-10-23-cli-design.md`, a new `## Verified (YYYY-MM-DD)` section after Part 1's risks, plus any Part 1 to Part 4 sentence a finding changes

**Interfaces:**

- Consumes: `/tmp/oauth-probe.json` from Task 2, step 3. Re-run it if missing.
- Produces: the spec decisions later plans rely on:
  - the loopback strategy, ephemeral or fixed ports, and whether to use `127.0.0.1` or `localhost`;
  - the `logout` endpoint and scope;
  - whether the CLI may cache the access token in the keychain;
  - whether Task 4 is needed.

- [ ] **Step 1: Write the "Verified" section.** It is a table with one row per finding: the measured value, and what it decides. Fill it in by these rules:
  - **Loopback ports:** if `redirect.otherPort` lands on `/oauth/consent`, the CLI binds an ephemeral port and registers one URI. Otherwise it uses the fixed ports `53682`, `53683` and `53684`, all three registered.
  - **Hostname:** use `localhost` only if `redirect.localhostName` behaves like `registeredPort` and the spec prefers it. Otherwise use `127.0.0.1`.
  - **Audience:** if `token.claims.aud` is `authenticated` and `iss` equals `<API_URL>/auth/v1`, write "API unchanged" and mark Task 4 skipped. Otherwise Task 4 runs.
  - **Refresh tokens:** if `refresh.rotates` is true, keep the spec's write-back rule. Note `reuseWithinInterval` as the margin for two parallel CLI runs.
  - **Logout:** the CLI uses `POST /auth/v1/logout?scope=local` if `logout.cliLocalEndsCli` is 401 or 400 and `cliLocalKeepsWeb` is 200. Otherwise record what does end only the CLI session.
  - **Web sign-out:** `webGlobalEndsCli` confirms or refutes Part 2's sign-out reasoning. If it is 200, the CLI survives a global sign-out, and the spec's checkbox text must say so.
  - **Keychain:** if `keychain.fitsWindowsLimit` is false, the keychain holds the refresh token only, as the spec already allows.
  - **Consent page:** `consent.detailsShape` tells Part 2 how to tell "already consented" apart.
- [ ] **Step 2: Add a maintainer runbook,** `## Maintainer setup (Production and Preview)`. It lists the dashboard steps with exact values, for each of the two projects:
  - Authentication → OAuth Server: enable, authorization path `/oauth/consent`;
  - Authentication → OAuth Apps: client `kurze-url CLI`, public, with the redirect URIs Step 1 decided;
  - Authentication → URL Configuration: the magic-link callback with the `next` parameter stays inside the allowlist. Name the exact pattern the current entries use, and add `**` if they lack it.
  - Say that the Production client ID is compiled into the CLI and the Preview one goes into `KURZE_URL_CLIENT_ID`. Neither is secret.
- [ ] **Step 3:** `pnpm format`, then commit the spec with the message `docs(cli): record the oauth server findings`.

### Task 4 (only if Task 3 says so): Accept a list of JWT audiences

Skip this task entirely when Task 3 recorded "API unchanged". The skip is a ruling for the ledger, not a failure.

**Files:**

- Modify: `apps/api/internal/auth/jwt.go` (`NewVerifier`, `Verifier.audience`)
- Modify: `apps/api/internal/config/config.go`, where `JWTAudience` is set from `SUPABASE_JWT_AUDIENCE`, about line 204
- Modify: every caller of `NewVerifier`; find them with `grep -rn "NewVerifier(" apps/api`
- Modify: `apps/api/.env.example`, the `SUPABASE_JWT_AUDIENCE` entry
- Modify: `CLAUDE.md`, the "JWT verification" convention bullet, one sentence
- Test: `apps/api/internal/auth/jwt_test.go`, `apps/api/internal/config/config_test.go`

**Interfaces:**

- Produces:
  - `func NewVerifier(ctx context.Context, jwksURL, issuer string, audiences []string) (*Verifier, error)`. It returns an error when `audiences` is empty.
  - `config.Config.JWTAudiences []string` replaces `JWTAudience string`.

- [ ] **Step 1: Write the failing tests**, reusing the key and token helpers already in `jwt_test.go`:
  - `TestVerifierAcceptsAnyConfiguredAudience`: a verifier built with `[]string{"authenticated", "<measured aud>"}` accepts a token for each.
  - `TestVerifierRefusesAForeignAudience`: a token with `aud: "someone-else"` fails.
  - `TestNewVerifierRequiresAnAudience`: `nil` and `[]string{}` both error.
  - `TestAudiencesAreACommaSeparatedList`, in `config_test.go`: `" authenticated , <measured aud> ,"` gives `["authenticated", "<measured aud>"]`, and unset gives `["authenticated"]`.
- [ ] **Step 2:** run `cd apps/api && go test ./internal/auth/ ./internal/config/ -count=1`. Expected: FAIL to compile, or the new tests fail.
- [ ] **Step 3:** implement it with `jwt.WithAudience(audiences...)` if the module's `WithAudience` is variadic with any-of semantics. Check that in `$(go env GOMODCACHE)/github.com/golang-jwt/jwt/v5@v5.3.1/parser_option.go` before relying on it. Otherwise loop over the audiences after parsing.
  - Split the config value on `,`, trim spaces, and drop empty entries.
  - In `.env.example`, document the comma form and why the OAuth server's tokens need it.
- [ ] **Step 4:** run `go test ./... -count=1` with `TEST_DATABASE_URL` set, as in Task 1 step 4, then `go vet ./...` and `golangci-lint run`. Expected: PASS and 0 issues.
- [ ] **Step 5:** commit with the message `feat(api): accept several jwt audiences`.

### After the tasks

- Open one pull request from `feat/oauth-server` with the `create-pr` skill. The title is `feat(auth): enable the supabase oauth server`. The body carries the probe's findings table, and Testing lists the probe run.
- Production and Preview are untouched by this pull request. The maintainer works through the runbook from Task 3 before Part 2's e2e can pass.
