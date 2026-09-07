# Backups: Design

**Status:** approved 2026-09-07 **Amends:** `CLAUDE.md` (the "Backups" open item), `docs/planning/02-external-services-and-hosting.md` (the free-tier backup note), `docs/planning/08-legal-and-compliance.md` (adds a processor and a retention period)

## Goal

The database can be brought back after Supabase loses it, after the account is lost, or after the maintainer destroys it by hand — without any of the recovery depending on Supabase.

The Supabase free tier provides no backups at all. Not short retention: none. That has been an open item since the project's first planning pass, and it is the last operational gap in the stack that has a total-loss failure mode.

## Success criterion

One sentence, and everything in this document exists to make it true:

> Given only the encrypted backup and the private key, a fresh Supabase project can be brought to a state where the existing production frontend, pointed at it, lets a previously registered maintainer log in and see their teams and links.

Stated this way it is falsifiable, which "we have backups" is not. The restore drill in this design is the test, and it is part of the work rather than a good intention that follows it.

## Scope

**In:** the production database — `public` (all ten application tables), the data in `auth` (the Supabase users), and the cluster roles.

**Out, deliberately:**

- **Redis.** Cache, rate-limit counters and visitor dedup are reconstructible or intentionally ephemeral. Golden rule 5 requires the dedup hash to expire after 25 hours; backing Redis up would preserve personal-data hashes the architecture goes out of its way to forget.
- **The preview database.** It is built from migrations plus test data and may be lost at any time.
- **Vercel project configuration, Sentry and Better Stack setup.** Recreated by hand; the restore procedure lists what has to change.
- **The age private key.** It is the one thing no backup can contain, and losing it makes every backup worthless.

## What a restore actually costs, beyond the data

A fresh Supabase project has a new project ref and new keys. Password hashes travel with the dump; sessions do not. So a restore is the dump **plus six environment variables across both Vercel projects**:

| Project | Variables |
| --- | --- |
| `kurze-url-api` | `DATABASE_URL`, `SUPABASE_JWKS_URL`, `SUPABASE_JWT_ISSUER`, `SUPABASE_SERVICE_ROLE_KEY` |
| `kurze-url-web` | `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY` |

The web pair is the one that hides: `apps/web/src/server/supabase.ts` reads them at runtime and throws without them, and sign-in goes through Supabase directly rather than through the API, so a restore that repoints only the API leaves a working database nobody can reach a login form for.

Two pieces of project configuration are not in Postgres either and have to be redone on the new project: **Resend as custom SMTP** — without it Supabase's built-in sender caps at two mails an hour and invitations quietly stop — and the **auth redirect and site URLs**.

This is exactly the failure the success criterion is worded to catch: "can log in", not "the data is there".

## Producing the dump

`supabase db dump --dry-run` prints the `pg_dump` script it would run, and reading it settled two things that would otherwise have been guesses.

**The schema dump excludes `auth`**, listing it among the "internal schemas as they are maintained by platform". **The data dump does not** — its exclusion list omits `auth`, and everything else runs under `--schema "*"`.

That split is exactly right and needs no coaxing: a fresh project brings its own correctly versioned `auth` schema, which must not be overwritten, while the user rows inside it are precisely what has to come back. Three invocations:

| Command | Output | Contents |
| --- | --- | --- |
| `supabase db dump --role-only` | `roles.sql` | cluster roles |
| `supabase db dump` | `schema.sql` | `public`, without `auth` |
| `supabase db dump --data-only --use-copy -s auth,public` | `data.sql` | `public` and `auth` data |

Restored in that order. This is the path Supabase's own restore documentation describes, not an invention of ours.

**Corrected 2026-09-07 by the local drill: the data dump needs `-s auth,public`.** Left to its default the CLI dumps every schema, which pulls in the platform-owned `storage` tables. They are empty here — this project stores no files — but a `COPY` into a table owned by `supabase_storage_admin` still needs write permission the restoring role does not have, and because a restore runs in a single transaction, `permission denied for table buckets_vectors` rolled back every user, team and link with it. The drill also ruled out the narrower fix of excluding those tables by name: the local stack already carried `iceberg_namespaces` and `iceberg_tables` that production did not, so such a list would go stale the moment Supabase adds a table, and the discovery would come during a restore. An allowlist of the two schemas we own cannot rot that way.

**`roles.sql` is the one file whose failure is not fatal**, also settled by that drill. It contains no `CREATE ROLE`: every role it names is one Supabase provisions itself, so the file only tunes settings a fresh project already carries. Its `GRANT SET ON PARAMETER "log_min_messages"` needs rights the restoring role lacks, and stopping a restore over a grant the platform has already made would be the wrong trade. Whether a hosted restore hits the same error is for the hosted drill to settle.

**`supabase_migrations` is excluded from the data dump.** A restored project therefore has a complete schema and an empty migration history, and the Supabase GitHub integration would try to apply every migration again on the next merge to `main`. The procedure ends with `supabase migration repair`; without it, the first merge after a restore damages what was just recovered.

**Connectivity is the risk that can end this design.** Supabase's direct connection is IPv6 and GitHub Actions runners are IPv4-only. The transaction pooler on 6543, which the API uses, is not suitable for `pg_dump`. The session pooler on 5432 should work. This is settled first, before anything else is built: if three non-empty files cannot be produced from a GitHub Actions runner, this approach is dead and the alternative is Supabase Pro.

**A size floor guards against the quiet failure.** All three files are checked against a minimum size before anything is uploaded. A 0-byte dump uploaded reliably every night is the canonical form of "we had backups".

## Storage

A private GitHub repository, one release per run, tagged `backup-YYYY-MM-DD`, carrying one asset: `kurze-url-YYYY-MM-DD.tar.gz.age`. Release assets rather than commits, because a git history of daily binary blobs only grows and cannot be pruned without rewriting it.

**The workflow lives in that private repository, not in `kurze-url`.** Two problems disappear at once: `GITHUB_TOKEN` can create releases in its own repository, so no personal access token is needed, and the production database URL never sits in a public repository's secrets. The cost is that the mechanism is not part of the open-source project and does not run through its CI. The restore procedure is published in the public repository to compensate — see below.

**Encryption uses `age` against a public recipient key.** The workflow secret holds only the public half, so a run can encrypt and upload but cannot decrypt anything, including what it wrote yesterday. A compromised Actions run gains no access to history — only the ability to append. The private key lives with the maintainer in two copies in separate places.

**Retention is 14 daily plus 12 weekly, derived from the tag date alone** rather than from bookkeeping that can drift: anything younger than 14 days is kept; anything older is kept only if it is the first backup of its ISO week and that week is no more than 12 weeks back; everything else is deleted. Stateless, recomputable at any time, and a missed run leaves no broken chain.

Three months of retention is also a data-protection parameter, not only an operational one: after a Verein deletes their team, their data survives in backups for that long. It is a defensible period, and it belongs in the Datenschutzerklärung.

## Noticing when it stops

GitHub disables scheduled workflows automatically after 60 days without repository activity. A dedicated backup repository has no human activity by construction — nobody commits, nobody opens issues. Whether a release created through the API counts as activity is not reliably documented, and the project's only backup should not rest on that.

So the design monitors **the absence of a backup rather than the failure of a job**. The workflow pings a Better Stack heartbeat after a successful upload; no ping for more than 26 hours raises an alert. One mechanism covers both the run that failed and the run that never started — and a job asked to report its own failure cannot do so when it does not run at all. Better Stack is already in the stack from the observability work, with three of ten monitors used.

## Restoring

**The procedure lives in the public repository**, at `docs/restore.md`. That sounds backwards until you notice that a recovery procedure stored only inside the system you are recovering from is unreachable exactly when it is needed. It contains nothing secret: the secrets are the database URL and the age key, and both live elsewhere.

Eight steps: fetch the newest asset → `age -d` with the private key → unpack → create a fresh Supabase project → apply `roles.sql`, `schema.sql`, `data.sql` in that order → `supabase migration repair` → reconfigure the new project (Resend as custom SMTP, auth redirect and site URLs) → repoint the six environment variables across both Vercel projects.

### The drill

Two levels, because they prove different things.

A **local** run against `supabase start` shows that the three files apply cleanly and the user rows arrive. Cheap, repeatable, and a candidate for CI later.

A **hosted** run against a throwaway Supabase project shows what the success criterion actually demands: that someone can log in afterwards. Only here do the new JWT keys and the environment rewiring show up. One obstacle to expect: the free tier allows two active projects per organisation, and production plus preview occupy both, so preview has to be paused for the duration.

The drill also produces the only honest answer to "how long does a restore take" — the measured one. Repeat it annually, and whenever Supabase changes anything about auth or the dump format.

## Configuration

| Secret | Where | Notes |
| --- | --- | --- |
| `DATABASE_URL` | private backup repo | Production, through the **session** pooler on 5432, not the transaction pooler |
| `AGE_PUBLIC_KEY` | private backup repo | Public half only; the workflow cannot decrypt |
| `BACKUP_HEARTBEAT_URL` | private backup repo | Better Stack heartbeat, pinged after a successful upload |

## What the maintainer does by hand

Before any code runs: create the private repository, generate the age keypair and store the private half twice in separate places, set the three secrets above, and create the Better Stack heartbeat monitor with a 26-hour grace period.

This is task 0 of the implementation plan, written out step by step. Manual steps buried in prose get skipped — the custom-SMTP episode is the precedent, and the resulting failure looked like a code defect.

## Open questions

- **Where the age private key lives** is the maintainer's decision and is not made here. The plan requires that it be made and written down, because an unrecorded location is the same as no key.
- **Whether the session pooler supports a full `public` + `auth` dump** is unverified and is the first task in the plan. A negative answer replaces this design with Supabase Pro.
- **Whether GitHub's release-asset storage on private repositories has a practical ceiling** for this use is untested. The dumps will be kilobytes to low megabytes for years, so this is a note to revisit if that ever stops being true, not a present constraint.
- **The Datenschutzerklärung and AVV** need to name GitHub as a processor and state the three-month backup retention. Doc 08 already routes legal text to a lawyer; this adds one item to that list.
