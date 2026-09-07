import {
	createDomain,
	deleteDomain,
	listDomains,
	verifyDomain,
	type Domain,
	type PageDomain,
	type VerifyDomainOutputBody,
} from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { authedApiClient, flushSessionCookies, requireSession } from './session';

/**
 * Same `...For`/`...Fn` split as `server/links.ts`, for the same reason:
 * `listDomainsFn`'s `createServerFn` can't be called directly under Vitest
 * ("No Start context found"), so the testable half takes `request: Request`
 * as a plain parameter instead of reaching for `getRequest()` itself.
 * `domains.test.ts` exercises this function directly.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are required for
 * the same reason documented on `listLinksFor`: reading the session via
 * `requireSession` is what refreshes an expiring one, and skipping the flush
 * would silently drop that refresh's cookies on every domain list fetch.
 *
 * No `page` parameter, unlike `listLinksFor`: a team's custom domains are a
 * short, admin-curated list, not a click-through list a visitor pages
 * through, so this always asks for the first page and lets the API's own
 * default (`per_page: 25`, `apps/api/internal/api/page.go`) apply. Screens
 * built on top (Tasks 13 and 14) render the whole result, not a pager.
 */
export const listDomainsFor = createServerOnlyFn(
	async (request: Request, teamId: string): Promise<PageDomain> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await listDomains({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// throwOnError is required: the generated client's default (false)
			// never rejects, so a failed request would resolve to
			// `{ data: undefined, error }` instead of throwing — silently
			// rendering an empty list rather than the loud failure this list is
			// deliberately built to show.
			throwOnError: true,
		});
		return data;
	},
);

/** `getRequest()` inline, not inside `listDomainsFor`, for the same reason as `listLinksFn`. */
export const listDomainsFn = createServerFn({ method: 'GET' })
	.validator((data: { teamId: string }) => data)
	.handler(async ({ data }) => listDomainsFor(getRequest(), data.teamId));

/**
 * One definition of the key and the fetcher, used by both a route's loader
 * (`ensureQueryData`) and its component (`useSuspenseQuery`), the same reason
 * `linksQueryOptions` exists. Two definitions drift, and the symptom is a
 * domain list that updates on navigation but not after a claim or delete.
 */
// oxlint's typescript(explicit-function-return-type) is error-level, but
// `queryOptions`'s own return type can't be written out by hand without
// losing the specific `['domains', teamId]` tuple type `useSuspenseQuery`
// needs downstream — same reasoning as `linksQueryOptions`, confirmed by
// trying it there.
// oxlint-disable-next-line typescript/explicit-function-return-type
export const domainsQueryOptions = (teamId: string) =>
	queryOptions({
		queryFn: () => listDomainsFn({ data: { teamId } }),
		queryKey: ['domains', teamId] as const,
	});

/**
 * Same `...For`/`...Fn` split, same reason as `createLinkFor`. Named "claim",
 * not "create", to match what this actually does from the caller's point of
 * view — the domain already exists as DNS, this is the team asserting it.
 */
export const claimDomainFor = createServerOnlyFn(
	async (request: Request, teamId: string, hostname: string): Promise<Domain> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createDomain({
			body: { hostname },
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// Required for the same reason as `createLinkFor`: the generated
			// client's default (false) never rejects, so a 422 for an apex
			// hostname would resolve to `{ data: undefined, error }` instead of
			// throwing — silently reporting a claim that was never created.
			throwOnError: true,
		});
		return data;
	},
);

export const claimDomainFn = createServerFn({ method: 'POST' })
	.validator((data: { hostname: string; teamId: string }) => data)
	.handler(async ({ data }) => claimDomainFor(getRequest(), data.teamId, data.hostname));

/**
 * Same `...For`/`...Fn` split. Returns the full `VerifyDomainOutputBody`,
 * `reason` included, rather than just the `Domain`: Task 13's screen needs to
 * tell the caller *why* verification failed, not only that it did, and
 * `reason` is typed as the union `'token_missing' | 'token_mismatch' |
 * 'unreachable'` — narrowed all the way from the generated client — so a
 * later exhaustive `switch` there stays exhaustive. Widening it to `string`
 * anywhere on the way through here would defeat that.
 *
 * Not scoped by `team_id` here, deliberately, for the same reason as
 * `getLinkFor`: the API's own entity-scoped authorization
 * (`internal/authz`) is what decides whether this caller may act on this
 * domain at all, answering with 404 for a non-member. Re-deriving that check
 * here would be a second, divergent copy of a decision the API already makes.
 */
export const verifyDomainFor = createServerOnlyFn(
	async (request: Request, domainId: string): Promise<VerifyDomainOutputBody> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await verifyDomain({
			client: authedApiClient(accessToken),
			path: { domain_id: domainId },
			throwOnError: true,
		});
		return data;
	},
);

export const verifyDomainFn = createServerFn({ method: 'POST' })
	.validator((data: { domainId: string }) => data)
	.handler(async ({ data }) => verifyDomainFor(getRequest(), data.domainId));

/**
 * Same `...For`/`...Fn` split. Returns `void`, not the domain: nothing
 * downstream reads a return value — the screen's mutation only cares whether
 * the promise resolved or rejected — same reasoning as `deleteLinkFor`.
 */
export const deleteDomainFor = createServerOnlyFn(
	async (request: Request, domainId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await deleteDomain({
			client: authedApiClient(accessToken),
			path: { domain_id: domainId },
			throwOnError: true,
		});
	},
);

export const deleteDomainFn = createServerFn({ method: 'POST' })
	.validator((data: { domainId: string }) => data)
	.handler(async ({ data }) => deleteDomainFor(getRequest(), data.domainId));
