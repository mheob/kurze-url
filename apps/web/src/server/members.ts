import { listTeamMembers, type PageMember } from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

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
