# Link Password Management — Design

**Status:** approved 2026-09-08 **Amends:** `CLAUDE.md` (the API-surface summary; a new non-obvious constraint about the redirect cache and `has_password`; the rate-limit entry gains two values), `docs/planning/06-api-design.md` (`PUT`/`DELETE /v1/links/{link_id}/password` gain their settled response shapes), `docs/planning/05-database-schema.md` (the password policy, which that document left open).

The tenth implementation spec. Nine plans have merged: a maintainer creates a team, a Verein claims a custom domain, links redirect from `go.kurze-url.app`, and every authenticated page is addressed by the team's slug.

Password protection is the odd one out. Its read half is complete and has been for some time: `link.password_hash` exists, `auth.HashPassword` and `auth.VerifyPassword` implement Argon2id at OWASP's second recommended profile, `GET /{slug}` branches to an interstitial when a link is protected, and `GET|POST /{slug}/verify` renders that page, verifies the password, and records the click only once verification succeeds. `audit.ErrForbiddenMetadata` already refuses to write a plaintext password or a hash into the audit log.

What does not exist is any way to set a password. There is no endpoint, no query, no audit action, and no frontend. A column that only a hand-written `UPDATE` can populate is not a feature, and golden rule 3 lists Argon2id password protection as MVP scope rather than something to retrofit. This spec builds the write half.

## Goal

A team member with the editor role can protect a link with a password, change that password, and remove the protection — from the dashboard, in German or English, with the change taking effect on the next visitor rather than after the cache expires.

## Scope

### In scope

- `PUT /v1/links/{link_id}/password` and `DELETE /v1/links/{link_id}/password`.
- A password policy, enforced in the API and mirrored in the browser for immediate feedback.
- Three audit actions, and the taxonomy entries that make them writable.
- Redirect-cache invalidation on both endpoints.
- A second, IP-independent rate-limit axis on `POST /{slug}/verify`, and a rate limit on the mutation itself.
- Two new `cache.Client` methods, because the existing limiter cannot express "check before, count only on failure".
- A password card on the link detail route, and a lock badge in the link list.

### Out of scope, and where each lands

- **Setting a password when the link is created.** `POST /v1/teams/{team_id}/links` is unchanged; see "The two-step window" below.
- **`GET /v1/links/{link_id}/qr` and `GET /v1/links/{link_id}/stats`.** The other two unbuilt endpoints from the planned surface. Separate features, separate specs.
- **Changing the Argon2id parameters.** `auth.DefaultParams` stays at 19 MiB, 2 iterations, 1 lane, and the reasoning in its docstring is unchanged by this work.
- **A rate limit on `signInWithOtp`.** Recorded as a gap on 2026-09-08 with the rate-limit values; unrelated to link passwords.

## Global constraints

Inherited and not re-litigated here:

- No RLS. Every query filters by `team_id`; the check lives in Go.
- A non-member gets 404, never 403. So does a member whose `link_id` belongs to another team.
- The redirect path is the hot path. This spec adds nothing to `GET /{slug}`; the two new Redis reads live on `POST /{slug}/verify`, which is not the hot path and is reached only for protected links.
- No hardcoded user-facing string; English and German ship together.
- WCAG 2.1 AA, gated in CI at two levels.
- Conventional Commits; `pnpm format` before every commit; the hooks are not bypassed.

## The API surface

Both endpoints authorize through `authz.LinkEditorScope`, the same scope `PATCH /v1/links/{link_id}` uses. A password is a property of a link, and the person who may change where a link points may also decide who reaches it.

**`PUT /v1/links/{link_id}/password`** takes `{"password": "…"}`. The length bounds are deliberately **not** declared as `minLength`/`maxLength` on the schema: Huma would then reject an out-of-range value with its own error shape, and `too_short` and `too_long` would be unreachable through HTTP while still existing in the policy. One enforcement point and one error shape is worth more here than the schema annotation, so the range lives in the field's `doc:` string and `ValidatePassword` owns all five reasons. 128 against 129 characters is not a denial-of-service boundary; Huma's general body-size limit is what guards that. It answers **200** with the full `Link` body — the same `LinkOutput` `updateLink` returns. A 204 was considered and rejected: the client needs `has_password` back, and returning the link saves it a refetch it would otherwise always make.

**`DELETE /v1/links/{link_id}/password`** takes no body and answers **200** with the `Link` body, symmetric with `PUT`. On a link that has no password it answers 200 with the link unchanged. `DELETE` is idempotent by definition, and a 404 there would say nothing the caller does not already know while forcing them to special-case it.

A policy violation is **422** with `huma.ErrorDetail{Location: "body.password", Value: "<reason>"}`. The typed-`Value`-keyed-by-`Location` pattern is the one `deleteDomain` established and `CLAUDE.md` records as deliberate: the free-text `detail` stays free to reword, and the frontend keys its message off `Value`. The reasons are `too_short`, `too_long`, `too_repetitive`, `derived_from_context` and `too_common`.

`PATCH /v1/links/{link_id}` continues to reject a `password` field with 422, as `UpdateLinkInput`'s docstring already explains.

## The password policy

The policy lives in `internal/auth`, beside the hashing, as `ValidatePassword(plain string, ctx PolicyContext) error`. It is a property of link passwords, not of HTTP, and putting it in `internal/api` would make it unreachable from anywhere that is not a handler.

`PolicyContext` carries the four values the rules compare against: the link's slug, the destination URL, the team's name and the team's slug.

### Why these rules and not others

Link passwords are shared out of band, with a group, by people who are not thinking about entropy. A Verein writes "Passwort: Sommerfest26" in a newsletter. That use case wants short and memorable, and a policy that fights it will be worked around — the maintainer will get asked to turn it off. So the rules target the one failure that actually happens, which is not weakness in the abstract but **predictability from context**. `Sommerfest26` is not a weak password because it is twelve characters of mixed case and digits; it is weak because it is the first thing anyone who has seen the link would guess.

Character-class rules are deliberately absent. NIST SP 800-63B stopped recommending them because they push people toward predictable substitutions and buy nothing measurable.

### The rules

1. **Length**: at least 8 characters, at most 128. Counted in runes, not bytes, so an umlaut is one character. The upper bound bounds the request, not the hash: Argon2id's cost comes from its memory and time parameters, not from input length.
2. **Repetition**: at least 4 distinct characters, counted over the raw password's runes rather than the normalized form — normalizing first would let `!!!!a!!!!` collapse to a single character and fail a password that is merely odd. This is what stops `!!!!!!!!` and `aaaaaaaa`, both of which clear the length rule. NIST explicitly permits blocking repetitive and sequential characters; this is not a composition rule.
3. **Context**: rejected if the normalized password contains, or is contained by, any normalized context token. Both directions, so `sommerfest` falls to the slug `sommerfest-2026` and `svgruenwaldsommerfest2026` falls to the team slug `sv-gruenwald`.
4. **Common**: rejected if the normalized password equals an entry in an embedded list.

### Normalization

Both the password and every context token pass through the same function before rules 3 and 4 compare them: transliterate German characters (`ä`→`ae`, `ö`→`oe`, `ü`→`ue`, `ß`→`ss`, and their upper-case forms), lower-case, then drop everything outside `[a-z0-9]`.

Transliteration is load-bearing rather than decorative. Without it, a team named `SV Grünwald` does not catch the password `Gruenwald2026`, which is exactly the password that team will choose. It is the same transliteration `suggestTeamSlug` performs in `apps/web/src/lib/team-slug.ts` and the team-slug backfill performed in SQL; this is its third implementation, in Go, and that is worth knowing when one of them changes.

### Context tokens

The token set is built from `PolicyContext`:

- The link's slug.
- The team's slug.
- The team's name.
- The destination URL's hostname with a leading `www.` removed and its last label dropped, so `https://www.sv-gruenwald.de/verein` contributes `sv-gruenwald` rather than `de`.

Each of those contributes both its whole normalized form and its parts, split on hyphens, dots and spaces before normalizing. Only tokens whose normalized form is at least **4** characters are kept: shorter ones — `sv`, `e.V.`, a two-letter country label — would reject almost every password a person could type.

Rule 3 is skipped when the normalized password is shorter than 3 characters, which can only happen for a password made almost entirely of punctuation. Such a password is already refused by rule 2.

### The common list

`internal/auth/common-passwords.txt`, embedded with `//go:embed`, a few hundred entries covering the obvious in German and English — `passwort`, `password`, `12345678`, `geheim`, `qwertz`, `letmein` and their kin. Not a hundred-thousand-entry corpus: the binary ships to a serverless function, and the context rules are the half that earns its size.

Comparison is **equality against the normalized password**, not substring. Substring matching would reject `meinpasswortistlang` for containing `passwort`, which is a worse password than it looks but not one this rule should be deciding about, and the false rejections would be hard to explain to a Verein.

The honest limit, recorded here so nobody rediscovers it as a bug: `passwort1` passes every rule. It is 9 characters, has 8 distinct ones, is not derived from context, and is not equal to a list entry. The policy raises the floor; it does not guarantee a good password. What bounds the damage is the rate limiting below.

## Rate limiting

Two new values, documented in `apps/api/.env.example` the way the rest were settled on 2026-09-08 — each with what it protects and what it explicitly does not.

### `RATE_LIMIT_PASSWORD_SET_PER_HOUR=20`, per user

`PUT` computes an Argon2id hash: 19 MiB of memory and two passes, per call. An authenticated member looping the endpoint is a CPU and memory amplifier against the function, and no other limit covers it — `RATE_LIMIT_LINK_CREATE_PER_MIN` guards a different route. Twenty an hour is far above any real use, because a Verein sets a link's password once and rarely changes it.

Key `rl:password-set:<user id>`, checked with the existing `Allow`, before the hash is computed. Fails closed, matching the rest of the password surface.

### `RATE_LIMIT_PASSWORD_FAILURES_PER_HOUR=100`, per link

The existing `RATE_LIMIT_PASSWORD_PER_MIN=5` is scoped per link **and** per client IP, so an attacker who rotates addresses is bounded by nothing. With the policy above, distributed guessing is no longer the pressing threat — 8 characters that are not derived from context outlast any rate anyone can drive over HTTP. What is still unbounded is the **cost to us**: every guess is 19 MiB and two Argon2id passes, triggered by someone with no account.

So this axis is sized to protect the function, not the password. A hundred failures an hour per link bounds the amplification at a known number while being far too loose to be a practical denial-of-service weapon against a Verein's own link: an attacker would have to sustain a hundred failures an hour indefinitely, and the sliding window recovers within the hour once they stop.

**Only failures count.** A visitor who knows the password consumes nothing, so a link whose password a whole Verein has been given never approaches the limit no matter how many people follow it.

Key `rl:pwfail:<hostname>:<slug>`. Checked before the hash is computed; incremented only after `VerifyPassword` returns false. Fails closed, like the per-IP check beside it.

### The new cache primitive

`cache.Client.Allow` checks and increments atomically in one script, which is correct for every existing caller and cannot express "count only on failure". This spec adds two methods and two Lua scripts:

- `WithinLimit(ctx, key, limit, window) (bool, error)` — reads the two window counters and reports whether the estimate is below the limit. Two Redis commands, no writes.
- `Increment(ctx, key, window) error` — increments the current window counter and refreshes its expiry. Two Redis commands.

Both are generic; the caller names the key. **The two scripts must use the same window-slot arithmetic as `lua/ratelimit.lua`** — `floor(now_ms / (window_seconds * 1000))`, with the previous slot weighted by the elapsed fraction. A peek and an increment that disagreed about where a window begins would count into a slot nobody reads, and the limit would silently never fire. That agreement is the thing to test.

Four extra Redis commands on a verify attempt. The verify path is not the hot path and is reached only for protected links, so this does not touch the command-budget arithmetic recorded for `GET /{slug}`.

### Order of checks in `HandleVerifySubmit`

1. Per-link-per-IP limit (existing, 5/min) — fails closed.
2. Per-link failure limit (new, 100/hour) — fails closed.
3. Load the link from Postgres and parse the form.
4. `auth.VerifyPassword`.
5. On failure only: increment the failure counter, then render the form with an error.

Both limits are consulted before step 4, which is the expensive one. Step 5 runs after, because the counter's meaning is "failures", not "attempts".

## Redirect-cache invalidation

`link.Cached` carries `HasPassword`, and the redirect path reads that field out of Redis to decide whether to redirect or render the interstitial. So a link that has just been given a password keeps redirecting straight through, with no prompt, until its cache entry expires — up to `LinkCacheTTL`, which is one hour.

Both endpoints therefore call `d.invalidateLink(ctx, hostname, slug)` after the transaction commits, exactly as `updateLink` does. The slug does not change here, so unlike `updateLink` there is only ever one key to clear, and there is no not-found sentinel to worry about under a second key.

This is the sharpest correctness risk in the feature, and the one whose failure is silent and looks like nothing at all: the API answers 200, the audit log records the change, the dashboard shows a protected link, and visitors keep sailing through for an hour. It gets a dedicated test that goes through the redirect handler rather than asserting that a function was called.

## Audit

Three new actions, added to the constant block and to `knownActions`:

- `link.password_set` — the link had no password before.
- `link.password_changed` — it had one.
- `link.password_removed` — `DELETE`.

Distinguishing set from changed is what `docs/planning/06-api-design.md` asked for, and the handler learns it for free: it re-reads the row inside the transaction the way `updateLink` does, so it knows whether `password_hash` was null.

`Metadata` is empty. `ErrForbiddenMetadata` already refuses a plaintext password or a hash, and there is nothing else about this change worth recording — "which fields moved" is the whole action name here, unlike a `PATCH` that can move several at once.

The entity type is the existing `EntityLink`, with the link's own id.

## Database

One new query in `internal/db/queries/link_crud.sql`:

```sql
-- name: SetLinkPassword :one
update link
set password_hash = $3,
    updated_at    = now()
where id = $1
  and team_id = $2
returning <the same column list UpdateLink returns>;
```

The returned column list is `UpdateLink`'s, copied verbatim rather than abbreviated, so `linkResponse` consumes the generated row unchanged and the two queries stay diffable against each other. One query serves both endpoints: `DELETE` passes `null`. The `team_id` predicate is there for the same reason it is on every other query in this file — the scope has already proved membership, and the filter is the second lock, not the first.

No migration. `link.password_hash` has existed since the first schema.

`GetLinkForAPI` inside the same transaction supplies the previous value, which decides the audit action and provides the `hostname` and `slug` the invalidation needs.

## Frontend

### Where it lives

A `PasswordProtection` component on the link detail route, `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, as its own card beside `<LinkForm>` rather than a field inside it.

It has to be separate, for a reason that is not stylistic. `<LinkForm>` maps to `PATCH`, which excludes `password` deliberately. And the value is write-only: the server holds an Argon2id hash and cannot produce the password, so there is no initial value to seed a controlled input with. A field that is always blank inside a form that otherwise round-trips the link's current state would read as "the password is empty".

### States

**Unprotected** — a password input and a submit control. The card explains what protection does, because a Verein board member should not have to infer it.

**Protected** — a statement that the link is protected, a control to change the password, and a control to remove protection. Removal goes through the existing `<ConfirmDelete>` pattern: it is destructive, silent from the visitor's side, and easy to hit by accident.

### The mirrored policy

`apps/web/src/lib/link-password.ts` reimplements the rules so a violation is caught before a request is made. The API remains the enforcement point: a 422 still renders under the field through `classifyApiError`, keyed off `ErrorDetail.Value`.

That is a second implementation of the same rules, and it will drift. This is accepted rather than solved — a shared fixture read by both a Go and a Vitest suite would couple two apps' test directories to buy protection against a divergence whose worst outcome is a message the user sees a round trip later than they could have. The drift is recorded under "Consequences to expect".

### Elsewhere

`link-list` gains a lock badge for `has_password`, which the list response already carries. New server functions in `apps/web/src/server/links.ts` following the existing shape; `packages/api-client` regenerated from the OpenAPI spec; new keys in both `apps/web/src/i18n/locales/en.json` and `de.json`.

## The two-step window

Creating a protected link takes two requests: `POST /v1/teams/{team_id}/links`, then `PUT …/password`. Between them the link is live and unprotected.

That window is not closed here. Doc 06 argues that the password belongs on exactly one route — so that it maps to its own audit action and its own rate limit — and the folders-and-tags spec cites that separation as the precedent for rejecting a tag subresource. Accepting a `password` field on create would give the field two enforcement points, two audit shapes, and two places for the policy to be applied or forgotten.

Against that, the exposure is small. The frontend issues the two calls back to back, so the window is milliseconds. A generated slug is 8 characters from a 32-symbol alphabet that nobody has been told. A custom slug is guessable in principle, but somebody would have to be probing that exact slug in that exact window.

Reopen this if bulk creation or import arrives — a batch that creates a hundred protected links would turn milliseconds into a real interval, and a create-time password would then be worth its cost.

## Testing

### Go

- **Policy**: a table test with one case per rule, including every context source (link slug, team slug, team name, destination hostname), both containment directions, the transliteration cases (`Grünwald` against `Gruenwald2026`), the short-token exclusion, and the `passwort1` case asserted as **accepted**, so the policy's documented limit is pinned rather than accidentally tightened later.
- **Endpoints**: role enforcement, 404-not-403 for a link in another team, the 422 body's `Location` and `Value`, the 200 body's `has_password`, and `DELETE` on an unprotected link answering 200.
- **Audit**: three cases producing three different actions, and an assertion that the metadata is empty.
- **Failure counter**: a correct password does not consume it; an incorrect one does; the limit refuses before `VerifyPassword` is reached.
- **Cache primitive**: `WithinLimit` and `Increment` agree on the window slot — increment to the limit through `Increment`, then assert `WithinLimit` reports false.
- **Invalidation**: set a password through the API, then drive `HandleRedirect` and assert it renders the interstitial rather than a redirect. Through the handler, not through a spy on `invalidateLink`.

### Web

Vitest and RTL for the card's three states, the mirrored policy's rejections, and the 422 path rendering the API's reason. One Storybook story per state, which is what carries the accessibility check.

### E2E

The dashboard side only: protect a link, see the state change, remove protection. The interstitial itself is unreachable from e2e — Preview's shared hostname is `short.invalid`, which deliberately does not resolve, and that is load-bearing for the `ShortUrlNotice` spec. The interstitial stays covered by Go tests against the real handler, as it is today.

## Consequences to expect

- **A third German transliteration.** `apps/web/src/lib/team-slug.ts` has one, the team-slug migration had one in SQL, and this adds one in Go. They serve different callers and share no code. If the rules ever change, all three change.
- **A second implementation of the password policy**, in TypeScript, which will drift from the Go one. The API is the enforcement point, so drift costs a late error message rather than a wrong outcome.
- **Two more rate-limit values to validate against real usage**, joining the six settled on 2026-09-08 with the same caveat: they are set from reasoning, not measurement.
- **`passwort1` is a valid link password.** Written down twice on purpose.
- **The redirect cache now depends on a third write path.** Creating, updating and now protecting a link all have to invalidate. A fourth will be easy to forget.
