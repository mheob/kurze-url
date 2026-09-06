import { expect, type Locator } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

/**
 * The half of the no-hardcoded-string rule that react/jsx-no-literals cannot
 * see. That rule reads JSX text children; a hardcoded aria-label or a string
 * built in a variable is invisible to it and still reaches a screen reader.
 *
 * A hardcoded string has no translation key, so catalogue parity cannot see it
 * either — but it cannot help failing this check, because it does not change
 * when the language does.
 */

/**
 * Strings legitimately identical in both languages. Adding one is deliberate
 * and reviewable. `TXT`/`CNAME` (`domain-list.tsx`'s record-type cells) join
 * `kurze.url` for the same reason: a DNS record type is a protocol literal,
 * not copy — nobody translates it, so it never changes with the language.
 */
const IDENTICAL_BY_DESIGN = new Set(['kurze.url', 'TXT', 'CNAME']);

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
	identicalByDesign: ReadonlySet<string> = IDENTICAL_BY_DESIGN,
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
			.filter((value) => !identicalByDesign.has(value))
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

/**
 * The screens people actually use, reached through the same `teamId` fixture
 * `links.spec.ts` uses — `test` above is `./fixtures/auth`'s extended one, a
 * superset of `@playwright/test`'s own, so the loop over the public `PATHS`
 * above never pays for provisioning a team it never asks for: a fixture only
 * runs for a test that destructures it.
 */
const AUTHENTICATED_PATHS = ['links', 'links/new', 'domains'] as const;

/**
 * What the `links` case below fills into the create form — known upfront,
 * unlike the short URL the API generates for it, which has to be read off
 * the page instead (see the comment at that read).
 */
const I18N_CRAWL_DESTINATION_URL = 'https://example.org/i18n-crawl';

/**
 * A DNS record's Value cell (`domain-list.tsx`) renders the raw value
 * immediately followed by a `CopyButton`, with no element between them, so
 * the cell's own `innerText` glues the value to that button's visible label
 * ("Copy"/"Kopieren") — which already differs by language on its own and
 * would mask the value underneath it. Reading only the cell's first child —
 * a plain text node, since the JSX puts the value before `CopyButton` — gets
 * the bare value instead, so it can be excluded below the same way the
 * hostname next to it is, regardless of whether that gluing keeps holding.
 */
async function directText(cell: Locator): Promise<string> {
	return cell.evaluate((node) => node.childNodes[0]?.textContent?.trim() ?? '');
}

for (const suffix of AUTHENTICATED_PATHS) {
	test(`no user-facing string is identical across languages (authenticated /${suffix})`, async ({
		page,
		baseURL,
		teamId,
		teamName,
	}) => {
		if (!baseURL) throw new Error('baseURL fixture is unset — check playwright.config.ts');

		// Populated only for `links` below, once that link's own destination and
		// short URL are known — see the long comment above `identicalByDesign`
		// for why these join `teamName` in the same exclusion Set.
		const linkStrings: string[] = [];
		// Same idea, populated only for `domains` below.
		const domainStrings: string[] = [];

		if (suffix === 'links') {
			// A freshly provisioned team starts with zero links, and `LinkList`'s
			// empty-state branch (src/components/link-list.tsx) never renders the
			// `.invalid`-domain notice, the per-link edit link, or the pagination
			// nav — all real, translated strings this crawl would otherwise miss.
			// Created once, before either language visits the page, so both passes
			// compare the same rendered list.
			await page.goto(`/teams/${teamId}/links/new`);
			await page.getByLabel(/destination/i).fill(I18N_CRAWL_DESTINATION_URL);
			await page.getByRole('button', { name: /save/i }).click();
			await expect(page.getByText(I18N_CRAWL_DESTINATION_URL)).toBeVisible();

			// `link-list.tsx` renders this same link's `short_url` as the visible
			// text of a plain `<a href>` — the one element on this page whose
			// `href` is an absolute http(s) URL; every other link here is a
			// TanStack Router `<Link>` to an app-relative path. The slug inside it
			// is generated server-side, so there is no formula to reconstruct it
			// from — reading it off the page it actually rendered is the only way
			// to get the exact value, and a locator that's supposed to match
			// exactly one element fails loudly rather than silently if that
			// assumption ever stops holding.
			const shortUrl = await page.locator('a[href^="http"]').innerText();
			linkStrings.push(I18N_CRAWL_DESTINATION_URL, shortUrl);
		}

		if (suffix === 'domains') {
			// `domain.hostname` is globally unique (a bare `unique` on the column,
			// not scoped by team), and this suite runs against a shared preview
			// database — a fixed literal like `I18N_CRAWL_DESTINATION_URL` above
			// would collide with a rerun of this same crawl, or with
			// `domains.spec.ts`'s own claims, against that same database.
			const hostname = `i18n-${Date.now()}.e2e.test`;

			await page.goto(`/teams/${teamId}/domains`);

			// Not decorative: this form is server-rendered too, and `goto` resolves
			// before React hydrates it — see `waitForHydration`.
			const hostnameField = page.getByLabel(/hostname/i);
			await waitForHydration(hostnameField);
			await hostnameField.fill(hostname);
			await page.getByRole('button', { name: /add domain/i }).click();

			// A level-2 heading, not a plain `getByText`: the hostname also
			// appears inside the TXT challenge name and the delete button below,
			// so a bare substring match would resolve to more than one element.
			await expect(page.getByRole('heading', { level: 2, name: hostname })).toBeVisible();

			// The TXT row renders before the CNAME row (`domain-list.tsx`'s own
			// JSX order); the Value column is the third cell in either row.
			const rows = page.locator('table tbody tr');
			const txtValue = await directText(rows.nth(0).locator('td').nth(2));
			const cnameValue = await directText(rows.nth(1).locator('td').nth(2));

			domainStrings.push(hostname, `_kurze-url-challenge.${hostname}`, txtValue, cnameValue);
		}

		// Every authenticated page renders `AuthedShell` -> `TeamSwitcher`, which
		// prints `membership.name` — this run's `teamName` fixture value — as
		// plain link text. That is user data, not UI copy: a real Verein's own
		// name would sit in that exact spot and would be exactly as identical
		// across languages, because nobody translates an association's name
		// (any more than they would translate "Bürgerinitiative Lindenstraße
		// e.V." into English for the English UI — it already is what it is,
		// regardless of language). The same is true of a link's destination and
		// short URL (`linkStrings`, above, populated only when `links` created
		// one): a real Verein's own link would render its own destination and
		// short URL in that exact spot, identically in both languages, for the
		// same reason — nobody translates a URL either. `domainStrings` (above,
		// populated only when `domains` claimed one) is the same story again: a
		// hostname, its TXT challenge name, and the raw values of the two DNS
		// records a Verein is told to create are all data a claiming team
		// supplied or that this instance generated, never copy. Allowing the
		// *literal* strings this run's own fixture, link creation, and domain
		// claim produced — reusing the module's own exclusion Set rather than a
		// second mechanism — has no blind spot: a pattern-based exclusion (a
		// UUID shape, an `e2e ` prefix, "anything that looks like a URL or
		// hostname") would just as happily swallow a real hardcoded string that
		// happened to sit next to one of these, which is exactly the false
		// negative this spec exists to prevent.
		const identicalByDesign = new Set([
			...IDENTICAL_BY_DESIGN,
			teamName,
			...linkStrings,
			...domainStrings,
		]);

		const path = `/teams/${teamId}/${suffix}`;
		const english = new Set(await visibleText(page, baseURL, 'en', path, identicalByDesign));
		const german = await visibleText(page, baseURL, 'de', path, identicalByDesign);

		const untranslated = german.filter((value) => english.has(value));

		expect(
			untranslated,
			`these strings did not change with the language: ${untranslated.join(', ')}`,
		).toEqual([]);
	});
}
