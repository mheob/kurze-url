# Safe Browsing Scanning — Design

**Status:** approved in conversation 2026-10-03, awaiting review of this written form.

**Amends:**

- `CLAUDE.md`: golden rule 2 gains one bounded exception; a new non-obvious-constraint entry; three API environment variables.
- `docs/planning/01-architecture.md`: the security section's scanning line gains its mechanism.
- `docs/planning/02-external-services-and-hosting.md`: the Safe Browsing section records the API version and the terms that shaped this design; a third Better Stack heartbeat.
- `docs/planning/05-database-schema.md`: the new `link` and `link_scan_result` columns.
- `docs/planning/06-api-design.md`: the nested verdict on `GET /v1/links/{link_id}` and the 409 on a flagged link's `state`.
- `docs/planning/07-repo-structure-and-tooling.md`: the new secrets and the workflow.
- `docs/planning/08-legal-and-compliance.md`: what the Datenschutzerklärung says about Google.

Golden rule 3 lists async Safe Browsing scanning as MVP scope, "not later". Everything around it already exists:

- `link.state` admits `flagged`;
- the redirect path answers a flagged link with a 403 "Link blocked" page in English and German;
- `link_scan_result` has been in the schema since the first migration.

Nothing writes any of it. A link to a phishing page is forwarded through `go.kurze-url.app` unchecked today. A domain's reputation is indivisible, so one such link can get the shared hostname blocklisted and take every Verein's links down with it. That is the hazard the invitation-only decision of 2026-09-08 names.

## Goal

Every active link's destination is checked against Google Safe Browsing:

- shortly after it is created;
- whenever its destination changes;
- daily thereafter.

A link Google reports is blocked at the redirect only while Google's report is fresh enough to satisfy Google's terms. The normal redirect of an active link never waits on Google.

## Decisions taken in conversation

| Question | Decision | Why |
| --- | --- | --- |
| How to block without breaking Google's 30-minute rule | Re-check on demand: a flagged link's redirect asks Google again when its last confirmation is older than the rule allows | The terms forbid blocking on stale data; only flagged links pay for it |
| Which API | Safe Browsing **v5 `hashes.search`** | Only 4-byte hash prefixes leave the server, never a URL; v4 ends 2027-03-31 |
| When to check | Immediately after create or a destination change (best-effort goroutine), plus a sweep every 30 minutes from GitHub Actions | Vercel does not guarantee work after the response; the sweep catches what the goroutine loses and re-checks daily |
| Who lifts a flag | Only Google, through a later clean check | A team that spreads phishing must not be able to switch the block off |
| How people learn of a flag | Link detail banner, list badge, audit entries with a system actor, and an Error log that reaches the maintainer through Sentry | No team e-mail: it would spend the shared Resend quota the magic links depend on |
| Where verdicts live (approach A) | `link.state` stays the only switch; `link` records when and what was last checked; `link_scan_result` gets a row only when the verdict changes; the 30-minute confirmation is a short Redis key | Smallest change to the redirect path, slow table growth, full history |

## Facts this design rests on

All checked on 2026-10-03; sources are in the research notes this spec was written from.

- **Terms, the 30-minute rule** (developers.google.com/safe-browsing/terms): "You may not treat a URL from Google's list as an unsafe web resource, such as by showing users a warning about the site or blocking access to it, unless your application has received from Google updated information (via the applicable API method) within the past thirty minutes."
- **Terms, commercial use:** "Unless you have a separate agreement with Google, you may not use the Safe Browsing API for commercial purposes." This is the reading accepted on 2026-09-01, and it stands.
- **Warnings** must be qualified ("suspected", "possible"), link to Google's definition of the threat, and carry the line "Advisory provided by Google". The product must say that Google cannot guarantee its information is complete and error-free.
- **`hashes.search` (v5):**
  - Request: `GET https://safebrowsing.googleapis.com/v5/hashes:search` with repeated `hashPrefixes` (base64 of 4 bytes each), at most 1000 per request.
  - Response: `fullHashes[]`, each a 32-byte `fullHash` with `fullHashDetails[]` (`threatType`, `attributes`), plus one `cacheDuration`.
  - An empty result is HTTP 200.
  - The client canonicalizes, builds the expressions, hashes, and compares full hashes itself.
- **Threat types:** `MALWARE`, `SOCIAL_ENGINEERING`, `UNWANTED_SOFTWARE`, `POTENTIALLY_HARMFUL_APPLICATION`. Clients must tolerate values they do not know.
- **Attributes:** `CANARY` means "do not enforce"; `FRAME_ONLY` means "enforce only on frames". A top-level redirect is not a frame.
- **Cache rule:** a result is valid for `cacheDuration` from receipt. A clean result may be extended, but never beyond 24 hours.

## Scope

### In scope

- `apps/api/internal/scanning`: canonicalization, expression generation, the `hashes.search` client, and a provider-neutral `Checker` interface.
- A migration adding the columns below.
- Scan triggers in `createLink` and `updateLink`, and the PATCH rules for flagged links.
- `POST /internal/scan` and `.github/workflows/scan.yml`.
- The flagged branch of the redirect and verify paths, a rewritten block page, and a new neutral "temporarily unavailable" page.
- The nested verdict on `GET /v1/links/{link_id}`, a `state` enum in the schema, and two audit actions with a system actor.
- The web badge, the detail banner, and the audit actor label.
- Documentation and the legal note.

### Out of scope, and why

- **Following the destination's redirect chain.** Google calls checking the redirected URL appropriate, but doing it means fetching arbitrary hosts with SSRF protection, outside this change. This is a known gap: a short link pointing at another shortener that forwards to phishing is not caught.
- **Web Risk.** The `Checker` interface keeps it a second adapter. Nothing else is built for it.
- **E-mail to teams, and a maintainer override.** Rejected in conversation.
- **Scanning disabled or expired links.** A link that does not redirect cannot harm anyone. It becomes due again when it is re-enabled.
- **A Redis counter for Google's quota.** The sweep's batch size and the existing per-user link-create limit bound the call volume, and Redis commands are this project's scarcest free-tier resource.

## Global constraints

- **Golden rule 2:** an `active` link's redirect calls nothing new. The only new waiting is on a `flagged` link, at most 2 seconds, at most about once per 30 minutes per link per instance.
- **No URL leaves the server:** only 4-byte SHA-256 prefixes are sent to Google.
- **The API key is never part of a URL.** It travels in the `X-Goog-Api-Key` header, because Go's `*url.Error` puts the request URL into error text, which reaches logs and Sentry.
- **Blocking needs a fresh confirmation:** a flagged link is shown the block page only if Google confirmed the threat within the last 30 minutes, or within `cacheDuration` if that is shorter.
- **The tenancy exception is narrow:** every query filters by `team_id` and checks the caller's role, except the instance-wide sweep reads and the scanner's verdict writes. These act for the instance, like the retention job's delete, and carry the same justification comment.
- **Strings:** every user-facing string exists in English and German. No hardcoded copy, including on the framework-free redirect pages.
- **Accessibility:** WCAG 2.1 AA.

## Data model

One migration, authored with `supabase migration new`.

- **`link`:**
  - `scan_checked_at timestamptz null`: the last time a check of this link completed, whatever it found.
  - `scan_destination text null`: the destination that check judged. A verdict never applies to a URL it did not see, so a link whose `scan_destination` differs from `destination_url` is due.
- **`link_scan_result`:**
  - `destination_url text not null` and `threat_types text[] not null default '{}'` are added.
  - A row is written only when a link's verdict changes, from active to flagged or back. The first check that finds a link clean writes nothing.
  - `verdict` keeps its existing check constraint. `error` stays allowed and unused, since errors are logged rather than stored.
  - The table is empty in production, so the new `not null` columns need no backfill.
- **A partial index for the sweep's due query** over `link (scan_checked_at nulls first) where state in ('active','flagged')`, or whatever shape the plan's `EXPLAIN` shows the query uses.
- `link_scan_result` stays outside the retention job, as `docs/superpowers/specs/2026-09-12-analytics-retention-design.md` decided. Writing on change only keeps it small.

## The scanning package

`apps/api/internal/scanning`, the package `docs/planning/04-backend-architecture.md` reserved.

- **Interface:**
  - `Checker` has one method, `Check(ctx, urls []string) (map[string]Result, error)`.
  - `Result` carries the threat types found (empty means clean) and `ValidFor`, the duration the verdict may be relied on.
  - Provider-neutral, so Web Risk would be a second implementation. It is injected on `api.Deps` like `domainVerifier`; `nil` means scanning is off.
- **Canonicalization** follows Google's URL canonicalization rules for Safe Browsing:
  - remove the fragment;
  - repeatedly percent-unescape until stable;
  - lowercase the host, strip leading and trailing dots, collapse consecutive dots, normalize IPv4 forms;
  - resolve `/./` and `/../` and collapse `//` in the path;
  - percent-escape the result.

  Google's published canonicalization examples become a table test, verbatim.

- **Expressions:** up to 5 host suffixes (the exact host, then up to 4 suffixes from the last five components, never the bare TLD; an IP host only exactly) times up to 6 paths (exact path with query, exact path without query, then up to 4 prefixes from the root). At most 30 per URL.
- **Lookup:**
  1. SHA-256 each expression and keep the first 4 bytes as a prefix.
  2. De-duplicate the prefixes across all URLs of one call and send them in one `hashes.search` request, splitting at 1000.
  3. A URL is reported for a threat type only when one of its own full hashes equals a returned `fullHash`, and only for details whose attributes include neither `CANARY` nor `FRAME_ONLY`.
  4. Unknown threat types are kept as reported strings, not dropped.
- **Validity:** `ValidFor` is the response's `cacheDuration`.
- **HTTP:**
  - 5-second client timeout, no redirects followed, response body capped (1 MiB), the key in `X-Goog-Api-Key`.
  - A 429 or a quota error is returned as a distinct `ErrQuotaExceeded`, so `main.go` can coalesce it in Sentry like `cache.QuotaExceeded`.
  - Any other non-200 is an ordinary error carrying the status, never the body.
- **Configuration:** `SAFE_BROWSING_API_KEY`. When it is unset, `main.go` logs a Warn at startup and leaves the checker `nil`.

## The scan pipeline

### Due links

A link is due when all of these hold:

- its `state` is `active` or `flagged`;
- it is not past `expires_at`;
- and one of these is true:
  - `scan_checked_at` is null;
  - `scan_destination` differs from `destination_url`;
  - `scan_checked_at` is older than 24 hours;
  - `state` is `flagged`.

A flagged link is due on every sweep, so a false positive Google corrects is lifted even if nobody visits the link.

The sweep orders due links: never checked first, then flagged, then oldest `scan_checked_at`.

### Applying a verdict

`applyVerdict` runs in one transaction per link:

1. Read the link `FOR UPDATE`.
2. If its `destination_url` is no longer the URL that was checked, discard the verdict. A newer destination is waiting for its own check.
3. Otherwise set `scan_checked_at = now()` and `scan_destination = <checked url>`.
4. Then:
   - **Threats found, `state = 'active'`:**
     - set `state = 'flagged'`;
     - insert a `link_scan_result` row (`flagged`, threat types, URL);
     - write the audit entry `link.flagged` with a nil actor;
     - after commit: set the Redis confirmation key, invalidate the redirect cache, log `link flagged by Safe Browsing` at Error with the link and team ids.
   - **No threats, `state = 'flagged'`:**
     - set `state = 'active'`;
     - insert a `clean` row;
     - write the audit entry `link.unflagged`;
     - after commit: delete the confirmation key, invalidate the redirect cache, log at Info.
   - **Threats found, `state = 'flagged'`:** refresh the confirmation key only.
   - **Otherwise:** the timestamps are all that change.
5. A check that errors changes nothing. The link stays due and is retried by the next sweep. Errors log at Warn, except `ErrQuotaExceeded`, which logs at Error and is coalesced.

### Immediate checks

After `createLink` commits, and after `updateLink` commits a changed `destination_url`, the handler starts a goroutine to check that one link. The goroutine:

- uses `context.WithoutCancel` on the request context, with a 10-second timeout;
- recovers panics, as `HandleDeepHealth`'s pings do;
- is best-effort by design. Vercel does not promise it runs to completion, and the sweep exists for the case where it does not.

### PATCH rules for a flagged link

- **A changed `destination_url`** sets `state` back to `active` in the same update, because the flag belonged to the old URL. The new destination is checked immediately. `link.updated`'s `metadata.changed` records both fields.
- **Any other change to `state`** on a flagged link (to `active` or to `disabled`) is refused with 409 and `huma.ErrorDetail{Location: "body.state", Value: "flagged"}`. Disabling is refused too, because disable-then-enable would otherwise be a way around the block.
- **No more lost flags:** `updateLink` reads the link `FOR UPDATE`. Today it reads without a lock and writes `state` back from that read, so a flag the scanner set in between is overwritten.

### The sweep

`POST /internal/scan` follows `/internal/retention`:

- **Placement:** on the root router above the hostname split, outside Huma and the OpenAPI document.
- **Auth:** guarded by its own `X-Scan-Token` against `SCAN_TOKEN`, compared in constant time. An unset token answers 404 to everyone, which is fail-closed.
- **No scanner:** when `SAFE_BROWSING_API_KEY` is unset, it answers 503, so the workflow's heartbeat goes missing instead of reporting a scan that never ran.
- **Run:** it takes up to a fixed batch of due links (a constant sized so one run fits a 25-second budget; the plan sets it) and checks them in as few `hashes.search` calls as the prefix limit allows. It applies each verdict and stops at the budget.
- **Response:** `checked`, `flagged`, `unflagged`, `failed` and `remaining` (due links left after this run).

`.github/workflows/scan.yml` mirrors `retention.yml`:

- **Schedule:** `cron: "7,37 * * * *"`, plus `workflow_dispatch`.
- **Pull requests:** a pull-request trigger scoped to its own path, with the steps skipped on pull requests.
- **Steps:** one `curl --fail` with the token, then a ping to `SCAN_HEARTBEAT_URL` only on success.

## The redirect path

`unavailable()` in `apps/api/internal/api/redirect.go` keeps its order: expiry, then state, then (in the caller) the password. Only the `flagged` case changes, for both `GET /{slug}` and `POST /{slug}/verify`:

1. **Confirmation key exists:** `sb:confirmed:<link_id>` (through `Client.Key`, so it carries the environment prefix) holds the confirmed threat types. If it exists, answer the block page, 403.
2. **No key:** call the checker for this one destination with a 2-second timeout. Concurrent requests for the same link on one instance share one call through `singleflight`. Then:
   - **Threats confirmed:** set the key with a TTL of `min(ValidFor, 30 minutes) − 1 minute`, then answer the block page. If that TTL is not positive, no key is set, and the next request re-checks.
   - **Clean:** forward exactly as an active link would, including click recording. Start the same best-effort goroutine `applyVerdict` uses to lift the flag. If it is lost, the sweep lifts it.
   - **Error, timeout or no checker configured:** answer the new neutral page (`KindUnavailable`, 503, `Retry-After: 300`) and log at Warn. The page claims nothing about the destination, because without a fresh confirmation the terms forbid saying it is unsafe.

The cost:

- An active link costs nothing.
- A flagged link costs one Redis `GET` per redirect, plus one `SET` per re-check.
- Waiting is at most 2 seconds, roughly once per 30 minutes per link per instance.

This is the one exception to golden rule 2, and `CLAUDE.md` states it with its reason: a flagged link may not be blocked on stale data, and it may not be forwarded unchecked either.

**The block page** (`KindFlagged`, `internal/pages`, English and German, still without JavaScript) is rewritten:

- a qualified heading and body per threat type. For example, "Suspected phishing site" / "Mutmaßliche Phishing-Seite" for `SOCIAL_ENGINEERING`, and "possibly harmful software" / "möglicherweise schädliche Software" for `MALWARE` and `UNWANTED_SOFTWARE`. An unknown type gets a generic qualified text;
- a link to Google's definition of each threat;
- "Advisory provided by Google", linked to Google's advisory page;
- the sentence that Google works to provide accurate information but cannot guarantee it is complete and error-free.

The current copy ("was flagged as unsafe") is too absolute and is replaced. The page's template takes the threat types from the confirmation key's value.

## The API, the audit log and the web app

### API

- **Nested verdict:** `GET /v1/links/{link_id}` gains `scan`, an object omitted while the link has never been checked. Per the Huma nullability rule, that means an `omitempty` struct pointer, checked in the generated schema. Its fields:
  - `verdict`: `clean` or `flagged`;
  - `threat_types`;
  - `since`: when the latest `link_scan_result` row was written, absent if there is none;
  - `checked_at`: from `link.scan_checked_at`.
- **List:** the list endpoint keeps only `state`.
- **`state` enum:** `state` on the `Link` response gains `enum:"active,disabled,expired,flagged"`, so the generated TypeScript type stops being a bare `string`.
- **PATCH:** the 409 described above.

### Audit

- **Actions:** `link.flagged` and `link.unflagged` join the closed taxonomy, making 23 values. Each goes into both the constant block and `knownActions`, and the link group's taxonomy test covers them.
- **System actor:** `audit.Entry.ActorUserID` becomes `*uuid.UUID`; nil is the system. Every existing caller passes its user's id, and `audit_log.actor_user_id` is already nullable.
- **Metadata:** `threat_types` and `destination_url`. Both pass the recursive metadata check.

### Web

- **Actor label:** an audit entry with a null actor reads "Google Safe Browsing" when its action is `link.flagged` or `link.unflagged`, and "A deleted account" for every other action, as today. Both cases are `actor_user_id = null` in the database, so the action is what tells them apart.
- **Link list:** a destructive-variant badge, "Blocked" / "Gesperrt", on a flagged link, beside the existing password badge.
- **Link detail page:** a persistent notice below the heading, a status rather than an alarm. It has:
  - the qualified text per threat type;
  - Google's definitions and "Advisory provided by Google";
  - the accuracy disclaimer;
  - a link to Google's form for reporting an incorrect warning;
  - the next steps: "We re-check every 30 minutes. Change the destination, or wait until Google clears the site."
- **No new state control:** the form gains none, and the update body still never sends `state`.

## Operations

### Environment variables

| Variable | Where | Unset means |
| --- | --- | --- |
| `SAFE_BROWSING_API_KEY` | Vercel, `kurze-url-api`, Production and Preview | Scanning off, with a startup Warn; `/internal/scan` answers 503; a flagged link answers the neutral 503 page |
| `SCAN_TOKEN` | Vercel Production, and the GitHub secret of the same name | `/internal/scan` answers 404 to everyone |
| `SCAN_HEARTBEAT_URL` | GitHub secret | The workflow fails its last step |

All three are documented valueless in `apps/api/.env.example`, with their reasoning, and in `CLAUDE.md`.

### Sentry

- Each new flag logs at Error, which is how the maintainer hears of it.
- `scanning.ErrQuotaExceeded` gets an `observability.CoalesceRule`: one event per hour per instance.
- Every other check failure logs at Warn and never reaches Sentry.

### Manual steps for the maintainer

None of these can be done from the repository:

1. A Google Cloud project with the Safe Browsing API enabled, and an API key restricted to that API.
2. The three variables above.
3. A Better Stack heartbeat "Safe Browsing scan" with a 30-minute period and at least 30 minutes of grace. GitHub starts scheduled runs late.

### Rollout

- **Migration:** it reaches production through the GitHub integration on merge. The Preview database needs it applied by hand before the branch's e2e can pass, per the constraint in `CLAUDE.md`.
- **Backlog:** on the first sweeps every existing link is due. The batch limit spreads that backlog over a few runs.
- **Live smoke test after deploy:**
  1. Create a link to Google's test URL `https://testsafebrowsing.appspot.com/s/phishing.html`.
  2. Within seconds it must be flagged, show the block page and carry an audit entry.
  3. Then delete it.

## Testing

- **`internal/scanning`, unit:**
  - canonicalization against Google's published examples;
  - expression generation (host-suffix and path-prefix counts, the IP-host case, the 30 cap);
  - full-hash matching against an `httptest` server, including a prefix hit whose full hash differs (not a match), `CANARY` and `FRAME_ONLY` (not enforced) and an unknown threat type (kept);
  - error classification: 429 to `ErrQuotaExceeded`, 5xx, timeout, oversized body;
  - the key never appears in a request URL.
- **`internal/api`, database-backed with a fake `Checker`:**
  - due selection and ordering;
  - every `applyVerdict` transition, including the stale-destination discard and the cache invalidation;
  - the immediate check after create and after a destination change;
  - PATCH: 409 for `state` on a flagged link, and a destination change unflagging it;
  - the sweep: 404 without a token, 503 without a checker, counts and `remaining`, the batch limit;
  - audit entries with a nil actor;
  - the nested `scan` field present or omitted;
  - the generated schema for `scan` and the `state` enum.
- **Redirect and verify:**
  - with a confirmation key: 403 with the attribution;
  - without one, and the fake confirms: 403, key set;
  - fake clean: 302 and the flag lifted;
  - fake errors: 503 neutral page;
  - an active link never calls the checker (the fake asserts it was not called).
- **Web:** badge, notice and audit actor label, with Storybook stories and a11y checks.

## Documentation this changes

`CLAUDE.md`:

- golden rule 2's exception;
- a new non-obvious-constraint entry covering the 30-minute rule, `hashes.search`, the flagged-state rules and the variables;
- the `scanning` package moves from "arrives later" to existing.

The planning documents named under **Amends**, and `apps/api/.env.example`.

## Risks accepted

- **The terms' reading.** "Received updated information within the past thirty minutes" is met per link, by a confirmation no older than 30 minutes. That is the plain reading. If Google read it otherwise, Web Risk under Cloud terms is the documented fallback.
- **The sweep's timing.** GitHub may start runs late, so a link created while the immediate check is lost can stay unchecked for longer than 30 minutes.
- **The redirect-chain gap**, as above.
- **Commercial use.** The 2026-09-01 decision stands. Any revenue tied to the service moves this to Web Risk.
