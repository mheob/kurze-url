import { listAuditLog, type PageAuditEntry } from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import type { AuditFilters } from '../lib/audit-filters';
import { toQueryRange } from '../lib/audit-filters';
import { authedApiClient, flushSessionCookies, requireSession } from './session';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- the only finding of this rule in
 * this file is the `request: Request` parameter `listAuditLogFor` takes below: `Request` nests a
 * mutable `Headers` through its own `.headers` getter, and `Readonly<>` is shallow — it does not
 * reach that nested property, unlike a bare `Headers` parameter, which the check does accept once
 * wrapped. Same finding, same reason, as the top of `links.ts`.
 */

/**
 * The page size every paginated list in this app uses. Exported because the
 * page's own pagination arithmetic needs it: `PageAuditEntry` carries
 * `page` and `total_count`, and without the size of a page those two cannot
 * say whether another page exists. One definition, so the request and the
 * "next page" link can never disagree about how many entries a page holds.
 */
export const AUDIT_LOG_PER_PAGE = 20;

/**
 * Takes `request` as a parameter rather than calling `getRequest()` itself,
 * the same shape `listLinksFor` uses in `server/links.ts`: that is what lets
 * `audit-log.test.ts` call this directly with a synthetic `Request`, instead
 * of needing the server's per-request `AsyncLocalStorage` context that only
 * exists inside a real request (`getRequest()` throws "No Start context
 * found" outside of one, which is exactly what running this under Vitest
 * is).
 *
 * `requireSession` reading the session is itself what refreshes an expiring
 * one, writing new cookies into the `Headers` object threaded through here.
 * `flushSessionCookies` is what carries those onto the real response, the
 * same reasoning `listLinksFor` documents for itself.
 *
 * Wrapped in `createServerOnlyFn` for the same reason `listLinksFor` is: a
 * separately exported, by-name-referenced helper (from `listAuditLogFn`'s
 * handler below) that calls `flushSessionCookies`, which itself reaches
 * `getResponse` from `@tanstack/react-start/server`.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param teamId - The team whose audit log to read.
 * @param filters - The active filters, including the page.
 * @returns The requested page of the team's audit log.
 */
export const listAuditLogFor = createServerOnlyFn(
	async (request: Request, teamId: string, filters: AuditFilters): Promise<PageAuditEntry> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await listAuditLog({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			query: {
				...(filters.actor !== undefined && { actor_user_id: filters.actor }),
				...(filters.entityType !== undefined && { entity_type: filters.entityType }),
				...toQueryRange(filters),
				page: filters.page,
				per_page: AUDIT_LOG_PER_PAGE,
			},
			// throwOnError is required for the same reason `listLinksFor` gives:
			// the generated client's default (false) never rejects, so a refused
			// request would resolve to `{ data: undefined, error }` and this page
			// would render an empty log instead of the refusal it must show.
			throwOnError: true,
		});
		return data;
	},
);

/**
 * `getRequest()` (not a `request` field on the handler's context) reads the
 * incoming request from the server's per-request AsyncLocalStorage, the same
 * correction `listLinksFn` already applies. Called inline, here, rather than
 * from inside `listAuditLogFor`: that keeps `listAuditLogFor` callable with a
 * synthetic request in tests, the same split `listLinksFor`/`listLinksFn`
 * use.
 *
 * `strict: { output: false }` is new here, and not in `listLinksFn`:
 * `AuditEntry.metadata` (`packages/api-client`) is generated as `unknown`,
 * because the API's own schema for it is genuinely open — it is whatever an
 * `audit_log` row's JSON column held. `createServerFn`'s default strict
 * output check walks the response type recursively and refuses to consider
 * `unknown` serializable at any depth (`ValidateSerializableMapped` in
 * `@tanstack/router-core`'s `transformer.d.ts` has no case for it — it isn't
 * `T extends object`, since `unknown` also admits primitives), so
 * `PageAuditEntry` fails that check regardless of how this handler is
 * written. The payload is ordinary JSON that already round-tripped through
 * `fetch`, so it serializes across this same RPC boundary without issue at
 * runtime; only the static check is the obstacle. Turning off the output
 * half of the check (input validation is untouched) is TanStack Start's own
 * documented escape hatch for exactly this shape, not a workaround for a
 * rule that is merely inconvenient.
 */
export const listAuditLogFn = createServerFn({ method: 'GET', strict: { output: false } })
	.validator((data: { readonly filters: AuditFilters; readonly teamId: string }) => data)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly filters: AuditFilters; readonly teamId: string };
		}) => listAuditLogFor(getRequest(), data.teamId, data.filters),
	);

/**
 * One definition of the key and the fetcher, the same reason
 * `linksQueryOptions` gives: two definitions drift, and the symptom is a list
 * that updates on navigation but not after a filter changes. Every filter is
 * part of the key, so changing one is a different query rather than a stale
 * hit on the old one.
 *
 * @param teamId - The team whose log to read.
 * @param filters - The active filters, including the page.
 * @returns Query options keyed on the team and every filter.
 */
// oxlint-disable-next-line typescript/explicit-function-return-type, typescript/explicit-module-boundary-types -- same missing annotation as `linksQueryOptions`; see its own note in links.ts.
export const auditLogQueryOptions = (teamId: string, filters: AuditFilters) =>
	queryOptions({
		queryFn: async () => listAuditLogFn({ data: { filters, teamId } }),
		queryKey: ['audit-log', teamId, filters] as const,
	});
