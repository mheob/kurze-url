# The Audit Log Page — Design

**Status:** approved 2026-09-19 **Amends:** nothing. `GET /v1/teams/{team_id}/audit-log` and the whole write path already exist; this builds the first thing that reads them with a human in front of it.

The seventeenth implementation spec.

## The problem

Every mutating endpoint has been writing an audit entry since plan 3 — twenty-one actions across teams, members, links, passwords, folders, tags and domains, each one written inside the mutation's own transaction so the log cannot disagree with the data. `GET /v1/teams/{team_id}/audit-log` serves them, paginated and filterable.

Nothing reads it. A Verein's board can be told what the log records and cannot look at it, which makes the guarantee academic: an audit log nobody can open is a promise rather than a control.

`CLAUDE.md` listed the action taxonomy as an open question until 2026-09-19, which is part of why this page never got built — the vocabulary looked undecided when it had in fact been settled in `internal/audit` and was merely undocumented.

## Goal

A Verein's admin or owner can open their team's history, see who did what and when in their own language, narrow it to the part they care about, and share the URL of what they are looking at.

## Scope

### In scope

- One new route, `/teams/$teamSlug/audit-log`, with its filters in the search parameters.
- A table of entries with a per-row expander for the entry's metadata.
- Resolving an actor's id to something a reader recognises.
- Three filters: entity type, actor, time range.
- A time-range control of its own.
- The sidebar entry, shown only to the roles that may use it.
- Translation keys for twenty-one actions, the columns, the filters, and every empty or refused state.

### Not in scope

- **Any change to the API.** The endpoint already offers more than this page uses, and the gap is deliberate — see the `action` filter below.
- **The `action` filter's user interface.** It stays in the endpoint for the API and the future CLI. A select of twenty-one values answers, more precisely, the question the entity-type filter already answers legibly.
- **Generalising `StatRangePicker`.** Its presets and its hard floor at ninety days exist because retention deletes click rollups. `audit_log` is not in that job, so both would be wrong here, and changing a component the statistics page depends on to serve a second page with different semantics buys less than it risks.
- **Exporting the log.** A CSV download is an obvious next ask and needs its own thinking about what a Verein may hand to whom.
- **Retention for `audit_log`.** The table grows without bound today. That is a real open item, but it is a policy question, not a page.

## Access

Reading the log requires admin. That is the endpoint's own decision — `authz.AdminScope`, documented there as "administrative history, not a viewer-level right like the team or member reads" — and this page inherits it rather than restating it.

Two gates, and both are needed.

**The sidebar entry renders only for `admin` and `owner`.** The role is already in the membership list `GET /v1/me` returns and `_authed.tsx` reads, so this costs no request. `app-sidebar.tsx` already gates its team-creation entry on `isMaintainer`, so this follows a pattern rather than inventing one.

**The route renders the refusal.** A hidden menu entry is not a permission: the URL can be typed, bookmarked, or shared by an admin with a member who is not one. The API distinguishes two cases and so does the page — a member whose role is below admin gets **403**, and someone who is not a member of the team at all gets **404**. The second already has a page; the first needs a state saying plainly that this part of the team is for admins, without implying the team does not exist.

## What an entry looks like

A table, newest first, four columns: **when · who · what · which entity**. Each row expands to show that entry's metadata.

The order is the query's own, `created_at desc, id desc`, and the tie-break is not decoration: several entries can share a timestamp because they were written in one transaction, and without a second key their relative order could differ between two requests for the same page — which is how a row appears twice while another is never seen at all.

A table rather than a sentence per entry. Sentences read better for one entry and worse for forty: a reader scanning for "when did the domain change" finds a column faster than a paragraph, and the three filters map onto column headings, which makes the page explain its own controls. The cost of the table is that "what" becomes a label rather than a clause — `team_member.role_changed` reads as "Role changed", and who it was changed for lives in the expander instead of the sentence. That is the trade accepted here.

### Who

`AuditEntry.actor_user_id` is a UUID and nothing else, which is useless to a reader on its own. `GET /v1/teams/{team_id}/members` returns `user_id` and `email` and is viewer-level, so an admin may read it: the page fetches the member list once and maps ids to addresses. **No API change.**

Three states, all of which occur:

| `actor_user_id` | What the page shows |
| --- | --- |
| present, found in the member list | the member's email address, which this page does not newly expose — `GET /v1/teams/{team_id}/members` already shows it to every member of the team |
| present, not in the member list | a former member — removing someone from a team deletes the membership, never their entries |
| `null` | a deleted account — `audit_log.actor_user_id` is `on delete set null` |

The second and third are distinct on purpose. "A former member" is a fact about this team; "a deleted account" is a fact about the person, and conflating them would tell a board that someone left when they did not.

### What the expander shows

The metadata is not an open sack. Eleven keys occur across every writer: `destination_url`, `email`, `from`, `hostname`, `links_unfiled`, `name`, `redirect_type`, `role`, `slug`, `to`, and `changed`, which carries the list of fields a `PATCH` moved.

The expander renders them as a key-value list, generically — no per-action renderer. Keys render as they are, the way `stat-breakdown-card.tsx` already prints a dimension's values: they are protocol vocabulary rather than prose, and the same reasoning that allowlists `TXT` and `CNAME` in `e2e/i18n.spec.ts` covers them. Those entries join that allowlist in the same change, because the crawl will otherwise report them as untranslated, which is what it is supposed to do.

A generic renderer is what makes twenty-one actions affordable. Writing one renderer per action is where this page would have become a project.

## Filters

Entity type, actor, and time range, all three held in the route's search parameters the way the statistics page holds its window — so a state can be linked, bookmarked and reached by the back button, and so a reload does not silently widen what someone is looking at.

The time-range control is new and deliberately plain: two dates, no presets, no floor. `StatRangePicker` looks like the obvious reuse and is not, for the reason under "Not in scope".

**A date is not an instant, and the conversion is where this goes wrong.** The control offers days; the endpoint takes RFC 3339 instants against a `timestamptz` column, with `created_at <= to`. A day handed over as midnight therefore excludes almost all of the day the reader picked — choose "to: today" and today's entries are missing, which reads as a broken log rather than an off-by-one. `from` becomes that day's start and `to` becomes the **end** of its day, and both are interpreted in UTC, the same convention the statistics window already uses because the rollup buckets in UTC. A test pins the `to` boundary specifically: an entry written during the chosen last day must be in the result.

Paging reuses the `Page[T]` envelope and the pagination the link list already renders.

## Copy

Twenty-one action labels in both catalogues, plus column headings, filter labels, the two refusal states, and an empty state.

This is the bulk of the work and it carries one specific hazard: `catalogues.test.ts` requires every key to differ between English and German, and twenty-one short labels drawn from the same small vocabulary of verbs will produce at least one pair that does not. Each collision is then a decision — a better German word, or an `identicalByDesign` entry with a reason — and it has to be made deliberately rather than by reaching for the allowlist to make a test go green.

## Testing

**Unit.** Actor resolution in all three states, including that a member absent from the list is reported as a former member rather than falling back to the raw id. The expander rendering metadata for an action nobody wrote a renderer for, since that is the property that makes the generic approach sound. The three filters round-tripping through the search parameters.

**Accessibility.** An axe run over the composed page inside the real shell, the way the statistics page's own suite does it, and one over the admin-refusal state — an error state is where heading structure is most often forgotten.

**End to end.** Sign in as an admin, create a link, and find the resulting entry: `createLink` already produces real audit rows, so this needs no seeding fixture. A second case asserts the sidebar entry is absent for a role below admin, which is the half a unit test cannot see. The i18n crawl gains the page, with the metadata keys in its exclusion set.

## Consequences

`/teams/$teamSlug/audit-log` is a child of the **dynamic** segment, so `reservedTeamSlugs` in `apps/api/internal/api/teams.go` is not affected. `CLAUDE.md`'s warning concerns static children of `/teams/` itself, which would shadow a team whose slug matched; stating this here so nobody adds a reservation that is not needed.

`audit_log` has no retention policy and this page will make that visible — a team active for a year will page through a lot of history. Naming it as an open item rather than solving it here.
