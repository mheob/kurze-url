// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, test } from '@playwright/test';

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
	await page.getByRole('button', { name: /dark mode|dunklen Modus/ }).click();
	await expect(page.locator('html')).toHaveClass(/dark/);

	await page.reload();
	await expect(page.locator('html')).toHaveClass(/dark/);
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
			if (!baseURL) throw new Error('baseURL fixture is unset — check playwright.config.ts');

			await context.addCookies([
				{ name: 'theme', value: theme, url: baseURL },
				{ name: 'lang', value: language, url: baseURL },
			]);
			await page.goto('/');

			const results = await new AxeBuilder({ page })
				.withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
				.analyze();

			expect(results.violations).toEqual([]);
		});
	}
}
