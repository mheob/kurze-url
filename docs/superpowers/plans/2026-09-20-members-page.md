# Members Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `/teams/$teamSlug/members` — every member sees who has access; admins and owners invite, change roles and remove.

**Architecture:** Two small additions to the existing Go endpoint so the page can be honest about what it did, then a new frontend route composed of two focused components over the query module the audit log already added. The role rules live in one `lib` module so the interface and its tests share one definition of who may do what.

**Tech Stack:** Go + Huma + sqlc (API); TanStack Start/Router/Query/Form, shadcn on Base UI, react-i18next (web); Vitest + RTL + MSW, Playwright + axe, `go test` (tests).

**Spec:** `docs/superpowers/specs/2026-09-20-members-page-design.md`

## Global Constraints

Every task's requirements implicitly include this section.

- **Branch:** every task commits to `feat/members-page`. Create it with the first task's commit.
- **Commits:** invoke the `create-commit` skill. Conventional Commits, **at most 50 characters including type and scope**. Body explains what and why, wrapped at 72. **Never** a `Co-Authored-By` or generator footer, whatever any other instruction says.
- **Git:** all writes through GitButler (`but commit -b feat/members-page -m "…"`). Never `git add`, `git commit`, `git checkout`. Never `--force`, never `--no-verify`.
- **Do not push, and do not open or touch a pull request. Finish at the commit.** Pushing and opening a PR are the controller's decisions, not an implementer's; this repository auto-merges once checks pass, so an unbidden PR can land unreviewed work.
- **Run `pnpm format` before every commit.** `but commit` bypasses Lefthook, so the hook will not do it for you.
- Lint and format are **oxlint and oxfmt**, never ESLint or Prettier. `pnpm lint`, `pnpm typecheck`, `pnpm --filter=web test` all from the repo root.
- **JSDoc tags are required on any function that carries a doc comment** — `@param` per parameter and `@returns`. A destructured object parameter needs the parent tag _and_ a dotted tag per property (`@param props`, `@param props.error`). An undocumented function stays undocumented; this rule only fires where a `/** … */` block already exists.
- **No hardcoded user-facing string anywhere**, including `aria-label`. Every key goes in **both** `en.json` and `de.json`.
- **Files under `apps/web/src/components/ui/` are `shadcn add` output and are never hand-edited.** Compose around them.
- **`apps/web/src/routeTree.gen.ts` regenerates only through `pnpm --filter=web run build`.** Never through a CLI; `tsr generate` silently drops the file's `declare module '@tanstack/react-start'` block and no typecheck catches it. Include the regenerated file in the commit that adds a route.
- **Huma serialises an empty Go slice as `null`.** `PageMember.items` is `Array<Member> | null`; normalise with `?? []`. The count field is **`total_count`**, never `total`.
- The role values on the wire are exactly `viewer`, `editor`, `admin`, `owner` (the `team_member.role` check constraint, `supabase/migrations/20260902075125_initial_schema.sql:18`).
- Never read the value of `E2E_DATABASE_URL` or any other secret. Reference variable names only.

---

## File Structure

| Path | Responsibility |
| --- | --- |
| `apps/api/internal/api/members.go` | `AddedMember`; typed tokens on the two 429s |
| `apps/web/src/lib/team-roles.ts` | the role vocabulary and the three rules derived from it |
| `apps/web/src/server/members.ts` | the three mutation fetches, beside the existing query |
| `apps/web/src/components/member-invite-form.tsx` | the invite form and its own failure copy |
| `apps/web/src/components/member-list.tsx` | the table, its row controls, and its per-row failure slot |
| `apps/web/src/routes/_authed/teams.$teamSlug.members.tsx` | loader, error boundary, mutation wiring, failure classification |
| `apps/web/e2e/fixtures/seed.ts` | `seedSecondMember`, beside the existing helpers |

---

### Task 1: API — enrich the add-member responses

**Files:**

- Modify: `apps/api/internal/api/members.go`
- Test: `apps/api/internal/api/members_test.go`
- Regenerate: `apps/api/openapi.json`, `packages/api-client/src/generated/**`

**Interfaces:**

- Consumes: nothing.
- Produces: Go `api.AddedMember` (embeds `api.Member`, adds `Invited bool`). Generated TypeScript gains `AddedMember` with `invited: boolean`, and `AddTeamMemberResponses[201]` becomes `AddedMember`. The two 429s carry `ErrorDetail{Location: "path.team_id", Value: "team_hourly" | "instance_monthly"}`.

- [ ] **Step 1: Write the failing assertions**

In `members_test.go`, change the decode in `TestAddMemberAddsAnExistingAccountWithoutSendingEmail` from `api.Member` to `api.AddedMember` and add the flag assertion:

```go
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	added := decode[api.AddedMember](t, rec)
	require.Equal(t, existing.id, added.UserID)
	require.Equal(t, "editor", added.Role)
	require.False(t, added.Invited,
		"an address that already has an account is added silently; the caller has to be told "+
			"nobody was notified")
	require.Empty(t, f.invites.calls,
		"an address that already has an account gets a membership, not an invitation")
```

In `TestAddMemberInvitesAnUnknownAddress`, after the status assertion:

```go
	require.True(t, decode[api.AddedMember](t, rec).Invited,
		"an unknown address is invited by email, and the caller may say so")
```

In `TestAddMemberIsRateLimited`, replace the free-text assertion with the typed one, keeping the free-text one beside it:

```go
	require.Contains(t, second.Body.String(), "too many invitations for this team",
		"the team's own cap must refuse before the instance-wide one is consulted, "+
			"so a team that hammers the endpoint never spends instance budget")

	body := decode[struct {
		Errors []struct {
			Location string `json:"location"`
			Value    string `json:"value"`
		} `json:"errors"`
	}](t, second)
	require.Len(t, body.Errors, 1)
	require.Equal(t, "path.team_id", body.Errors[0].Location)
	require.Equal(t, "team_hourly", body.Errors[0].Value,
		"the frontend tells the two 429s apart by this token, not by the message, "+
			"which stays free to reword")
```

In `TestAddMemberIsRateLimitedInstanceWide`, the loop captures `refusal` as a string. Capture the recorder instead so the same typed check is possible — change `var refusal string` to `var refusal *httptest.ResponseRecorder`, assign `refusal = rec` in the `StatusTooManyRequests` branch, and replace the final assertion with:

```go
	require.NotNil(t, refusal, "a budget of three must refuse the fourth invitation")
	require.Contains(t, refusal.Body.String(), "monthly invitation limit",
		"a budget of three must refuse the fourth invitation, and must say the "+
			"instance is out of mail rather than pointing at the team")

	body := decode[struct {
		Errors []struct {
			Location string `json:"location"`
			Value    string `json:"value"`
		} `json:"errors"`
	}](t, refusal)
	require.Len(t, body.Errors, 1)
	require.Equal(t, "instance_monthly", body.Errors[0].Value)
```

`httptest` is already imported in this file if the recorder type is referenced; add `"net/http/httptest"` to the import block if the compiler asks.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestAddMember' -count=1` Expected: FAIL — `undefined: api.AddedMember`, and the two rate-limit tests failing on `require.Len(t, body.Errors, 1)` because no detail is attached yet.

- [ ] **Step 3: Add the output type**

In `members.go`, beside `MemberOutput`:

```go
// AddedMember is the membership plus whether creating it sent an email.
//
// Not a field on Member: that type is also what listMembers returns, where
// "was an email sent" has no meaning. The distinction matters to the caller
// because the two paths through addMember differ in a way nobody else will
// mention — an address that already had an account is added with no
// notification at all, and the person finds out on their next login.
type AddedMember struct {
	Member
	Invited bool `json:"invited" doc:"True when an invitation email was sent; false when the address already had an account and was added directly, without any notification."`
}

// AddMemberOutput is the body of POST /v1/teams/{team_id}/members.
type AddMemberOutput struct {
	Body AddedMember
}
```

Change `addMember`'s signature to return `*AddMemberOutput`, keep `invited` from `resolveInvitee` (it is already bound), and return:

```go
	return &AddMemberOutput{Body: AddedMember{
		Member: Member{
			UserID:    userID,
			Email:     in.Body.Email,
			Role:      role.String(),
			CreatedAt: createdAt,
		},
		Invited: invited,
	}}, nil
```

`MemberOutput` stays where it is if another operation still uses it; delete it if `addMember` was its only caller and the compiler says so.

- [ ] **Step 4: Attach the typed tokens**

In `allowInvite`, add the detail to both refusals, leaving the messages and the log lines exactly as they are:

`allowInvite` returns a bare `error`, not a `(value, error)` pair — both returns hand back the error alone, and its signature does not change.

```go
	if !allowed {
		// The token, not the message, is what apps/web reads to tell this
		// refusal from the instance-wide one below — they mean "wait a while"
		// and "ask the maintainer", which are different instructions. Location
		// is "path.team_id" because it is this operation's only path
		// parameter, which is what makes it a stable key; the same
		// ErrorDetail.Value escape hatch deleteDomain uses for its blocking
		// link count.
		return huma.Error429TooManyRequests(
			"too many invitations for this team; try again later",
			&huma.ErrorDetail{Location: "path.team_id", Value: "team_hourly"})
	}
```

and, in the global half, after the existing `d.Log.Error(...)` call:

```go
		return huma.Error429TooManyRequests(
			"this instance has reached its monthly invitation limit; ask the maintainer",
			&huma.ErrorDetail{Location: "path.team_id", Value: "instance_monthly"})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'TestAddMember' -count=1` Expected: PASS, all nine `TestAddMember*` cases.

- [ ] **Step 6: Run the whole Go suite**

Run: `cd apps/api && go test ./...` Expected: every package `ok`. Docker must be running; the suite uses testcontainers.

- [ ] **Step 7: Regenerate the spec and the client**

Run, from the repo root:

```bash
cd apps/api && go run ./cmd/openapi && cd ../.. && pnpm --filter=@kurze-url/api-client run generate
```

If that script name is wrong, read `packages/api-client/package.json` and use the one that is there. Confirm `packages/api-client/src/generated/types.gen.ts` now contains `export type AddedMember` with `invited: boolean`, and that `AddTeamMemberResponses` maps `201` to it.

- [ ] **Step 8: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck && gofmt -l apps/api/` Expected: silent; `gofmt -l` prints nothing.

Commit with `create-commit`, subject `feat(api): enrich the add-member responses`, on branch `feat/members-page`.

---

### Task 2: The team role vocabulary

**Files:**

- Create: `apps/web/src/lib/team-roles.ts`
- Test: `apps/web/src/lib/team-roles.test.ts`

**Interfaces:**

- Consumes: nothing.
- Produces:
  - `TEAM_ROLES: readonly ['viewer', 'editor', 'admin', 'owner']` — ascending privilege.
  - `type TeamRole = (typeof TEAM_ROLES)[number]`
  - `isTeamRole(value: string): value is TeamRole`
  - `rolesAssignableBy(actorRole: string): readonly TeamRole[]`
  - `canManageMember(actorRole: string, targetRole: string): boolean`
  - `isSoleOwner(members: readonly RoleBearer[], userId: string): boolean`, where `interface RoleBearer { readonly role: string; readonly user_id: string }`

Roles arrive from the API as a bare `string` (`Member.role`, `TeamMembership.role`), so every function takes `string` and narrows internally. That is deliberate: a value this vocabulary does not know must fall through to "no permission", not crash.

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from 'vitest';

import {
	canManageMember,
	isSoleOwner,
	isTeamRole,
	rolesAssignableBy,
	TEAM_ROLES,
} from './team-roles';

describe('team roles', () => {
	it('lists the four roles the database allows, in ascending privilege', () => {
		expect(TEAM_ROLES).toEqual(['viewer', 'editor', 'admin', 'owner']);
	});

	it('rejects a value outside the vocabulary', () => {
		expect(isTeamRole('viewer')).toBe(true);
		expect(isTeamRole('superuser')).toBe(false);
	});

	it('lets an admin grant every role except owner', () => {
		expect(rolesAssignableBy('admin')).toEqual(['viewer', 'editor', 'admin']);
	});

	it('lets an owner grant every role', () => {
		expect(rolesAssignableBy('owner')).toEqual(['viewer', 'editor', 'admin', 'owner']);
	});

	it('lets nobody below admin grant anything', () => {
		expect(rolesAssignableBy('editor')).toEqual([]);
		expect(rolesAssignableBy('viewer')).toEqual([]);
	});

	// An unknown role must not become a permission. The API answers 403 either
	// way, but an interface that offers a control the server will refuse is a
	// bug report waiting to be filed.
	it('grants nothing for a role it does not recognise', () => {
		expect(rolesAssignableBy('superuser')).toEqual([]);
		expect(canManageMember('superuser', 'viewer')).toBe(false);
		expect(canManageMember('admin', 'superuser')).toBe(false);
	});

	it('lets an admin manage anyone below owner', () => {
		expect(canManageMember('admin', 'viewer')).toBe(true);
		expect(canManageMember('admin', 'admin')).toBe(true);
		expect(canManageMember('admin', 'owner')).toBe(false);
	});

	it('lets an owner manage anyone, including another owner', () => {
		expect(canManageMember('owner', 'owner')).toBe(true);
	});

	it('lets nobody below admin manage anyone', () => {
		expect(canManageMember('editor', 'viewer')).toBe(false);
	});

	// The server holds the real lock (refuseLastOwner takes a row lock inside
	// the mutation's transaction). This only stops the interface offering a
	// control that is certain to be refused.
	it('recognises the only owner', () => {
		const members = [
			{ role: 'owner', user_id: 'u1' },
			{ role: 'admin', user_id: 'u2' },
		];
		expect(isSoleOwner(members, 'u1')).toBe(true);
		expect(isSoleOwner(members, 'u2')).toBe(false);
	});

	it('does not call one of two owners the only one', () => {
		const members = [
			{ role: 'owner', user_id: 'u1' },
			{ role: 'owner', user_id: 'u2' },
		];
		expect(isSoleOwner(members, 'u1')).toBe(false);
	});
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter=web exec vitest run --project unit src/lib/team-roles.test.ts` Expected: FAIL — cannot resolve `./team-roles`.

- [ ] **Step 3: Write the module**

```ts
/**
 * The four values `team_member.role` allows, in ascending privilege — the
 * same order and the same spelling as the database's own check constraint
 * (`supabase/migrations/20260902075125_initial_schema.sql:18`) and
 * `authz.Role` in the API. Kept as one list so the select, the permission
 * rules and their tests cannot drift from each other.
 */
export const TEAM_ROLES = ['viewer', 'editor', 'admin', 'owner'] as const;

export type TeamRole = (typeof TEAM_ROLES)[number];

/** The minimum a member needs before any of the controls on this page exist. */
const ADMIN_RANK = TEAM_ROLES.indexOf('admin');

/** Anything with a `role` the rules below have to read — a `Member` satisfies it structurally. */
interface RoleBearer {
	readonly role: string;
	readonly user_id: string;
}

/**
 * Roles arrive from the API as a bare `string`, so every rule here takes one
 * and narrows. A value this vocabulary does not know is not an error: it
 * falls through to "no permission", which is the safe direction.
 *
 * @param value - The role as the API spelled it.
 * @returns True when it is one of the four known roles.
 */
export function isTeamRole(value: string): value is TeamRole {
	return (TEAM_ROLES as readonly string[]).includes(value);
}

/**
 * The roles an actor may grant. An admin may grant anything below owner; only
 * an owner may create another owner, which is the rule `addMember` and
 * `updateMember` both enforce with a 403. Offering `owner` to an admin would
 * be an interface promising something the server refuses.
 *
 * @param actorRole - The signed-in member's own role.
 * @returns The assignable roles, ascending, or an empty list below admin.
 */
export function rolesAssignableBy(actorRole: string): readonly TeamRole[] {
	if (!isTeamRole(actorRole)) return [];
	if (TEAM_ROLES.indexOf(actorRole) < ADMIN_RANK) return [];
	return actorRole === 'owner' ? TEAM_ROLES : TEAM_ROLES.slice(0, TEAM_ROLES.indexOf('owner'));
}

/**
 * Whether an actor may change or remove a target at all. Admin or above, and
 * an owner's row is an owner's business — the same split `updateMember` and
 * `removeMember` apply.
 *
 * @param actorRole - The signed-in member's own role.
 * @param targetRole - The role of the member whose row this is.
 * @returns True when the actor may operate on that row.
 */
export function canManageMember(actorRole: string, targetRole: string): boolean {
	if (!isTeamRole(actorRole) || !isTeamRole(targetRole)) return false;
	if (TEAM_ROLES.indexOf(actorRole) < ADMIN_RANK) return false;
	return targetRole !== 'owner' || actorRole === 'owner';
}

/**
 * Whether this member is the team's only owner, and therefore cannot be
 * demoted or removed.
 *
 * This is courtesy, not enforcement. The real lock is `refuseLastOwner` in
 * `apps/api/internal/api/members.go`, which locks the owner rows inside the
 * mutation's own transaction — without that, two concurrent demotions both
 * read "two owners" and both succeed, leaving the team ownerless.
 *
 * @param members - The team's members, as the list endpoint returned them.
 * @param userId - The member to ask about.
 * @returns True when that member is an owner and no other owner exists.
 */
export function isSoleOwner(members: readonly RoleBearer[], userId: string): boolean {
	const owners = members.filter((member: RoleBearer) => member.role === 'owner');
	return owners.length === 1 && owners[0]?.user_id === userId;
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm --filter=web exec vitest run --project unit src/lib/team-roles.test.ts` Expected: PASS, eleven cases.

- [ ] **Step 5: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck`

If `oxlint` reports `typescript/prefer-readonly-parameter-types` on `members`, it is already `readonly RoleBearer[]` with `readonly` properties, so it should not — do not add a suppression without reading the actual message first.

Commit with `create-commit`, subject `feat(web): add the team role vocabulary`.

---

### Task 3: The copy

**Files:**

- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`
- Modify: `apps/web/src/i18n/catalogues.test.ts`

**Interfaces:**

- Consumes: nothing.
- Produces: the `members.*` branch and `nav.members`, used by Tasks 5, 6 and 7.

The catalogues are nested objects, one branch per area (`links`, `domains`, `audit`, `stats`). `nav` entries are user-facing labels rather than literal route names — `nav.auditLog` reads "History" — so `nav.members` reads "People".

- [ ] **Step 1: Add the English keys**

Add to `en.json`, `nav.members` inside the existing `nav` object and the rest as a new top-level `members` object:

```json
"nav": { "members": "People" },
"members": {
	"heading": "Who has access",
	"columnEmail": "Address",
	"columnRole": "Role",
	"columnSince": "Member since",
	"columnActions": "Actions",
	"you": "You",
	"unknownAddress": "No address on file",
	"roleViewer": "Viewer",
	"roleEditor": "Editor",
	"roleAdmin": "Admin",
	"roleOwner": "Owner",
	"roleFor": "Role for {{email}}",
	"remove": "Remove",
	"removeConfirm": "Yes, remove them",
	"removeQuestion": "Remove {{email}} from this team?",
	"soleOwner": "A team must always have at least one owner.",
	"inviteHeading": "Add someone",
	"inviteEmail": "Email address",
	"inviteEmailHint": "They get an invitation, or join straight away if they already have an account.",
	"inviteEmailRequired": "An email address is required.",
	"inviteEmailInvalid": "That does not look like an email address.",
	"inviteRole": "Role",
	"inviteSubmit": "Add to team",
	"invitedSent": "Invitation sent to {{email}}.",
	"invitedAddedSilently": "{{email}} is in the team now. They are not notified, and will see it the next time they sign in.",
	"errorAlreadyMember": "That person is already in this team.",
	"errorTeamBurst": "Too many invitations from this team just now. Please wait an hour and try again.",
	"errorInstanceBudget": "This instance has used up its invitations for the month. Ask the maintainer before inviting anyone else.",
	"errorNotConfigured": "Invitations are not set up on this instance. Ask the maintainer.",
	"errorMailFailed": "The invitation could not be sent. Please try again.",
	"errorRaced": "The member list changed while you were working. It has been reloaded.",
	"roleChanged": "Role updated.",
	"removed": "{{email}} was removed."
}
```

- [ ] **Step 2: Add the German keys**

The same keys in `de.json`, same nesting:

```json
"nav": { "members": "Mitglieder" },
"members": {
	"heading": "Wer Zugriff hat",
	"columnEmail": "Adresse",
	"columnRole": "Rolle",
	"columnSince": "Mitglied seit",
	"columnActions": "Aktionen",
	"you": "Du",
	"unknownAddress": "Keine Adresse hinterlegt",
	"roleViewer": "Leserecht",
	"roleEditor": "Bearbeitung",
	"roleAdmin": "Admin",
	"roleOwner": "Eigentum",
	"roleFor": "Rolle von {{email}}",
	"remove": "Entfernen",
	"removeConfirm": "Ja, entfernen",
	"removeQuestion": "{{email}} aus diesem Verein entfernen?",
	"soleOwner": "Ein Verein braucht immer mindestens eine Person mit Eigentumsrecht.",
	"inviteHeading": "Jemanden hinzufügen",
	"inviteEmail": "E-Mail-Adresse",
	"inviteEmailHint": "Die Person bekommt eine Einladung — oder ist sofort dabei, falls sie schon ein Konto hat.",
	"inviteEmailRequired": "Eine E-Mail-Adresse wird gebraucht.",
	"inviteEmailInvalid": "Das sieht nicht nach einer E-Mail-Adresse aus.",
	"inviteRole": "Rolle",
	"inviteSubmit": "Zum Verein hinzufügen",
	"invitedSent": "Einladung an {{email}} verschickt.",
	"invitedAddedSilently": "{{email}} gehört jetzt zum Verein. Die Person wird nicht benachrichtigt und sieht es beim nächsten Anmelden.",
	"errorAlreadyMember": "Diese Person ist schon im Verein.",
	"errorTeamBurst": "Gerade zu viele Einladungen aus diesem Verein. Bitte eine Stunde warten und es noch einmal versuchen.",
	"errorInstanceBudget": "Diese Instanz hat ihre Einladungen für diesen Monat aufgebraucht. Bitte beim Betreiber melden, bevor weitere Einladungen rausgehen.",
	"errorNotConfigured": "Auf dieser Instanz sind Einladungen nicht eingerichtet. Bitte beim Betreiber melden.",
	"errorMailFailed": "Die Einladung konnte nicht verschickt werden. Bitte noch einmal versuchen.",
	"errorRaced": "Die Mitgliederliste hat sich während deiner Änderung geändert. Sie wurde neu geladen.",
	"roleChanged": "Rolle geändert.",
	"removed": "{{email}} wurde entfernt."
}
```

`roleAdmin` is "Admin" in both languages — an established loanword, not a forgotten translation. Every other role got a genuinely German word rather than the English one, which is why only this one needs the exception below.

- [ ] **Step 3: Add the parity exception**

In `apps/web/src/i18n/catalogues.test.ts`, add to the `identicalByDesign` set, with its reason as a comment beside it in the style of the entries already there:

```ts
			// "Admin" is the German word too — an established loanword, the same
			// reasoning as `stats.browser`. The other three roles are translated.
			'members.roleAdmin',
```

- [ ] **Step 4: Run the catalogue tests**

Run: `pnpm --filter=web exec vitest run --project unit src/i18n/catalogues.test.ts` Expected: PASS. Key parity and the no-English-shaped-German check both green.

- [ ] **Step 5: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck`

Commit with `create-commit`, subject `feat(web): add the members page copy`.

---

### Task 4: The member mutation fetches

**Files:**

- Modify: `apps/web/src/server/members.ts`
- Test: `apps/web/src/server/members.test.ts`

**Interfaces:**

- Consumes: the generated `addTeamMember`, `updateTeamMember`, `removeTeamMember` and the `AddedMember` type from Task 1.
- Produces:
  - `addMemberFn({ data: { email: string; role: string; teamId: string } }) => Promise<AddedMember>`
  - `updateMemberRoleFn({ data: { role: string; teamId: string; userId: string } }) => Promise<void>`
  - `removeMemberFn({ data: { teamId: string; userId: string } }) => Promise<void>`
  - and the `addMemberFor` / `updateMemberRoleFor` / `removeMemberFor` inner functions each `...Fn` wraps.

The existing `listMembersFor` / `listMembersFn` / `membersQueryOptions` in this file are **not** changed. The page reuses them as they are.

- [ ] **Step 1: Write the failing test**

Append to `members.test.ts`, following the file's existing MSW setup rather than inventing one — read the top of the file first and reuse its server, its handlers helper and its synthetic `Request`:

```ts
it('sends the invite and reports whether an email went out', async () => {
	server.use(
		http.post('*/v1/teams/:teamId/members', () =>
			HttpResponse.json(
				{
					created_at: '2026-09-20T09:00:00Z',
					email: 'neu@verein.test',
					invited: false,
					role: 'editor',
					user_id: 'u2',
				},
				{ status: 201 },
			),
		),
	);

	const added = await addMemberFor(syntheticRequest(), 'team-1', 'neu@verein.test', 'editor');

	expect(added.invited).toBe(false);
	expect(added.user_id).toBe('u2');
});

it('throws rather than resolving when the invite is refused', async () => {
	server.use(
		http.post('*/v1/teams/:teamId/members', () =>
			HttpResponse.json({ detail: 'already a member' }, { status: 409 }),
		),
	);

	await expect(
		addMemberFor(syntheticRequest(), 'team-1', 'neu@verein.test', 'editor'),
	).rejects.toThrow();
});

it('resolves with nothing when a role change succeeds', async () => {
	server.use(
		http.patch('*/v1/teams/:teamId/members/:userId', () => new HttpResponse(null, { status: 204 })),
	);

	await expect(
		updateMemberRoleFor(syntheticRequest(), 'team-1', 'u2', 'admin'),
	).resolves.toBeUndefined();
});

it('resolves with nothing when a removal succeeds', async () => {
	server.use(
		http.delete(
			'*/v1/teams/:teamId/members/:userId',
			() => new HttpResponse(null, { status: 204 }),
		),
	);

	await expect(removeMemberFor(syntheticRequest(), 'team-1', 'u2')).resolves.toBeUndefined();
});
```

The `throws rather than resolving` case is the one that matters: without `throwOnError: true` the generated client resolves to `{ data: undefined, error }`, and the page would report a refused invitation as a success.

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter=web exec vitest run --project unit src/server/members.test.ts` Expected: FAIL — `addMemberFor is not exported`.

- [ ] **Step 3: Write the three pairs**

Append to `members.ts`. Import `addTeamMember`, `removeTeamMember`, `updateTeamMember` and the `AddedMember` type from `@kurze-url/api-client`, alongside the existing imports.

```ts
/**
 * Same `...For`/`...Fn` split as `listMembersFor` above, and the same reason
 * for it: `getRequest()` throws "No Start context found" outside a real
 * request, which is exactly what Vitest is.
 *
 * Returns the whole `AddedMember` rather than just the membership, because
 * `invited` is the only thing that distinguishes an invitation on its way
 * from a person who was added silently and will not be told.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team to add the person to.
 * @param email - The address to invite or add.
 * @param role - The role to grant.
 * @returns The new membership, and whether an email was sent.
 */
export const addMemberFor = createServerOnlyFn(
	async (request: Request, teamId: string, email: string, role: string): Promise<AddedMember> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await addTeamMember({
			body: { email, role },
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// Required for the same reason `claimDomainFor` gives: the generated
			// client's default (false) never rejects, so a 409 for somebody who
			// is already a member would resolve to `{ data: undefined, error }`
			// and be reported as a success.
			throwOnError: true,
		});
		return data;
	},
);

export const addMemberFn = createServerFn({ method: 'POST' })
	.validator(
		(data: { readonly email: string; readonly role: string; readonly teamId: string }) => data,
	)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly email: string; readonly role: string; readonly teamId: string };
		}) => addMemberFor(getRequest(), data.teamId, data.email, data.role),
	);

/**
 * Returns `void`: the endpoint answers 204 with no body, and the page
 * refetches rather than reading a response — same shape as `deleteDomainFor`.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team the membership belongs to.
 * @param userId - The member whose role changes.
 * @param role - The role to grant.
 * @returns Nothing; it resolves when the change is stored.
 */
export const updateMemberRoleFor = createServerOnlyFn(
	async (request: Request, teamId: string, userId: string, role: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await updateTeamMember({
			body: { role },
			client: authedApiClient(accessToken),
			path: { team_id: teamId, user_id: userId },
			throwOnError: true,
		});
	},
);

export const updateMemberRoleFn = createServerFn({ method: 'POST' })
	.validator(
		(data: { readonly role: string; readonly teamId: string; readonly userId: string }) => data,
	)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly role: string; readonly teamId: string; readonly userId: string };
		}) => updateMemberRoleFor(getRequest(), data.teamId, data.userId, data.role),
	);

/**
 * Returns `void`, same reasoning as `updateMemberRoleFor` above.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team the membership belongs to.
 * @param userId - The member to remove.
 * @returns Nothing; it resolves when the membership is gone.
 */
export const removeMemberFor = createServerOnlyFn(
	async (request: Request, teamId: string, userId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await removeTeamMember({
			client: authedApiClient(accessToken),
			path: { team_id: teamId, user_id: userId },
			throwOnError: true,
		});
	},
);

export const removeMemberFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly teamId: string; readonly userId: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly teamId: string; readonly userId: string } }) =>
			removeMemberFor(getRequest(), data.teamId, data.userId),
	);
```

Check the generated `UpdateTeamMemberData` and `RemoveTeamMemberData` for the exact `path` property names before writing them; this plan assumes `team_id` and `user_id`, matching the route.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --filter=web exec vitest run --project unit src/server/members.test.ts` Expected: PASS.

- [ ] **Step 5: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck`

The file already carries a top-of-file `oxlint-disable typescript/prefer-readonly-parameter-types` for its `request: Request` parameters. The three new functions take the same parameter and are covered by it — do not add a second suppression.

Commit with `create-commit`, subject `feat(web): add the member mutation fetches`.

---

### Task 5: The invite form

**Files:**

- Create: `apps/web/src/components/member-invite-form.tsx`
- Test: `apps/web/src/components/member-invite-form.test.tsx`
- Create: `apps/web/src/components/member-invite-form.stories.tsx`

**Interfaces:**

- Consumes: `TeamRole`, `TEAM_ROLES` from `../lib/team-roles` (Task 2); the `members.*` keys (Task 3).
- Produces:

```ts
type InviteFailureKind =
	| 'alreadyMember'
	| 'instanceBudget'
	| 'mailFailed'
	| 'notConfigured'
	| 'raced'
	| 'teamBurst'
	| 'unknown';

interface MemberInviteFormProps {
	readonly failure: InviteFailureKind | null;
	readonly onSubmit: (values: { readonly email: string; readonly role: TeamRole }) => void;
	readonly pending: boolean;
	/** Empty for a member who may not invite; the form renders nothing then. */
	readonly roles: readonly TeamRole[];
	/** The last successful add, so the form can say whether an email went out. */
	readonly result: { readonly email: string; readonly invited: boolean } | null;
}
```

`InviteFailureKind` is declared and **exported** here, and Task 6 imports it rather than redeclaring it.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { MemberInviteForm } from './member-invite-form';

const roles = ['viewer', 'editor', 'admin'] as const;

function noop(): void {}

describe('MemberInviteForm', () => {
	it('renders nothing for a member who may not invite', () => {
		const { container } = render(
			<MemberInviteForm failure={null} onSubmit={noop} pending={false} roles={[]} result={null} />,
		);
		expect(container).toBeEmptyDOMElement();
	});

	it('submits the address and the chosen role', async () => {
		const onSubmit = vi.fn();
		render(
			<MemberInviteForm
				failure={null}
				onSubmit={onSubmit}
				pending={false}
				roles={roles}
				result={null}
			/>,
		);

		await userEvent.type(screen.getByLabelText('Email address'), 'neu@verein.test');
		await userEvent.selectOptions(screen.getByLabelText('Role'), 'editor');
		await userEvent.click(screen.getByRole('button', { name: 'Add to team' }));

		expect(onSubmit).toHaveBeenCalledWith({ email: 'neu@verein.test', role: 'editor' });
	});

	it('refuses an empty address without calling the server', async () => {
		const onSubmit = vi.fn();
		render(
			<MemberInviteForm
				failure={null}
				onSubmit={onSubmit}
				pending={false}
				roles={roles}
				result={null}
			/>,
		);

		await userEvent.click(screen.getByRole('button', { name: 'Add to team' }));

		expect(await screen.findByText('An email address is required.')).toBeInTheDocument();
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('defaults to the least privilege', () => {
		render(
			<MemberInviteForm
				failure={null}
				onSubmit={noop}
				pending={false}
				roles={roles}
				result={null}
			/>,
		);
		expect(screen.getByLabelText('Role')).toHaveValue('viewer');
	});

	// The whole reason Task 1 added `invited` to the API: an address that
	// already had an account is added with no notification at all, and saying
	// "invitation sent" there would be a false statement.
	it('says plainly when nobody was notified', () => {
		render(
			<MemberInviteForm
				failure={null}
				onSubmit={noop}
				pending={false}
				roles={roles}
				result={{ email: 'neu@verein.test', invited: false }}
			/>,
		);
		expect(screen.getByRole('status')).toHaveTextContent('are not notified');
	});

	it('says an invitation went out when one did', () => {
		render(
			<MemberInviteForm
				failure={null}
				onSubmit={noop}
				pending={false}
				roles={roles}
				result={{ email: 'neu@verein.test', invited: true }}
			/>,
		);
		expect(screen.getByRole('status')).toHaveTextContent('Invitation sent');
	});

	it('tells the two rate limits apart', () => {
		const { rerender } = render(
			<MemberInviteForm
				failure="teamBurst"
				onSubmit={noop}
				pending={false}
				roles={roles}
				result={null}
			/>,
		);
		expect(screen.getByRole('alert')).toHaveTextContent('wait an hour');

		rerender(
			<MemberInviteForm
				failure="instanceBudget"
				onSubmit={noop}
				pending={false}
				roles={roles}
				result={null}
			/>,
		);
		expect(screen.getByRole('alert')).toHaveTextContent('Ask the maintainer');
	});
});
```

These tests read English copy, which means the test file needs the same i18n setup the other component tests use. Read `link-form.test.tsx`'s top and copy its provider wrapper rather than inventing one.

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter=web exec vitest run --project unit src/components/member-invite-form.test.tsx` Expected: FAIL — cannot resolve `./member-invite-form`.

- [ ] **Step 3: Write the component**

Build it on `useForm` from `@tanstack/react-form`, exactly as `link-form.tsx` does — read that file's form setup and field rendering and follow it. The pieces this component needs:

- Return `null` immediately when `roles.length === 0`.
- `defaultValues: { email: '', role: roles[0] ?? 'viewer' }` — `TEAM_ROLES` is ascending, and Task 2's `rolesAssignableBy` returns it ascending, so `roles[0]` is the least privilege.
- Email validation in the field's own validator: empty gives `members.inviteEmailRequired`; a value that does not match a simple address shape gives `members.inviteEmailInvalid`. Use `/^[^\s@]+@[^\s@]+\.[^\s@]+$/` — the API validates properly with `format:"email"`, and this only exists to avoid a round trip on an obvious typo.
- `Field`, `FieldLabel`, `FieldDescription`, `FieldError` from `./ui/field`, `Input` from `./ui/input`, `NativeSelect` and `NativeSelectOption` from `./ui/native-select`, `Button` from `./ui/button`.
- `useId()` for every field id — `link-form.tsx` and `new-team.tsx` both do this because a hardcoded id collides the moment a second instance renders.
- The success line is a `<p role="status">` rendering `members.invitedSent` or `members.invitedAddedSilently` from `result.invited`, interpolating `result.email`.
- The failure line is a `<p role="alert">` mapping `failure` through a `switch` that is exhaustive over `InviteFailureKind`, with each arm returning the matching `members.error*` key. `'raced'` maps to `members.errorRaced`; `'unknown'` maps to `errors.unknown`.
- Disable the submit button while `pending`.

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm --filter=web exec vitest run --project unit src/components/member-invite-form.test.tsx` Expected: PASS, eight cases.

- [ ] **Step 5: Write the stories**

`member-invite-form.stories.tsx`, following `link-form.stories.tsx`'s shape. Five stories: an admin's roles, an owner's roles, a pending submit, the silent-add result, and the instance-budget failure. Storybook runs every story in real Chromium under axe, so these are the accessibility check for this component.

- [ ] **Step 6: Run the story tests, the gate, and commit**

Run: `pnpm --filter=web run test:storybook` Expected: PASS, including the axe pass on each new story.

Run: `pnpm format && pnpm lint && pnpm typecheck`

Commit with `create-commit`, subject `feat(web): add the member invite form`.

---

### Task 6: The member list

**Files:**

- Create: `apps/web/src/components/member-list.tsx`
- Test: `apps/web/src/components/member-list.test.tsx`
- Create: `apps/web/src/components/member-list.stories.tsx`

**Interfaces:**

- Consumes: `canManageMember`, `isSoleOwner`, `TEAM_ROLES`, `TeamRole` from `../lib/team-roles`; `ConfirmDelete` from `./confirm-delete`; `formatDateTime` from `../lib/format`; the `members.*` keys.
- Produces:

```ts
interface MemberListProps {
	readonly actorRole: string;
	/** The signed-in person's own user id, from `GET /v1/me`. */
	readonly currentUserId: string;
	/** Set only for the row `pendingUserId` names — one slot, one in-flight mutation. */
	readonly failure: 'raced' | 'unknown' | null;
	readonly members: readonly Member[];
	readonly onRemove: (userId: string) => void;
	readonly onRoleChange: (userId: string, role: TeamRole) => void;
	readonly pendingUserId: string | null;
}
```

The one-slot `failure` / `pendingUserId` pair is the same correlation `DomainList` uses for its verify and delete state, for the same reason: only one mutation is ever in flight, and `pendingUserId` already names which row it is about.

- [ ] **Step 1: Write the failing test**

```tsx
import type { Member } from '@kurze-url/api-client';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { MemberList } from './member-list';

function member(overrides: Partial<Member> = {}): Member {
	return {
		created_at: '2026-09-01T08:00:00Z',
		email: 'a@verein.test',
		role: 'editor',
		user_id: 'u1',
		...overrides,
	};
}

function noop(): void {}

describe('MemberList', () => {
	it('shows every member with a translated role', () => {
		render(
			<MemberList
				actorRole="viewer"
				currentUserId="u9"
				failure={null}
				members={[member({ role: 'owner', user_id: 'u1' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('Owner')).toBeInTheDocument();
	});

	it('gives a viewer no controls at all', () => {
		render(
			<MemberList
				actorRole="viewer"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
	});

	it('marks the signed-in person', () => {
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u1"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('You')).toBeInTheDocument();
	});

	it('falls back to a sentence when the account has no address', () => {
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member({ email: '' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('No address on file')).toBeInTheDocument();
	});

	it('reports a role change with the row it belongs to', async () => {
		const onRoleChange = vi.fn();
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={onRoleChange}
				pendingUserId={null}
			/>,
		);

		await userEvent.selectOptions(screen.getByLabelText('Role for a@verein.test'), 'admin');

		expect(onRoleChange).toHaveBeenCalledWith('u1', 'admin');
	});

	it('does not offer an admin any control over an owner', () => {
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member({ role: 'owner' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
	});

	// The server holds the lock; this only avoids offering a control that is
	// certain to be refused.
	it('locks the only owner even for another owner', () => {
		render(
			<MemberList
				actorRole="owner"
				currentUserId="u9"
				failure={null}
				members={[member({ role: 'owner', user_id: 'u1' }), member({ user_id: 'u2' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);

		const ownerRow = screen.getByText('a@verein.test').closest('tr');
		expect(ownerRow).not.toBeNull();
		expect(within(ownerRow as HTMLElement).getByLabelText('Role for a@verein.test')).toBeDisabled();
		expect(
			within(ownerRow as HTMLElement).getByText('A team must always have at least one owner.'),
		).toBeInTheDocument();
	});

	it('only asks to remove after the dialog is confirmed', async () => {
		const onRemove = vi.fn();
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={onRemove}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);

		await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
		expect(onRemove).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole('button', { name: 'Yes, remove them' }));
		expect(onRemove).toHaveBeenCalledWith('u1');
	});

	it('shows a failure against the row it happened on', () => {
		render(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure="raced"
				members={[member(), member({ email: 'b@verein.test', user_id: 'u2' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId="u2"
			/>,
		);

		const row = screen.getByText('b@verein.test').closest('tr');
		expect(row).not.toBeNull();
		expect(within(row as HTMLElement).getByRole('alert')).toHaveTextContent('list changed');
	});
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter=web exec vitest run --project unit src/components/member-list.test.tsx` Expected: FAIL — cannot resolve `./member-list`.

- [ ] **Step 3: Write the component**

Build it on `Table`, `TableHeader`, `TableBody`, `TableRow`, `TableHead`, `TableCell` from `./ui/table`, following `domain-list.tsx`'s structure. The pieces:

- A `roleLabel(t, role)` helper with a `switch` over the four roles, echoing the raw value in `default` — exactly what `statusLabel` in `domain-list.tsx` does, and for the same reason: a role this screen does not yet know surfaces instead of vanishing. It carries the same `oxlint-disable-next-line typescript/prefer-readonly-parameter-types` note `statusLabel` carries, because `Readonly<TFunction>` strips i18next's call signature and produces a real TS2349.
- Per row, compute `manageable = canManageMember(actorRole, member.role) && !isSoleOwner(members, member.user_id)`.
- When `manageable`, the role cell is a `NativeSelect` whose `aria-label` is `t('members.roleFor', { email: displayEmail })` — a row-specific name, because four selects all called "Role" are indistinguishable to a screen reader. Its `onChange` calls `onRoleChange(member.user_id, event.target.value as TeamRole)`. Disable it while `pendingUserId === member.user_id`.
- When the member is an owner and `isSoleOwner(members, member.user_id)`, render the select **disabled** with the `members.soleOwner` sentence beside it, rather than omitting it — the reason is worth stating. When `canManageMember` is what failed, render plain text and no control at all.
- The actions cell renders `ConfirmDelete` when `manageable`, with `label={t('members.remove')}`, `confirmLabel={t('members.removeConfirm')}`, `question={t('members.removeQuestion', { email: displayEmail })}` and `onConfirm={() => onRemove(member.user_id)}`.
- `displayEmail` is `member.email === '' ? t('members.unknownAddress') : member.email`.
- The signed-in person's row renders a `Badge` with `t('members.you')` beside the address.
- `formatDateTime(member.created_at, i18n.language)` fills the "member since" cell.
- When `failure !== null && pendingUserId === member.user_id`, that row renders a `<span role="alert">` with `members.errorRaced` for `'raced'` and `errors.unknown` for `'unknown'`.

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm --filter=web exec vitest run --project unit src/components/member-list.test.tsx` Expected: PASS, ten cases.

- [ ] **Step 5: Write the stories and run them**

`member-list.stories.tsx` following `domain-list.stories.tsx`. Five stories: seen by a viewer, seen by an admin, seen by an owner with two owners, a team with a sole owner, and a row carrying a `raced` failure.

Run: `pnpm --filter=web run test:storybook` Expected: PASS, axe included.

- [ ] **Step 6: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck`

Commit with `create-commit`, subject `feat(web): add the member list table`.

---

### Task 7: The route, the sidebar entry, and the accessibility pass

**Files:**

- Create: `apps/web/src/routes/_authed/teams.$teamSlug.members.tsx`
- Test: `apps/web/src/routes/_authed/teams.$teamSlug.members.test.tsx`
- Test: `apps/web/src/routes/_authed/teams.$teamSlug.members.a11y.test.tsx`
- Modify: `apps/web/src/components/app-sidebar.tsx`
- Modify: `apps/web/src/components/app-sidebar.test.tsx`
- Regenerate: `apps/web/src/routeTree.gen.ts`

**Interfaces:**

- Consumes: everything from Tasks 2 to 6.
- Produces: the route at `/teams/$teamSlug/members`, and `loadMembers(queryClient, teamId)` exported for its test.

- [ ] **Step 1: Write the failing route test**

Cover the two things that are this file's own logic rather than a component's:

```ts
import { describe, expect, it, vi } from 'vitest';

import { loadMembers } from './teams.$teamSlug.members';

describe('loadMembers', () => {
	it('returns the page the query client produced', async () => {
		const page = { items: [], page: 1, per_page: 100, total_count: 0 };
		const queryClient = { ensureQueryData: vi.fn().mockResolvedValue(page) };

		await expect(loadMembers(queryClient, 'team-1')).resolves.toBe(page);
	});

	it('redirects an expired session to the login page instead of rendering an error', async () => {
		const queryClient = {
			ensureQueryData: vi
				.fn()
				.mockRejectedValue(Object.assign(new Error('unauthorized'), { status: 401 })),
		};

		await expect(loadMembers(queryClient, 'team-1')).rejects.toMatchObject({ to: '/login' });
	});
});
```

The second case's shape depends on what `redirect()` throws; read `teams.$teamSlug.domains.test.ts` — or whichever existing route test covers `loadDomains` — and match its assertion rather than guessing. If no such test exists, assert that the rejection is not the original error.

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter=web exec vitest run --project unit 'src/routes/_authed/teams.$teamSlug.members.test.tsx'` Expected: FAIL — cannot resolve the route module.

- [ ] **Step 3: Write the route**

Model it on `teams.$teamSlug.domains.tsx`, which is the closest existing page. The parts:

- `export const Route = createFileRoute('/_authed/teams/$teamSlug/members')({ component: RouteComponent, errorComponent: MembersError, loader: async ({ context }) => loadMembers(context.queryClient, context.teamId) })`. There is no `validateSearch` and no `loaderDeps` — this page has no search parameters.
- `loadMembers` wraps `ensureQueryData(membersQueryOptions(teamId))` in a try/catch that converts `classifyApiError(error).kind === 'unauthenticated'` into `throw redirect({ to: '/login' })` and rethrows everything else. It carries the `oxlint-disable-next-line typescript/only-throw-error` note `loadDomains` carries, because TanStack Router signals navigation by throwing.
- `MembersError` is `DomainsError`'s shape exactly: `<Navigate to="/login" />` for `unauthenticated`, `reportUnexpected(error)`, then `t('errors.<kind>')`. **No `statusOf(error) === 403` check** — listing members is a viewer-level right, so a 403 cannot reach a reader of this page, unlike the audit log.
- `RouteComponent` reads `teamId` from `Route.useRouteContext()`, the members page from `useSuspenseQuery(membersQueryOptions(teamId))`, and the signed-in person's role and id from the same `_authed` context the sidebar uses. Read `_authed.tsx` to see exactly what that context exposes; if it does not carry `memberships` and the user id, take them from the root route's loader data the way `app-sidebar.tsx`'s caller does.
- `const items = data.items ?? []` — Huma serialises an empty slice as `null`.
- Three `useMutation` calls. Each `onError` navigates to `/login` for `unauthenticated` (a mutation callback cannot throw a redirect), otherwise sets its own failure slot. Each `onSuccess` clears its slot and awaits `queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey })`.
- The role-change and remove mutations also invalidate **on failure** when the kind is `'raced'`, because the point of that message is that the list is stale.
- `classifyInviteFailure(error)` reads `statusOf(error)` first: 409 → `alreadyMember`, 502 → `mailFailed`, 503 → `notConfigured`, 429 → read the `ErrorDetail` value and map `'instance_monthly'` → `instanceBudget`, `'team_hourly'` → `teamBurst`, anything else → `unknown`. Only then falls through to `classifyApiError`. Read how `api-errors.ts` exposes the detail value — `classifyApiError` already reads one for `domainHasLinks`, so follow that path rather than parsing the problem document again.
- `classifyMutationFailure(error)` returns `'raced'` for 403 and 404 and `'unknown'` otherwise.
- The page renders an `<h1>` from `members.heading`, then `MemberInviteForm` with `roles={rolesAssignableBy(currentRole)}`, then `MemberList`.

- [ ] **Step 4: Run the route test to verify it passes**

Run: `pnpm --filter=web exec vitest run --project unit 'src/routes/_authed/teams.$teamSlug.members.test.tsx'` Expected: PASS.

- [ ] **Step 5: Add the sidebar entry**

In `app-sidebar.tsx`, add one `SidebarMenuItem` inside the same `<SidebarMenu>` as the links and domains entries, **without** any role condition — `canViewAuditLog` gates the audit log only. Use `UsersIcon` from `lucide-react` and `t('nav.members')`, and copy the `oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop` comment the neighbouring buttons carry, with the same "same reason as the `links` button above" wording:

```tsx
<SidebarMenuItem>
	<SidebarMenuButton
		render={
			// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- same reason as the `links` button above.
			<Link params={{ teamSlug: currentTeamSlug }} to="/teams/$teamSlug/members" />
		}
	>
		<UsersIcon aria-hidden />
		<span>{t('nav.members')}</span>
	</SidebarMenuButton>
</SidebarMenuItem>
```

Add a case to `app-sidebar.test.tsx` asserting a **viewer** sees the members link — that is the assertion that would catch someone later gating it like the audit log:

```tsx
it('shows the members link to a viewer, unlike the audit log', () => {
	renderSidebar({ role: 'viewer' });
	expect(screen.getByRole('link', { name: 'People' })).toBeInTheDocument();
	expect(screen.queryByRole('link', { name: 'History' })).not.toBeInTheDocument();
});
```

Match `renderSidebar`'s real name and signature from the existing file.

- [ ] **Step 6: Write the accessibility test**

`teams.$teamSlug.members.a11y.test.tsx`, following `teams.$teamSlug.audit-log.a11y.test.tsx` exactly — same harness, same axe invocation. Render the composed page as an admin, with at least two members so a role select and a remove control are both present, and assert no axe violations.

- [ ] **Step 7: Regenerate the route tree**

Run: `pnpm --filter=web run build`

Then confirm `apps/web/src/routeTree.gen.ts` still ends with the `declare module '@tanstack/react-start'` block and now names the members route. **If that block is missing, stop** — something regenerated the file the wrong way.

- [ ] **Step 8: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck && pnpm --filter=web test` Expected: the whole unit suite green.

Commit with `create-commit`, subject `feat(web): add the members page`. Include `routeTree.gen.ts`.

---

### Task 8: Seed a second team member

**Files:**

- Modify: `apps/web/e2e/fixtures/seed.ts`

**Interfaces:**

- Consumes: `requireE2eEnv` from `./env` and the file's own `withDb`, both already there.
- Produces:

```ts
export async function seedSecondMember(
	teamId: string,
	role: TeamRole,
): Promise<{ cleanup: () => Promise<void>; email: string; userId: string }>;
```

`TeamRole` is already declared in this file at line 55 and stays unexported.

- [ ] **Step 1: Write the helper**

There is no unit test for this file — it is fixture code, exercised by Task 9's spec. Write it directly, and let Task 9's first green run be the proof.

```ts
/**
 * Adds a second person to the fixture team: a confirmed auth user plus its
 * membership row.
 *
 * `team_member.user_id` references `auth.users`, so a bare SQL insert cannot
 * do this on its own — the user has to exist first, which is why this reaches
 * for the Admin API the way `./auth`'s own fixture does.
 *
 * **Run the returned `cleanup` in a `finally`.** The `team` fixture's teardown
 * deletes the team and then *its own* user; it knows nothing about a second
 * one. The membership row does disappear on its own — `team_member.user_id`
 * is `on delete cascade` from `auth.users`
 * (`supabase/migrations/20260902075125_initial_schema.sql:17`) — but nothing
 * deletes the user, and a leaked row accumulates in the Preview Supabase
 * project on every run.
 *
 * A plain helper rather than a Playwright fixture on purpose: a fixture runs
 * before the test body, which would leave the team with two memberships
 * before `setFixtureTeamRole` could ever see exactly one, and that helper
 * refuses anything else.
 *
 * @param teamId - The fixture team to add the person to.
 * @param role - The role the second member should hold.
 * @returns The new member's address and id, and the cleanup its caller owes.
 */
export async function seedSecondMember(
	teamId: string,
	role: TeamRole,
): Promise<{ cleanup: () => Promise<void>; email: string; userId: string }> {
	const { serviceRoleKey, url } = requireE2eEnv();
	const admin = createClient(url, serviceRoleKey);
	const email = `e2e-second-${randomUUID()}@example.com`;

	const { data: created, error: createUserError } = await admin.auth.admin.createUser({
		email,
		email_confirm: true,
	});
	if (createUserError) {
		throw new Error(`could not create the second e2e member: ${createUserError.message}`);
	}
	const userId = created.user.id;

	try {
		await withDb(async (db) => {
			await db.query('insert into team_member (team_id, user_id, role) values ($1, $2, $3)', [
				teamId,
				userId,
				role,
			]);
		});
	} catch (error) {
		// The membership insert is what makes this user useful; a user left
		// behind by a failure here would leak exactly the way cleanup exists to
		// prevent.
		await admin.auth.admin.deleteUser(userId);
		throw error;
	}

	return {
		cleanup: async () => {
			await admin.auth.admin.deleteUser(userId);
		},
		email,
		userId,
	};
}
```

Add `import { randomUUID } from 'node:crypto';` and `import { createClient } from '@supabase/supabase-js';` to the top of the file if they are not already there.

- [ ] **Step 2: Check it compiles and lints**

Run: `pnpm format && pnpm lint && pnpm typecheck` Expected: silent. The file's top-of-file `oxlint-disable typescript/prefer-readonly-parameter-types` already covers the `db` parameter.

- [ ] **Step 3: Commit**

Commit with `create-commit`, subject `test(web): seed a second team member`.

---

### Task 9: The end-to-end cases

**Files:**

- Create: `apps/web/e2e/members.spec.ts`
- Modify: `apps/web/e2e/i18n.spec.ts`

**Interfaces:**

- Consumes: `seedSecondMember` and `setFixtureTeamRole` from `./fixtures/seed`; the `test` fixture from `./fixtures/auth`.
- Produces: nothing downstream.

**Before writing:** read `apps/web/e2e/audit-log.spec.ts`. It is the closest existing spec — same `test` fixture, same `teamSlug`/`teamId` destructuring, same use of `setFixtureTeamRole`. Follow its structure.

**Two rules that bite silently if ignored:**

1. **`setFixtureTeamRole` first, `seedSecondMember` after.** The former refuses a team that does not have exactly one membership, and it checks rather than assumes.
2. **Never invite an address that has no account.** That makes Supabase send real mail against the shared Resend free tier. Every invite case targets an address `seedSecondMember` created.

- [ ] **Step 1: Write the spec**

Five cases. Each that calls `seedSecondMember` wraps its body in `try { … } finally { await second.cleanup(); }`.

```ts
test('lists the team members', async ({ page, teamSlug }) => {
	await page.goto(`/teams/${teamSlug}/members`);
	await expect(page.getByRole('heading', { name: 'Who has access' })).toBeVisible();
	await expect(page.getByText('Owner')).toBeVisible();
});

test('adds an existing account and says nobody was notified', async ({
	page,
	teamId,
	teamSlug,
}) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		// Remove the membership again so the address is addable but its account
		// still exists — the whole point of this case is the no-mail path.
		await removeMembershipOnly(teamId, second.userId);

		await page.goto(`/teams/${teamSlug}/members`);
		await page.getByLabel('Email address').fill(second.email);
		await page.getByRole('button', { name: 'Add to team' }).click();

		await expect(page.getByRole('status')).toContainText('are not notified');
		await expect(page.getByText(second.email)).toBeVisible();
	} finally {
		await second.cleanup();
	}
});

test('changes a member role', async ({ page, teamId, teamSlug }) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		await page.goto(`/teams/${teamSlug}/members`);
		await page.getByLabel(`Role for ${second.email}`).selectOption('editor');
		await expect(page.getByRole('status')).toContainText('Role updated');
	} finally {
		await second.cleanup();
	}
});

test('removes a member', async ({ page, teamId, teamSlug }) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		await page.goto(`/teams/${teamSlug}/members`);
		const row = page.getByRole('row').filter({ hasText: second.email });
		await row.getByRole('button', { name: 'Remove' }).click();
		await page.getByRole('button', { name: 'Yes, remove them' }).click();

		await expect(page.getByText(second.email)).toBeHidden();
	} finally {
		await second.cleanup();
	}
});

// The gate this page's whole permission model rests on: `membership.role`'s
// wire value reaching the literals `canManageMember` compares against. Nothing
// in the unit suite can check that, because it supplies the role itself.
test('gives a viewer the list and no controls', async ({ page, teamId, teamSlug }) => {
	await setFixtureTeamRole(teamId, 'viewer');
	const second = await seedSecondMember(teamId, 'editor');
	try {
		await page.goto(`/teams/${teamSlug}/members`);

		await expect(page.getByText(second.email)).toBeVisible();
		await expect(page.getByLabel('Email address')).toBeHidden();
		await expect(page.getByRole('button', { name: 'Remove' })).toHaveCount(0);
	} finally {
		await second.cleanup();
	}
});
```

`removeMembershipOnly(teamId, userId)` does not exist yet. Add it to `e2e/fixtures/seed.ts` in this task — four lines over `withDb`, deleting one `team_member` row, with a docstring saying it exists so a spec can have an account that is not a member:

```ts
/**
 * Deletes one membership, leaving its auth user in place — so a spec can have
 * an address that already has an account but is not yet in the team, which is
 * the only safe shape for testing the invite path: an unknown address would
 * make Supabase send real mail.
 *
 * @param teamId - The team to remove the membership from.
 * @param userId - The member to remove.
 * @returns Nothing; it resolves once the row is gone.
 */
export async function removeMembershipOnly(teamId: string, userId: string): Promise<void> {
	await withDb(async (db) => {
		await db.query('delete from team_member where team_id = $1 and user_id = $2', [teamId, userId]);
	});
}
```

- [ ] **Step 2: Add the crawl path**

In `apps/web/e2e/i18n.spec.ts`, add `'members'` to `AUTHENTICATED_PATHS`:

```ts
const AUTHENTICATED_PATHS = [
	'links',
	'links/new',
	'domains',
	'stats',
	'stats-data',
	'stats-disabled',
	'audit-log',
	'members',
] as const;
```

No seeding branch is needed for it — the crawling account is itself in the list, so the page always has a row. If the crawl reports a string identical across languages, it is a real finding: check it against Task 3's catalogues rather than adding an exclusion.

- [ ] **Step 3: Run the specs**

E2E runs against a Vercel preview, not a local dev server. If running locally, build first — the Vite dev server injects an overlay that breaks these flows.

Run: `pnpm --filter=web exec playwright test e2e/members.spec.ts e2e/i18n.spec.ts` Expected: PASS. If the members crawl fails on a missing translation, fix the catalogue, not the test.

- [ ] **Step 4: Run the gate and commit**

Run: `pnpm format && pnpm lint && pnpm typecheck`

Commit with `create-commit`, subject `test(web): cover the members page e2e`.

---

## Self-Review

**Spec coverage.** Walked each section of the spec against the tasks:

| Spec section | Task |
| --- | --- |
| API addition 1 (`invited`) | 1 |
| API addition 2 (typed 429s) | 1 |
| What stays untyped | 7 (`classifyMutationFailure` → `raced`) |
| Authorization and visibility | 2 (rules), 6 (controls), 7 (sidebar, ungated) |
| Data source, no pagination | 7 (reuses `membersQueryOptions` untouched) |
| Empty addresses | 3 (`members.unknownAddress`), 6 (fallback + test) |
| Server functions | 4 |
| `member-invite-form.tsx` | 5 |
| `member-list.tsx` | 6 |
| Route, classifiers, error boundary | 7 |
| Failure surfaces table | 5 (invite arms), 6 (row arms), 7 (classifiers) |
| The `updateMember` no-op 204 | 7 (treated as success, refetch) |
| i18n, `identicalByDesign` | 3 |
| Accessibility | 5 and 6 (Storybook axe), 7 (page-level axe), 6 (`members.roleFor`) |
| Go tests | 1 |
| Unit tests | 2, 4, 5, 6, 7 |
| e2e, `seedSecondMember`, ordering rule | 8, 9 |

No spec requirement is unassigned.

**Placeholder scan.** No "TBD", no "handle errors appropriately", no "similar to Task N". Four steps deliberately say _read an existing file and match it_ rather than reproducing it — the MSW setup in Task 4, the i18n test wrapper in Task 5, `renderSidebar`'s signature in Task 7, and the axe harness in Task 7. Each names the exact file and what to take from it. That is not a placeholder: reproducing a harness this plan has not read would be the guess.

**Type consistency.** `TeamRole` is defined once (Task 2) and imported by 5, 6 and 7; the e2e file keeps its own unexported copy, which is deliberate — `e2e/` does not import from `src/`. `InviteFailureKind` is declared and exported by Task 5 and imported by Task 7, rather than declared twice. `MemberListProps.failure` is `'raced' | 'unknown' | null`, matching `classifyMutationFailure`'s return in Task 7. Server function parameter order is `(request, teamId, …)` throughout, matching the existing `listMembersFor`.

**One thing this plan cannot settle.** Task 7 has to read `_authed.tsx` to learn how the route context exposes the signed-in person's `user_id` and `memberships`. `app-sidebar.tsx` receives them as props from its caller rather than reading context itself, so the route may need to take the same path. The task says so and names the file; it is an unknown to look up, not a decision left open.
