// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, test } from '@playwright/test';

import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own fixture argument object, which nests `Page`/`BrowserContext`; both have
 * many mutating methods (`goto`, `click`, `addCookies`, ...) and neither type is ours to edit.
 */

const THEMES = ['light', 'dark'] as const;
const LANGUAGES = ['en', 'de'] as const;

test('renders server-side', async ({ page }) => {
	const response = await page.goto('/');
	const html = await response?.text();

	// Asserting on the raw response, not the hydrated DOM: this is what proves
	// the content is server-rendered rather than fetched after load.
	expect(html).toContain('<h1');
});

test('keeps the theme across a reload', async ({ page }) => {
	await page.goto('/');

	// Same hazard `createLink` hit in `links.spec.ts`: this page is
	// server-rendered, so the toggle is clickable before React attaches a
	// handler to it, and a click in that window does nothing at all. See
	// `waitForHydration`.
	const toggle = page.getByRole('button', { name: /dark mode|dunklen Modus/u });
	await waitForHydration(toggle);
	await toggle.click();
	await expect(page.locator('html')).toHaveClass(/dark/u);

	await page.reload();
	await expect(page.locator('html')).toHaveClass(/dark/u);
});

for (const theme of THEMES) {
	for (const language of LANGUAGES) {
		test(`has no accessibility violations in ${theme} ${language}`, async ({
			page,
			context,
			baseURL,
		}) => {
			// Cookie domain comes from `url`, not from the later `page.goto` target —
			// it must be the fixture's `baseURL` (the host these tests actually run
			// against), never a hardcoded `localhost`, or the cookie is scoped to the
			// wrong host and never sent on CI's `*.vercel.app` preview.
			if (baseURL === undefined)
				throw new Error('baseURL fixture is unset — check playwright.config.ts');

			await context.addCookies([
				{ name: 'theme', url: baseURL, value: theme },
				{ name: 'lang', url: baseURL, value: language },
			]);
			await page.goto('/');

			const results = await new AxeBuilder({ page })
				.withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
				.analyze();

			expect(results.violations).toEqual([]);
		});
	}
}

/*
 * Chunk URLs are content-hashed, so a chunk that is served may be cached as
 * immutable for a year, and a chunk that is missing must never be. A browser
 * holding HTML from an older deployment, or from the one a rollback promotes
 * again, asks for chunks this deployment does not have; an immutable 404 would
 * keep that page broken for a year. `vite.config.ts` makes `/assets` a Nitro
 * public-asset base that does not fall through, which is what tells the two
 * apart. On a Vercel preview the CDN answers the miss before the function is
 * reached (`no-store`); locally the Nitro runtime answers it, with no cache
 * header at all.
 */
test('never caches a missing chunk as immutable', async ({ request }) => {
	const response = await request.get(`/assets/does-not-exist-${crypto.randomUUID()}.js`);

	expect(response.status()).toBe(404);
	expect(response.headers()['cache-control'] ?? '').not.toContain('immutable');
});

test('serves a real chunk as immutable, and not its source map', async ({ request }) => {
	// A chunk this deployment really has: the login page references its own.
	const login = await request.get('/login');
	const html = await login.text();
	const chunk = /\/assets\/[\w-]+\.js/u.exec(html)?.[0];
	if (chunk === undefined) throw new Error('/login references no /assets/*.js chunk');

	const response = await request.get(chunk);
	expect(response.status()).toBe(200);
	expect(response.headers()['cache-control']).toContain('immutable');

	// Sentry uploads the client maps and deletes them before deployment, so the
	// map is a missing file like any other: a 404, not cached as immutable.
	const map = await request.get(`${chunk}.map`);
	expect(map.status()).toBe(404);
	expect(map.headers()['cache-control'] ?? '').not.toContain('immutable');
	expect(await map.text()).not.toContain('"mappings"');
});
