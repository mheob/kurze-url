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

async function visibleText(
	page: import('@playwright/test').Page,
	language: string,
): Promise<string[]> {
	await page
		.context()
		.addCookies([{ name: 'lang', value: language, url: 'http://localhost:3000' }]);
	await page.goto('/');

	const texts = await page.locator('body :visible').allInnerTexts();
	const labels = await page
		.locator('[aria-label]')
		.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('aria-label') ?? ''));

	return (
		[...texts, ...labels]
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

test('no user-facing string is identical across languages', async ({ page }) => {
	const english = new Set(await visibleText(page, 'en'));
	const german = await visibleText(page, 'de');

	const untranslated = german.filter((value) => english.has(value));

	expect(
		untranslated,
		`these strings did not change with the language: ${untranslated.join(', ')}`,
	).toEqual([]);
});
