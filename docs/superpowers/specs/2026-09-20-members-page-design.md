# Members Page — Design

Written 2026-09-20. Implements `/teams/$teamSlug/members`, the screen where a Verein's board sees who has access and changes it.

The API behind it arrived with the tenancy and authorization plan (`docs/superpowers/plans/2026-09-02-tenancy-authz-audit.md`) and is complete: `GET|POST /v1/teams/{team_id}/members` and `PATCH|DELETE /v1/teams/{team_id}/members/{user_id}`, with twenty-five Go tests covering the role hierarchy, the last-owner lock and both rate-limit axes. This design adds the frontend, plus two small additions to that API where it cannot currently tell the frontend enough to be honest.

## Scope

**In:** list the team's members; invite or add someone by email address; change a member's role; remove a member. Plus the two API additions in the next section.

**Out, deliberately:**

- **Self-removal ("leave team").** `AdminScope` guards `DELETE .../members/{user_id}`, so a viewer or editor cannot remove themselves at all — exactly the people most likely to want to. Building it for admins only would ship the feature to the wrong half of the membership. It needs its own API decision (a self-scoped route, or relaxing the scope for the self case) and belongs in its own round.
- **Pagination.** See "Data source" below.
- **A notification for the person added directly.** `addMember`'s own comment names this as a known gap: an address that already has an account is added with no email at all and finds out on next login. There is no notification system for it to use. This design makes the gap _visible to the person doing the adding_ (see `invited` below) rather than closing it.

## The API additions

Both live in `apps/api/internal/api/members.go`. Neither touches the schema, so no migration — which also means the branch's e2e is not subject to the preview-database trap in `CLAUDE.md`. Both require regenerating `openapi.json` and `packages/api-client`.

### 1. `invited` on the 201

`POST /v1/teams/{team_id}/members` does one of two very different things, and its response cannot say which. An address with no account is **invited** — Supabase sends a magic-link email. An address that already has one is **added directly**, with no email and no notification of any kind.

`resolveInvitee` already returns that distinction; `addMember` discards it. The response type becomes:

```go
// AddedMember is the membership plus whether creating it sent an email. The
// two paths through addMember differ in a way the person doing the adding
// has to know about: an address that already had an account is added with no
// notification at all, and only this flag distinguishes that from an
// invitation that is on its way.
type AddedMember struct {
	Member
	Invited bool `json:"invited" doc:"True when an invitation email was sent; false when the address already had an account and was added directly, without any notification."`
}
```

Not a field on `Member`: that type is also what `listMembers` returns, where "was an email sent" has no meaning. `Invited` is a scalar and always present, so Huma's object-nullability trap documented in `CLAUDE.md` does not apply — the generated TypeScript is `invited: boolean`.

### 2. The two 429s become distinguishable

`allowInvite` refuses on two axes with two different remedies:

| Axis                                 | Meaning                    | What the person should do |
| ------------------------------------ | -------------------------- | ------------------------- |
| `RATE_LIMIT_INVITE_PER_HOUR`         | this team's burst guard    | wait and retry            |
| `RATE_LIMIT_INVITE_GLOBAL_PER_MONTH` | the instance's mail budget | contact the maintainer    |

Both are plain 429s today, so `classifyApiError` collapses them into one `rateLimited`. Telling a Verein "try again later" when the real answer is "this instance is out of invitation budget for the month" sends them back to retry for up to thirty days.

Following the `deleteDomain` precedent that `CLAUDE.md` records under "Conventions" — a typed value in `huma.ErrorDetail.Value`, keyed by the operation's own path parameter — each refusal carries a token:

```go
huma.Error429TooManyRequests("too many invitations for this team; try again later",
	&huma.ErrorDetail{Location: "path.team_id", Value: "team_hourly"})
```

and `"instance_monthly"` for the global half. Reading the free-text `detail` instead is not an option; `CLAUDE.md` explicitly reserves it as rewordable.

### What stays untyped, and why

The three 403s — granting or revoking `owner` without being one, removing an owner without being one, and the last-owner refusal — keep their plain status. The interface does not offer those paths: `owner` is absent from the role select unless the actor is an owner, an owner's row carries no controls for a non-owner, and the sole owner's row carries neither a role select nor a remove button. A 403 that arrives anyway means another admin changed something concurrently, and for a race the honest response is "the list changed, here it is again" plus a refetch. That needs no new field.

## Authorization and visibility

`listMembers` embeds `authz.ViewerScope`; the three mutations embed `authz.AdminScope`. `resolveMembership` answers a non-member with **404** and a member whose role is too low with **403**.

The page is therefore in the sidebar for **every member**, unlike the audit log. There is no forbidden state to render: anyone who can reach the route can read the list, so this page has no equivalent of `audit.forbiddenTitle` and no `statusOf(error) === 403` check in its loader.

What each role sees:

|  | list | invite form | role select | remove |
| --- | --- | --- | --- | --- |
| viewer, editor | yes | — | — | — |
| admin | yes | yes, roles `viewer`/`editor`/`admin` | on non-owner rows | on non-owner rows |
| owner | yes | yes, plus `owner` | on every row | on every row |

`currentRole` is read the way `app-sidebar.tsx` already reads it: from `memberships` in the route context, found by `currentTeamSlug`. The signed-in person's own row is marked with a badge, from `GET /v1/me`'s `user_id`.

The sole-owner rule is computed from the list itself — if exactly one row has `role === 'owner'`, that row's select and remove control are disabled. This is courtesy, not enforcement: enforcement is `refuseLastOwner`, which takes a row lock inside the mutation's transaction precisely because two concurrent demotions would otherwise both read "two owners" and both succeed.

## Data source

The page reuses `membersQueryOptions` from `apps/web/src/server/members.ts` **unchanged**. That module already exists — the audit log page added it to resolve actor ids — and its cache key is `['members', teamId]`. Sharing it is deliberate: one cache entry, and every mutation here refreshes the names the audit log renders.

**No pagination.** `listMembersFor` asks for one page of 100, the API's cap, and logs to `console.error` when `total_count` exceeds what came back. That cap was reasoned through for the audit log's benefit and holds here too: a Verein's team is bounded by its board and its helpers, not by time, so the list does not grow the way an audit log does. Adding a pager would be building for a team shape nobody has. The existing overflow log stays the signal if one ever appears.

**Empty addresses are possible.** `Member.Email` comes from `derefString(row.Email)`, and `auth.users.email` is nullable because Supabase allows phone-only accounts. A row can therefore carry `""`. The list renders `members.unknownAddress` in that case rather than an empty cell, so the row still reads as a person.

## Server functions

Three new pairs in `apps/web/src/server/members.ts`, in the shape `domains.ts` established — a `...For` under `createServerOnlyFn` taking `request` as a parameter, and a `...Fn` above it calling `getRequest()` inline, so the inner function stays callable with a synthetic request under Vitest:

- `addMemberFor` / `addMemberFn` — `{ teamId, email, role }` → `AddedMember`
- `updateMemberRoleFor` / `updateMemberRoleFn` — `{ teamId, userId, role }` → void
- `removeMemberFor` / `removeMemberFn` — `{ teamId, userId }` → void

All three are `createServerFn({ method: 'POST' })`. Server functions support only GET and POST, so the PATCH and DELETE behind them are the generated client's business, not the boundary's — `deleteDomainFn` does the same. All three pass `throwOnError: true`, without which a refused request resolves to `{ data: undefined, error }` rather than throwing.

`PATCH` and `DELETE` answer 204, so the generated client yields no body; the `...Fn` returns void and the route refetches rather than reading a response.

## Components

### `apps/web/src/components/member-invite-form.tsx`

Email field and role select, on TanStack Form, in the shape of `link-form.tsx`. Client-side validation covers only what is knowable without the server: the address is required and must look like an address. Everything else — already a member, rate limits, mail failures — is a server answer and is rendered from the mutation's failure, not guessed at.

The role select's options come from a prop, so the route decides whether `owner` is among them. The default selection is `viewer`: the least privilege is the safe default when somebody submits without looking.

### `apps/web/src/components/member-list.tsx`

A `ui/table` with four columns: address, role, member since, actions. `formatDateTime` from `lib/format.ts` renders the date — it already memoises its `Intl.DateTimeFormat` and pins `timeZone: 'UTC'`.

The role cell is a `ui/native-select` for a member the actor may change, and plain translated text otherwise. Changing the select fires the mutation immediately — no save button. A role change is reversible and written to the audit log, so a confirmation dialog would be friction without a benefit; removal, which is neither, keeps one.

Removal uses the existing `ConfirmDelete` with an explicit `confirmLabel`. Its default is "Yes, delete it", which on this screen reads as an offer to delete a person — the same reasoning that gave the component its `confirmLabel` prop when the link-password card became its third caller.

The list renders no empty state. Whoever can see this page is in it.

## Route

`apps/web/src/routes/_authed/teams.$teamSlug.members.tsx`, structured as the domains route is:

- `loader` calls `loadMembers(context.queryClient, context.teamId)`, which wraps `ensureQueryData` and converts `kind: 'unauthenticated'` into `redirect({ to: '/login' })`. Everything else is rethrown.
- `errorComponent` is `MembersError`, the same shape as `DomainsError`: `<Navigate to="/login" />` for an unauthenticated background refetch, `reportUnexpected(error)` so a genuine 500 reaches Sentry, and `t('errors.<kind>')` otherwise.
- Three `useMutation` calls, each with its own failure slot in state, each invalidating `membersQueryOptions(teamId).queryKey` on success.
- A mutation callback cannot throw a redirect, so `kind: 'unauthenticated'` inside one navigates via `router.navigate({ to: '/login' })` — the note the domains and create-link routes both carry.

Two classifiers live in this file:

```ts
type InviteFailureKind =
	| 'alreadyMember' // 409
	| 'instanceBudget' // 429 with ErrorDetail.Value === 'instance_monthly'
	| 'mailFailed' // 502
	| 'notConfigured' // 503
	| 'teamBurst' // 429 with ErrorDetail.Value === 'team_hourly'
	| 'unknown';

type MemberMutationFailureKind = 'raced' | 'unknown';
```

Both read `statusOf(error)` before `classifyApiError`, for the reason `classifyVerifyFailure` already does on the domains route: the collapse of 403 and 404 into `notFound`, and of every 429 into `rateLimited`, throws away exactly what these screens need. `'raced'` covers 403 and 404 on a role change or removal and triggers a refetch alongside its message.

None of these reach `reportUnexpected`. `CLAUDE.md` excludes what `classifyApiError` names from Sentry, on the grounds that the visitor already saw it and the monthly budget is finite.

## Failure surfaces

Every refusal the three mutations can produce, and what the page does with it.

| Operation | Condition | Status | Treatment |
| --- | --- | --- | --- |
| invite | already a member | 409 | inline under the email field |
| invite | team's hourly cap | 429 `team_hourly` | inline, "wait and try again" |
| invite | instance's monthly budget | 429 `instance_monthly` | inline, "contact the maintainer" |
| invite | invitations not configured | 503 | inline, names it as an instance misconfiguration |
| invite | mail could not be sent | 502 | inline, retry is meaningful |
| invite | unknown role, owner grant refused | 422, 403 | `unknown` — the form does not offer these |
| role change | target is no longer a member | 404 | row message + refetch |
| role change | owner rule, last owner | 403 | row message + refetch |
| removal | target is no longer a member | 404 | row message + refetch |
| removal | owner rule, last owner | 403 | row message + refetch |
| any | session expired | 401 | `router.navigate({ to: '/login' })` |

One non-failure worth handling: `updateMember` returns 204 **and writes no audit row** when the submitted role equals the current one. The select cannot normally submit an unchanged value, but the list may be stale; the page treats this as an ordinary success and refetches, which corrects the stale row.

`invited === false` is also not a failure, and is the reason the API change exists: the success message says the person was added and **will not be notified**, rather than claiming an invitation is on its way.

## i18n

A new `members.*` branch in both `en.json` and `de.json`, plus `nav.members`. English `nav` keys are user-facing copy rather than literal names — `nav.auditLog` reads "History" — so `nav.members` reads "People" / "Mitglieder".

Role names need their own keys: the wire carries `viewer|editor|admin|owner` and the page displays translated text. `members.roleAdmin` is identical in both languages and therefore needs an entry in `identicalByDesign` in `apps/web/src/i18n/catalogues.test.ts`, alongside the three the audit log added; without it the no-English-shaped-German check fails.

Rate-limit copy is written fresh rather than reusing `errors.rateLimited`, whose English text is about creating links.

## Accessibility

- `teams.$teamSlug.members.a11y.test.tsx` runs axe over the composed page, as the audit log and stats pages do.
- Each row's role select needs an accessible name that identifies its row, not just "Role" — a screen-reader user hearing four identical "Role" selects cannot tell them apart. The label names the member.
- `ConfirmDelete` supplies the dialog's accessible name from its `question`, so that question names the member too.
- The success and failure messages are `role="status"` and `role="alert"` respectively, so a change made with the keyboard is announced.

## Testing

**Go.** Four existing tests in `members_test.go` are extended rather than replaced: `TestAddMemberAddsAnExistingAccountWithoutSendingEmail` and `TestAddMemberInvitesAnUnknownAddress` gain an assertion on `invited`; `TestAddMemberIsRateLimited` and `TestAddMemberIsRateLimitedInstanceWide` gain one on `ErrorDetail.Value`. No new fixture work.

**Vitest.** `server/members.test.ts` extended for the three mutations. New `member-invite-form.test.tsx` and `member-list.test.tsx`, plus `.stories.tsx` for both — every component in this repo has one. A route test covering the loader's redirect path and both classifiers. The seven invite failure shapes are exercised here with MSW, where producing them costs nothing.

**e2e.** A new `members.spec.ts` covering: the list renders the acting member; an admin invites an address that already has an account and sees the "will not be notified" message; an admin changes a second member's role and the row updates; an admin removes a second member; and a viewer sees the page with no controls. `'members'` joins `'audit-log'` in `AUTHENTICATED_PATHS` in `i18n.spec.ts`; that crawl needs no seeding, since the crawling account is itself in the list.

Two fixture facts constrain how those cases may be written, and both bite silently if ignored.

**There is no second account to work with.** `e2e/fixtures/auth.ts` creates exactly one user per test — `e2e-<uuid>@example.com`, through `admin.auth.admin.createUser` — and provisions a fresh team with that user as its only member. Every case above except the first needs a second person, so this design adds one helper to `e2e/fixtures/seed.ts`:

```ts
seedSecondMember(teamId: string, role: TeamRole): Promise<{ email: string; userId: string }>
```

It creates a confirmed auth user the same way the auth fixture does — the `team_member` row has a foreign key into `auth.users`, so a bare SQL insert is not enough — and then inserts the membership. It returns the address, which the invite case needs in order to target an account that already exists.

**`setFixtureTeamRole` throws unless the team has exactly one membership.** Its own docstring pins that: it identifies the row by team id alone, and it checks `rowCount === 1` rather than assuming, because an update matching no row would silently leave the session an owner and let a spec asserting something is _absent_ pass for the wrong reason. This page is the first screen whose tests deliberately create a second membership, so the two helpers now interact: **call `setFixtureTeamRole` first, while one membership still exists, and `seedSecondMember` after it.** The reverse order throws. The viewer case needs both, in that order.

**The invitation must target an address that already has an account.** An unknown address makes Supabase send real mail against the shared Resend quota, which no test should do; `seedSecondMember` exists partly to make the safe path the convenient one. Note that the no-mail path still spends the rate limiters, because `allowInvite` charges both _before_ resolving the address. That is affordable only because of #86: preview's counter is now `preview:rl:invite:global`, where until that change preview and production shared one Redis and one such key.

## Files

| Path | What |
| --- | --- |
| `apps/api/internal/api/members.go` | `AddedMember`, typed 429 details |
| `apps/api/internal/api/members_test.go` | four tests extended |
| `apps/api/openapi.json`, `packages/api-client/src/generated/**` | regenerated |
| `apps/web/src/server/members.ts` | three mutation pairs added |
| `apps/web/src/components/member-invite-form.tsx` | new, + test + stories |
| `apps/web/src/components/member-list.tsx` | new, + test + stories |
| `apps/web/src/routes/_authed/teams.$teamSlug.members.tsx` | new, + test + a11y test |
| `apps/web/src/components/app-sidebar.tsx` | one `SidebarMenuItem` inside the existing team-scoped block, with no role condition |
| `apps/web/src/i18n/locales/{en,de}.json` | `members.*`, `nav.members` |
| `apps/web/src/i18n/catalogues.test.ts` | one `identicalByDesign` entry |
| `apps/web/e2e/fixtures/seed.ts` | `seedSecondMember` added |
| `apps/web/e2e/members.spec.ts` | new |
| `apps/web/e2e/i18n.spec.ts` | one path added |

`apps/web/src/routeTree.gen.ts` regenerates through `pnpm --filter=web run build`, never through a CLI — see `CLAUDE.md`, and note that the `generate-routes` script was removed in #85 for that reason.

## Open questions this design does not answer

None blocking. Two it deliberately leaves where it found them: self-removal, and the missing notification for a directly added member. Both are recorded under "Scope" above with the reason they are not solved here.
