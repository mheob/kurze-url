import { expect, test } from '@playwright/test';

/**
 * The half of the no-hardcoded-string rule that react/jsx-no-literals cannot
 * see. That rule reads JSX text children; a hardcoded aria-label or a string
 * built in a variable is invisible to it and still reaches a screen reader.
 *
 * A hardcoded string has no translation key, so catalogue parity cannot see it
 * either — but it cannot help failing this check, because it does not change
 * when the language does.
 */

/** Strings legitimately identical in both languages. Adding one is deliberate and reviewable. */
const IDENTICAL_BY_DESIGN = new Set(['kurze.url']);

/**
 * `/` is a real route with real content; the 404 page is a separate render
 * path entirely — TanStack Router's own default `notFoundComponent` (a
 * hardcoded English literal) would pass every other check here (catalogue
 * parity has no key for it, `react/jsx-no-literals` cannot see a dependency's
 * output, axe finds a title and adequate contrast), so it needs its own visit
 * rather than being assumed to divergence-check for free just because `/` does.
 */
const PATHS = ['/', '/this-page-does-not-exist'] as const;

async function visibleText(
	page: import('@playwright/test').Page,
	baseURL: string,
	language: string,
	path: string,
): Promise<string[]> {
	// Playwright derives a cookie's domain from `url`, not from wherever
	// `page.goto` later navigates — it has to be the fixture's `baseURL`, the
	// same host the test actually runs against, or the cookie is scoped to
	// whatever host `url` names (e.g. `localhost`) and never sent to a CI
	// preview host.
	await page.context().addCookies([{ name: 'lang', value: language, url: baseURL }]);
	await page.goto(path);

	const texts = await page.locator('body :visible').allInnerTexts();
	const labels = await page
		.locator('[aria-label]')
		.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('aria-label') ?? ''));
	// `body :visible` and `[aria-label]` both scope to <body> and never see
	// <head>, so <title> needs its own read — `page.title()` is the direct API
	// for it, not a locator workaround.
	const title = await page.title();

	return (
		[...texts, ...labels, title]
			// `body :visible` also matches the theme toggle's inline SVG icon (and its
			// child <path>/<circle>/<line> nodes) — SVGElement has no `innerText`, so
			// Playwright reports `null` for it rather than `''`. Nullish-coalescing
			// before the split keeps that from throwing; the empty string it produces
			// is filtered out below like any other content-free node.
			.flatMap((value) => (value ?? '').split('\n'))
			.map((value) => value.trim())
			.filter((value) => value.length > 0 && !/^[\d\s\p{P}]+$/u.test(value))
			.filter((value) => !IDENTICAL_BY_DESIGN.has(value))
	);
}

for (const path of PATHS) {
	test(`no user-facing string is identical across languages (${path})`, async ({
		page,
		baseURL,
	}) => {
		// playwright.config.ts always sets `use.baseURL` (to BASE_URL or the
		// localhost fallback), so this is only ever undefined if that invariant is
		// broken — worth a loud failure rather than silently falling back to a
		// wrong host.
		if (!baseURL) throw new Error('baseURL fixture is unset — check playwright.config.ts');

		const english = new Set(await visibleText(page, baseURL, 'en', path));
		const german = await visibleText(page, baseURL, 'de', path);

		const untranslated = german.filter((value) => english.has(value));

		expect(
			untranslated,
			`these strings did not change with the language: ${untranslated.join(', ')}`,
		).toEqual([]);
	});
}
