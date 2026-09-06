import { request } from '@playwright/test';

/**
 * Refuses to run the suite against a preview whose paired API preview is not
 * an API.
 *
 * `apps/api` and `apps/web` are two Vercel projects deploying from one repo,
 * and the API's `ignoreCommand` can skip its build. When it does, Vercel keeps
 * serving the branch alias — with a "Deployment was cancelled" page, at
 * **HTTP 200**. Nothing about that looks like an outage: `withRelatedProject`
 * hands the web preview that alias, `getMe` parses the HTML as JSON, the
 * memberships come back empty, and `assertMembership` throws `notFound()`. The
 * suite then fails four specs deep inside `waitForHydration`, on a page that
 * says "Page not found", which reads as a frontend bug and is not one.
 *
 * That happened on 2026-09-06: a commit touching only `CLAUDE.md` matched
 * nothing in the API's pathspec, so the API build was skipped while the web
 * build was not.
 *
 * A positive match on 'ok', not a search for the failure values: an unknown
 * state has to fail closed. If the footer stops rendering the attribute
 * altogether, this must stop the run rather than wave it through — the same
 * reasoning `ci-js.yml`'s deployment filter is built on.
 *
 * Skipped without `BASE_URL`, i.e. locally: there is no related-projects
 * lookup and no branch alias in a local run, so the failure mode does not
 * exist there, and `playwright.config.ts` starts its own server instead.
 */
export default async function assertPairedApiIsReal(): Promise<void> {
	const baseURL = process.env.BASE_URL;
	if (!baseURL) return;

	// Same header the specs themselves send (see `playwright.config.ts`): a
	// protected preview would otherwise answer with Vercel's login page, and
	// this check would report a missing attribute for the wrong reason.
	const bypassSecret = process.env.VERCEL_AUTOMATION_BYPASS_SECRET;
	const context = await request.newContext({
		baseURL,
		...(bypassSecret && { extraHTTPHeaders: { 'x-vercel-protection-bypass': bypassSecret } }),
	});

	try {
		const response = await context.get('/');
		const apiStatus = /data-api-status="([^"]*)"/.exec(await response.text())?.[1];

		if (apiStatus !== 'ok') {
			throw new Error(
				`${baseURL} reports its API as ${apiStatus ?? 'not rendered at all'}, so the authenticated ` +
					'specs would fail against a 404 page rather than against this deployment. The usual cause ' +
					"is a skipped kurze-url-api preview build: check the pull request's " +
					'"Vercel – kurze-url-api" check for "Canceled by Ignored Build Step".',
			);
		}
	} finally {
		await context.dispose();
	}
}
