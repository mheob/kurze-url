import { createTeam, type Team } from '@kurze-url/api-client';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { authedApiClient, flushSessionCookies, requireSession } from './session';

/**
 * Takes `request` as a parameter rather than calling `getRequest()` itself,
 * the same shape every function in `server/links.ts` uses: `getRequest()`
 * throws "No Start context found" outside a real request, which is exactly
 * what running this under Vitest is, so the testable half takes the request
 * in and `createTeamFn` below supplies it.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are both required
 * for the reasons documented on `listLinksFor`: reading the session through
 * `requireSession` is itself what refreshes an expiring one, and dropping
 * those cookies reproduces the "signed in, then silently signed out" failure
 * this app has already hit twice.
 */
export const createTeamFor = createServerOnlyFn(
	async (request: Request, name: string, slug: string): Promise<Team> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createTeam({
			body: { name, slug },
			client: authedApiClient(accessToken),
			// Required: the generated client's default never rejects, so the 403 a
			// non-maintainer gets would resolve to `{ data: undefined, error }` and
			// this would report a team that was never created. `classifyApiError`
			// is written against the thrown shape.
			throwOnError: true,
		});
		return data;
	},
);

export const createTeamFn = createServerFn({ method: 'POST' })
	.validator((data: { name: string; slug: string }) => data)
	.handler(async ({ data }) => createTeamFor(getRequest(), data.name, data.slug));
