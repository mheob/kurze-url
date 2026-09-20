import {
	addTeamMember,
	listTeamMembers,
	removeTeamMember,
	updateTeamMember,
	type AddedMember,
	type PageMember,
} from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import type { TeamRole } from '../lib/team-roles';
import { authedApiClient, flushSessionCookies, requireSession } from './session';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- the only finding of this rule in
 * this file is the `request: Request` parameter `listMembersFor` takes below: `Request` nests a
 * mutable `Headers` through its own `.headers` getter, and `Readonly<>` is shallow — it does not
 * reach that nested property, unlike a bare `Headers` parameter, which the check does accept once
 * wrapped. Same finding, same reason, as the top of `links.ts`.
 */

/**
 * The API caps `per_page` at 100. Asking for the cap is deliberate: this list
 * exists to resolve actor ids, and an actor on page two would be rendered as a
 * former member — wrong, and wrong quietly. A team large enough to need paging
 * here needs a different approach, not a second request bolted on. Until one
 * exists, `listMembersFor` below at least refuses to let the case stay quiet:
 * it compares what came back against the envelope's own `total_count`.
 */
const PER_PAGE = 100;

/**
 * Same `...For` shape as `listAuditLogFor` in `server/audit-log.ts`, for the
 * same reason: taking `request` as a parameter, rather than calling
 * `getRequest()` internally, is what lets this be called directly with a
 * synthetic `Request` outside of a real one — `getRequest()` throws "No Start
 * context found" in that environment, which is exactly what running under
 * Vitest is.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are required for
 * the identical reason documented on `listAuditLogFor`/`listLinksFor`:
 * reading the session via `requireSession` is what refreshes an expiring
 * one, and skipping the flush would silently drop that refresh's cookies.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team whose members to list.
 * @returns The team's members, one page of up to 100.
 */
export const listMembersFor = createServerOnlyFn(
	async (request: Request, teamId: string): Promise<PageMember> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await listTeamMembers({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			query: { page: 1, per_page: PER_PAGE },
			// throwOnError is required for the same reason `listLinksFor` gives:
			// the generated client's default (false) never rejects, so a refused
			// request would resolve to `{ data: undefined, error }` instead of
			// throwing.
			throwOnError: true,
		});

		// The cap above is what the reader never sees. An actor whose
		// membership sits past it is simply missing from the map the audit log
		// builds, and `audit-entry-table.tsx` renders missing as "a former
		// member" — a confident sentence, in front of a board, about somebody
		// who is still in the Verein. Nothing about the page looks wrong, which
		// is the whole problem: this is the one condition here that produces a
		// plausible wrong answer rather than a visible failure, so it must at
		// least reach a log. Same channel and same reasoning as
		// `loadVerifiedDomains`'s swallowed error
		// (`routes/_authed/teams.$teamSlug.links.new.tsx`): a day of Vercel
		// runtime logs is not a durable record, but it is the difference
		// between a question somebody can answer and one nobody can.
		if ((data.items?.length ?? 0) < data.total_count) {
			console.error(
				`listMembersFor: team ${teamId} has ${data.total_count} members and this read caps at ${PER_PAGE}; audit-log actors past the cap render as former members`,
			);
		}

		return data;
	},
);

/**
 * `getRequest()` called inline here, not inside `listMembersFor`, for the
 * same reason `listAuditLogFn`/`listLinksFn` do it this way: it keeps
 * `listMembersFor` callable with a synthetic request in tests.
 */
export const listMembersFn = createServerFn({ method: 'GET' })
	.validator((data: { readonly teamId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly teamId: string } }) =>
		listMembersFor(getRequest(), data.teamId),
	);

/**
 * One definition of the key and the fetcher, the same reason
 * `linksQueryOptions` gives.
 *
 * @param teamId - The team whose members to list.
 * @returns Query options keyed on `['members', teamId]`.
 */
// oxlint-disable-next-line typescript/explicit-function-return-type, typescript/explicit-module-boundary-types -- same missing annotation as `linksQueryOptions`; see its own note in links.ts.
export const membersQueryOptions = (teamId: string) =>
	queryOptions({
		queryFn: async () => listMembersFn({ data: { teamId } }),
		queryKey: ['members', teamId] as const,
	});

/**
 * Same `...For`/`...Fn` split as `listMembersFor` above, and the same reason
 * for it: `getRequest()` throws "No Start context found" outside a real
 * request, which is exactly what Vitest is.
 *
 * Returns the whole `AddedMember` rather than just the membership, because
 * `invited` is the only thing that distinguishes an invitation on its way
 * from a person who was added silently and will not be told.
 *
 * `body` is bundled into one object, mirroring `createLinkFor`'s own
 * `body: CreateLinkInputBodyWritable` parameter, rather than passed as two
 * positional `email`/`role` parameters: `eslint(max-params)` caps at three
 * and this already has four independent things to name (`request`, `teamId`,
 * `email`, `role`), the same reasoning `loadStatsPage`'s docstring
 * (`routes/_authed/teams.$teamSlug.links.$linkId_.stats.tsx`) and
 * `completeQrDownload`'s give for their own bundling.
 *
 * `body.role` is typed as `TeamRole` (`lib/team-roles.ts`), not `string`: the
 * generated `AddMemberInputBodyWritable.role` is the literal union
 * `'viewer' | 'editor' | 'admin' | 'owner'`, and `TeamRole` is the same four
 * values under the same name this feature already gave them — widening it to
 * `string` here would fail to compile against `addTeamMember`'s body, for the
 * identical reason `verifyDomainFor`'s docstring gives for not widening
 * `reason`.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team to add the person to.
 * @param body - The address to invite or add, and the role to grant.
 * @returns The new membership, and whether an email was sent.
 */
export const addMemberFor = createServerOnlyFn(
	async (
		request: Request,
		teamId: string,
		body: { readonly email: string; readonly role: TeamRole },
	): Promise<AddedMember> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await addTeamMember({
			body,
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
		(data: { readonly email: string; readonly role: TeamRole; readonly teamId: string }) => data,
	)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly email: string; readonly role: TeamRole; readonly teamId: string };
		}) => addMemberFor(getRequest(), data.teamId, { email: data.email, role: data.role }),
	);

/**
 * Returns `void`: the endpoint answers 204 with no body, and the page
 * refetches rather than reading a response — same shape as `deleteDomainFor`.
 *
 * `userId` and `role` are bundled into `change`, for the same
 * `eslint(max-params)` reason `addMemberFor` gives above: `teamId` is kept
 * positional, matching `removeMemberFor`'s own second parameter below, and
 * `change` groups the member being changed with what it changes to.
 *
 * `change.role` is `TeamRole`, not `string`, for the same reason given on
 * `addMemberFor`: `UpdateMemberInputBodyWritable.role` is the same literal
 * union.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team the membership belongs to.
 * @param change - The member whose role changes, and the role to grant.
 * @returns Nothing; it resolves when the change is stored.
 */
export const updateMemberRoleFor = createServerOnlyFn(
	async (
		request: Request,
		teamId: string,
		change: { readonly role: TeamRole; readonly userId: string },
	): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await updateTeamMember({
			body: { role: change.role },
			client: authedApiClient(accessToken),
			path: { team_id: teamId, user_id: change.userId },
			throwOnError: true,
		});
	},
);

export const updateMemberRoleFn = createServerFn({ method: 'POST' })
	.validator(
		(data: { readonly role: TeamRole; readonly teamId: string; readonly userId: string }) => data,
	)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly role: TeamRole; readonly teamId: string; readonly userId: string };
		}) => updateMemberRoleFor(getRequest(), data.teamId, { role: data.role, userId: data.userId }),
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
