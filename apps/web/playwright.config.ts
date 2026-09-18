import { defineConfig } from '@playwright/test';

/**
 * BASE_URL is set by CI to the pull request's Vercel preview, per planning doc
 * 07. Locally it falls back to a build served on 3000, so the same specs run
 * in both places without a second configuration.
 */
const baseURL = process.env.BASE_URL ?? 'http://localhost:3000';

/**
 * Preview deployments sit behind Vercel Authentication, which answers an
 * unauthenticated request with a 302 to vercel.com/sso-api rather than an
 * error. Playwright follows it, so without a bypass the whole suite runs
 * against Vercel's login page: axe finds that page accessible, and the i18n
 * comparison reports its chrome ("Continue with Google", "Login – Vercel") as
 * untranslated. Every assertion is then about the wrong document.
 *
 * The secret comes from the project's Protection Bypass for Automation.
 * Playwright applies extraHTTPHeaders to every request a context makes,
 * navigations and subresources alike, so the header alone is enough --
 * x-vercel-set-bypass-cookie only adds a redirect hop to set a cookie for
 * requests that would already carry the header.
 */
const bypassSecret = process.env.VERCEL_AUTOMATION_BYPASS_SECRET;

// Fail loudly rather than testing the login page. A protected preview without
// the secret is precisely the case that produces confident, meaningless
// passes, so it must not be reachable by forgetting an environment variable.
if (
	new URL(baseURL).hostname.endsWith('.vercel.app') &&
	(bypassSecret === undefined || bypassSecret === '')
) {
	throw new Error(
		`BASE_URL points at a Vercel deployment (${baseURL}) but VERCEL_AUTOMATION_BYPASS_SECRET is unset. ` +
			'Deployment Protection would redirect every request to the Vercel login page and the suite would ' +
			'assert against that instead of the app.',
	);
}

export default defineConfig({
	/*
	 * Playwright's own defaults are five seconds per expectation and thirty per
	 * test, which are sized for an app answering from localhost. This suite runs
	 * against a Vercel preview, where every server function is cold by
	 * construction: nothing has warmed it, and a PR's preview is discarded
	 * before it ever could be.
	 *
	 * Measured, from the trace of the run that made this necessary (i18n's
	 * `/stats` crawl, 2026-09-18): the create POST answered in 246 ms, but the
	 * server function the list page then re-fetched took 4.5 seconds. Click to
	 * rendered list was about 5.1 seconds, and the assertion waiting on it gave
	 * up at 5.0. Nothing was wrong with the app or the test — the budget was
	 * simply smaller than one cold round trip plus change.
	 *
	 * Fifteen seconds is roughly three of those worst-case round trips, which
	 * covers a page that chains two of them. Ninety for the whole test follows
	 * from it: several assertions may each wait, and a per-expectation timeout
	 * the test's own budget cannot reach never fires — the test dies first, on a
	 * timeout that was never spent.
	 *
	 * The cost is paid only by failures: an assertion that passes returns as
	 * soon as it is true, so a green run takes exactly as long as before. A red
	 * one takes longer to go red, and the trace is what explains it either way.
	 */
	expect: { timeout: 15_000 },
	// Runs before any spec and stops the suite when the deployment's paired API
	// preview was never built — see the file's own docstring for why that state
	// is invisible from inside a test.
	globalSetup: './e2e/global-setup.ts',
	testDir: './e2e',
	timeout: 90_000,
	use: {
		baseURL,
		// Only on failure, and only kept for one: a passing run writes nothing,
		// so this costs nothing until something breaks. The suite runs against a
		// live preview deployment where a failure can come from the deployment
		// rather than the code, and the console output alone ("element(s) not
		// found") never distinguishes the two — the trace and the screenshot of
		// what was actually on screen do. CI uploads `test-results/` on failure.
		screenshot: 'only-on-failure',
		trace: 'retain-on-failure',
		...(bypassSecret !== undefined &&
			bypassSecret !== '' && {
				extraHTTPHeaders: { 'x-vercel-protection-bypass': bypassSecret },
			}),
	},
	webServer:
		process.env.BASE_URL !== undefined && process.env.BASE_URL !== ''
			? undefined
			: { command: 'pnpm build && pnpm start', port: 3000, reuseExistingServer: true },
});
