import { expect, type Locator, type Page } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';
import { waitForHydration } from './fixtures/hydration';
import { linkIdForTeam, seedLinkClicks } from './fixtures/seed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is one of two things this side of the codebase cannot change: Playwright's own `Page`
 * (bare, or nested inside the fixture argument object each `test` callback destructures — it has
 * many mutating methods, `goto`/`fill`/`click` among them), or the `node`/`nodes` parameter of a
 * `.evaluate`/`.evaluateAll` callback, which is the real, live, mutable DOM running inside the
 * browser, not a value this file constructs or owns.
 */

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
 *
 * `Bot`/`QR` (`stats.dimensionValueBot`/`stats.dimensionValueQr`, rendered by
 * `StatSummary`'s binary splits) and `BROWSER` (`stats.browser`, a breakdown
 * card's title) join them for the same reason `catalogues.test.ts` already
 * allowlists those three keys: "Bot" and "Browser" are the established German
 * words too and "QR" is the initialism in both languages, none of them prose
 * to translate. All three render only on the statistics page with data, which
 * the `stats-data` case below reaches — without these entries that case would
 * report three failures that look exactly like missing translations.
 *
 * `BROWSER` is shouted because `ui/card.tsx`'s `CardTitle` carries `uppercase`,
 * and this crawl reads `innerText`, which is the text as rendered rather than
 * as written. The catalogue's own value is "Browser"; it never reaches a
 * screen in that shape, so the allowlist matches what a reader would see.
 *
 * `Domain` and `Link` join them for the audit log's own filter bar:
 * `audit-filter-bar.tsx`'s entity-type `<select>` lists all six
 * `AUDIT_ENTITY_TYPES` as options, and the German catalogue's own
 * `audit.entityDomain`/`audit.entityLink` values are "Domain"/"Link" too — the
 * established German words, exactly like `Bot`/`Browser` above, not a missed
 * translation.
 *
 * `PERSON` is the audit log's actor filter label (`audit.filterActor`,
 * "Person" in both catalogues — another established shared word) shouted for
 * the same reason `BROWSER` is: `FieldLabel` (`ui/field.tsx`) renders through
 * `Label` (`ui/label.tsx`), whose own base class carries `uppercase`, and this
 * crawl reads the rendered text, not the catalogue's stored value.
 *
 * `Admin` joins them for the members page's own role `<select>`s:
 * `MemberInviteForm`'s role picker and `MemberList`'s per-row role select both
 * list all four `TEAM_ROLES` as options — including the row select on the
 * team's sole owner, which `MemberList` renders disabled rather than omitted —
 * and the German catalogue's own `members.roleAdmin` value is "Admin" too, the
 * established German word, exactly like `Bot`/`Browser` above and not a
 * missed translation. `catalogues.test.ts` allowlists the same
 * `members.roleAdmin` key for the same reason, so the two lists agree. Unlike
 * `BROWSER`/`PERSON` above, it is not shouted: an `<option>` carries none of
 * `CardTitle`/`Label`'s `uppercase` styling, so the rendered text is plain
 * "Admin", the same case the catalogue itself stores it in. The other three
 * roles (`roleViewer`/`roleEditor`/`roleOwner`) are translated and do not
 * collide — this is the members page's only one.
 *
 * `destination_url`, `hostname`, `redirect_type` and `slug` are
 * `audit-entry-table.tsx`'s `MetadataList` own `<dt>` keys — the exact four
 * `createLink` writes into a `link.created` row's metadata
 * (`apps/api/internal/api/links.go:551-556`), and the only ones the
 * `audit-log` case below ever discloses. A metadata key renders exactly as
 * written, never through a translation lookup — the same reasoning as
 * `TXT`/`CNAME` above, protocol vocabulary rather than prose. No other
 * action's metadata keys join this set, because no other action's row is
 * ever disclosed here.
 *
 * A team's one member's email, and this entry's own `slug`/`hostname`/
 * `destination_url` *values* (as opposed to the fixed key literals just
 * above), are excluded separately, per test run, the same way `teamName` is
 * below: see the `audit-log` branch further down for why real per-run data
 * cannot live in this static set.
 */
const IDENTICAL_BY_DESIGN = new Set([
	'kurze.url',
	'TXT',
	'CNAME',
	'Bot',
	'BROWSER',
	'QR',
	'Domain',
	'Link',
	'PERSON',
	'Admin',
	'destination_url',
	'hostname',
	'redirect_type',
	'slug',
]);

/**
 * `/` is a real route with real content; the 404 page is a separate render
 * path entirely — TanStack Router's own default `notFoundComponent` (a
 * hardcoded English literal) would pass every other check here (catalogue
 * parity has no key for it, `react/jsx-no-literals` cannot see a dependency's
 * output, axe finds a title and adequate contrast), so it needs its own visit
 * rather than being assumed to divergence-check for free just because `/` does.
 */
const PATHS = ['/', '/this-page-does-not-exist'] as const;

/**
 * @param options - The page to crawl and the language/path to render it at.
 * @param options.page - The page to drive.
 * @param options.baseURL - The fixture's own base URL; a cookie's domain must come from here, not
 * from wherever `page.goto` later navigates.
 * @param options.language - The `lang` cookie value to set before navigating.
 * @param options.path - The path to visit.
 * @param options.identicalByDesign - Strings expected to render identically in both languages;
 * excluded from the returned crawl.
 * @param options.afterGoto - Run immediately after navigating, before the DOM is read. For state
 * that lives in a client-side `useState` rather than the server or the URL — the audit log's own
 * disclosure toggle, in particular — nothing else has a chance to reproduce it: a full `page.goto`
 * resets it, so it has to be redone after every navigation this function itself performs, not once
 * by the caller.
 * @returns Every visible string this render produced, one exclusion pass already applied.
 */
async function visibleText({
	page,
	baseURL,
	language,
	path,
	identicalByDesign = IDENTICAL_BY_DESIGN,
	afterGoto,
}: Readonly<{
	page: Page;
	baseURL: string;
	language: string;
	path: string;
	identicalByDesign?: ReadonlySet<string>;
	afterGoto?: (page: Page) => Promise<void>;
}>): Promise<string[]> {
	// Playwright derives a cookie's domain from `url`, not from wherever
	// `page.goto` later navigates — it has to be the fixture's `baseURL`, the
	// same host the test actually runs against, or the cookie is scoped to
	// whatever host `url` names (e.g. `localhost`) and never sent to a CI
	// preview host.
	await page.context().addCookies([{ name: 'lang', url: baseURL, value: language }]);
	await page.goto(path);
	if (afterGoto !== undefined) await afterGoto(page);

	const texts = await page.locator('body :visible').allInnerTexts();
	const labels = await page
		.locator('[aria-label]')
		.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('aria-label') ?? ''));
	// `body :visible` and `[aria-label]` both scope to <body> and never see
	// <head>, so <title> needs its own read — `page.title()` is the direct API
	// for it, not a locator workaround.
	const title = await page.title();

	// A line survives only if it says something this crawl has not already
	// approved word by word. Two things make that distinction necessary rather
	// than clever. `allInnerTexts()` reads every visible element, so a container
	// is collected along with its own children: `StatSummary`'s split renders
	// `<li><span>QR</span> <span>40%</span></li>`, and that `<li>` arrives as the
	// single line "QR 40%" beside the two parts it is made of. And a figure is
	// not prose — "40%" is the same in every language, which is why a
	// digits-and-punctuation line was already dropped before this existed.
	//
	// Dropping a line whose every word was individually excluded cannot hide a
	// missing translation: an untranslated string contributes at least one word
	// that is neither a figure nor an allowlist entry, and that word keeps its
	// line. This is the one generalisation the exclusion mechanism permits, for
	// exactly that reason — anything looser (a shape, a prefix, "looks like a
	// URL") would swallow real copy that merely sat next to approved text.
	const approved = (value: string): boolean =>
		identicalByDesign.has(value) || /^[\d\s\p{P}]+$/u.test(value);

	// The whole line first, then its words: an exclusion may itself contain
	// spaces — this run's team name is `e2e <uuid>` — and testing only the words
	// would drop it on the floor.
	const excluded = (value: string): boolean =>
		approved(value) || value.split(/\s+/u).every((word) => approved(word));

	return (
		[...texts, ...labels, title]
			// `body :visible` also matches the theme toggle's inline SVG icon (and its
			// child <path>/<circle>/<line> nodes) — SVGElement has no `innerText`, so
			// Playwright reports `null` for it rather than `''`. Nullish-coalescing
			// before the split keeps that from throwing; the empty string it produces
			// is filtered out below like any other content-free node.
			.flatMap((value) => (value ?? '').split('\n'))
			.map((value) => value.trim())
			.filter((value) => value.length > 0)
			.filter((value) => !excluded(value))
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
		if (baseURL === undefined)
			throw new Error('baseURL fixture is unset — check playwright.config.ts');

		const english = new Set(await visibleText({ baseURL, language: 'en', page, path }));
		const german = await visibleText({ baseURL, language: 'de', page, path });

		const untranslated = german.filter((value) => english.has(value));

		expect(
			untranslated,
			`these strings did not change with the language: ${untranslated.join(', ')}`,
		).toEqual([]);
	});
}

/**
 * The screens people actually use, reached through the same `teamSlug` fixture
 * `links.spec.ts` uses — `test` above is `./fixtures/auth`'s extended one, a
 * superset of `@playwright/test`'s own, so the loop over the public `PATHS`
 * above never pays for provisioning a team it never asks for: a fixture only
 * runs for a test that destructures it.
 */
const AUTHENTICATED_PATHS = [
	'links',
	'links/new',
	'domains',
	'stats',
	'stats-data',
	'stats-disabled',
	'audit-log',
	'members',
] as const;

/**
 * The statistics page renders one of three mutually exclusive views, and each
 * carries strings the other two never show — an empty window, a link whose
 * counting is switched off, and a page with figures on it. Crawling only one
 * of them would leave two thirds of that page's copy unchecked, so the last
 * three entries above are three states of one route rather than three paths.
 * `statsCase` below turns each of them into the link and the URL it needs.
 */

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
 *
 * @param cell - The table cell locator to read the leading text node from.
 * @returns The cell's own text, with the glued-on `CopyButton` label excluded.
 */
async function directText(cell: Readonly<Locator>): Promise<string> {
	return cell.evaluate((node) => node.childNodes[0]?.textContent?.trim() ?? '');
}

/**
 * Builds one of the statistics page's three states and reports what the crawl
 * needs to know about it.
 *
 * Every case creates its own link, because the page belongs to one and each
 * iteration of the loop below gets a freshly seeded team — there is nothing
 * left over from the `links` case to reuse, even within this file.
 * `stats-disabled` unchecks counting in the create form, which is what makes
 * its view the disabled one rather than the empty one; `stats-data` seeds
 * rollup rows straight into `link_click_stats`, because the redirect path
 * writes them asynchronously and finishes at no moment a test can wait for
 * (see `./fixtures/seed`).
 *
 * @param options - The pieces the case needs.
 * @param options.page - The page to drive; must already be authenticated.
 * @param options.suffix - Which of the three statistics cases to build.
 * @param options.teamId - The fixture team's id, used to look the new link's id up.
 * @param options.teamSlug - The fixture team's slug, used to build URLs.
 * @returns The page's path, its link's short URL, and every string on it that is data, not copy.
 */
async function statsCase({
	page,
	suffix,
	teamId,
	teamSlug,
}: Readonly<{
	page: Page;
	suffix: string;
	teamId: string;
	teamSlug: string;
}>): Promise<{ path: string; shortUrl: string; strings: string[] }> {
	const shortUrl = await createLink(page, teamSlug, {
		countClicks: suffix !== 'stats-disabled',
		destinationUrl: I18N_CRAWL_DESTINATION_URL,
	});
	const linkId = await linkIdForTeam(teamId);
	const strings = [shortUrl];

	if (suffix === 'stats-data') {
		const seeded = await seedLinkClicks(linkId);
		strings.push(...seeded.dimensionValues);
	}

	return { path: `/teams/${teamSlug}/links/${linkId}/stats`, shortUrl, strings };
}

for (const suffix of AUTHENTICATED_PATHS) {
	test(`no user-facing string is identical across languages (authenticated /${suffix})`, async ({
		page,
		baseURL,
		teamId,
		teamSlug,
		teamName,
	}) => {
		if (baseURL === undefined)
			throw new Error('baseURL fixture is unset — check playwright.config.ts');

		// Populated only for `links` below, once that link's own destination and
		// short URL are known — see the long comment above `identicalByDesign`
		// for why these join `teamName` in the same exclusion Set.
		const linkStrings: string[] = [];
		// Same idea, populated only for `domains` below.
		const domainStrings: string[] = [];
		// Same idea, populated only for `audit-log` below.
		const auditStrings: string[] = [];
		// Set only for `audit-log` below: re-opens that page's one disclosure
		// row after each language's own navigation, since that state lives in
		// client-side `useState` and a fresh `page.goto` resets it.
		let afterGoto: ((crawlPage: Page) => Promise<void>) | undefined = undefined;

		// Correct for every suffix except `stats`, which the `stats` branch
		// below overwrites: a statistics page nests under a real link id
		// (`/teams/$teamSlug/links/$linkId/stats`), and `AUTHENTICATED_PATHS`
		// itself carries no link id to interpolate.
		let path = `/teams/${teamSlug}/${suffix}`;

		if (suffix === 'links') {
			// A freshly provisioned team starts with zero links, and `LinkList`'s
			// empty-state branch (src/components/link-list.tsx) never renders the
			// `.invalid`-domain notice, the per-link edit link, or the pagination
			// nav — all real, translated strings this crawl would otherwise miss.
			// Created once, before either language visits the page, so both passes
			// compare the same rendered list.
			const shortUrl = await createLink(page, teamSlug, {
				destinationUrl: I18N_CRAWL_DESTINATION_URL,
			});
			linkStrings.push(I18N_CRAWL_DESTINATION_URL, shortUrl);
		}

		if (suffix.startsWith('stats')) {
			const stats = await statsCase({ page, suffix, teamId, teamSlug });
			path = stats.path;
			linkStrings.push(...stats.strings);

			// Guards the exact regression this branch exists to catch: this
			// route once silently rendered the link *edit* form instead of its
			// own statistics page — with build, lint, typecheck and every
			// existing test still green — because its filename made it a
			// nested child route of the link id rather than a sibling of it.
			// The edit form has no `<h1>` naming the link's own short URL, so a
			// re-nesting regression fails here, not just in this crawl below.
			await page.goto(path);
			await expect(page.getByRole('heading', { level: 1, name: stats.shortUrl })).toBeVisible();
		}

		if (suffix === 'domains') {
			// `domain.hostname` carries no unique constraint any more — a hostname
			// may be claimed by several teams at once (see the comment on
			// `claimDomain` in `domains.spec.ts`) — but a repeated one would still
			// break this crawl: below, a level-2 heading naming `hostname` is
			// asserted to resolve to exactly one element, and two domain rows
			// sharing the same hostname would turn that into a strict-mode
			// violation. This suite also runs against a shared preview database, so
			// a fixed literal like `I18N_CRAWL_DESTINATION_URL` above would collide
			// with a rerun of this same crawl, or with `domains.spec.ts`'s own
			// claims, against that same database.
			const hostname = `i18n-${Date.now()}.e2e.test`;

			await page.goto(`/teams/${teamSlug}/domains`);

			// Not decorative: this form is server-rendered too, and `goto` resolves
			// before React hydrates it — see `waitForHydration`.
			const hostnameField = page.getByLabel(/hostname/iu);
			await waitForHydration(hostnameField);
			await hostnameField.fill(hostname);
			await page.getByRole('button', { name: /add domain/iu }).click();

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

		if (suffix === 'audit-log') {
			// A fresh team has no history at all — no seeding fixture writes to
			// `audit_log`, and `team`/`team_member` themselves are inserted directly
			// over Postgres (`fixtures/auth.ts`), not through the audited API — so
			// without a link, this crawl would only ever reach `audit.emptyUnfiltered`.
			// `AuditEntryTable` itself is gated on `entries.length > 0`
			// (`AuditLogPageBody`), so an empty team never mounts it at all — the
			// four column headings and the entry's own action label, the largest
			// body of new copy this page adds, would go completely uncovered.
			// `createLink` writes a real `link.created` row, the same as the
			// `links` case above, so the table actually renders.
			const shortUrl = await createLink(page, teamSlug, {
				destinationUrl: I18N_CRAWL_DESTINATION_URL,
			});
			// `createLink`'s own `link.created` row carries exactly four metadata
			// keys (`apps/api/internal/api/links.go:551-556`) — the static
			// `destination_url`/`hostname`/`redirect_type`/`slug` entries above are
			// those keys; these are their *values* for this run. `redirect_type`'s
			// value is digits only, already caught by the digits-and-punctuation
			// rule above, but `slug` and `hostname` are letters (a generated slug
			// draws from `23456789abcdefghijkmnpqrstuvwxyz`, and the shared
			// hostname is `short.invalid` on Preview) and cannot be assumed away —
			// both are read back off the short URL itself, the one place this test
			// already has them, rather than guessed at.
			const shortUrlParts = new URL(shortUrl);
			auditStrings.push(
				I18N_CRAWL_DESTINATION_URL,
				shortUrlParts.host,
				shortUrlParts.pathname.slice(1),
			);

			await page.goto(path);

			// `AuditFilterBar`'s actor `<select>` always lists the fixture's own
			// one team member — the owner this test signed in as, and the same
			// person `createLink` just wrote the entry as. That email is real
			// per-run data, the same story as `teamName` above, and it cannot live
			// in the static `identicalByDesign` Set for the same reason `teamName`
			// doesn't — a fresh address every run. Read here, once, before either
			// language visits the page, so both passes compare the same rendered
			// option.
			const ownerEmail = await page
				.getByLabel(/^person$/iu)
				.locator('option[value]:not([value=""])')
				.first()
				// `innerText` is the rendered, visible text, which is what this crawl compares
				// against elsewhere (`visibleText`'s own `allInnerTexts`); `textContent` reads raw
				// text-node content instead — same note as `create-link.ts`'s identical disable.
				// oxlint-disable-next-line unicorn/prefer-dom-node-text-content
				.innerText();
			auditStrings.push(ownerEmail);

			// Opens the one entry's disclosure so its raw metadata keys/values and
			// the "Link created" action label are actually on screen — without
			// this, seeding the link above would render the table but never its
			// most detailed copy. Set as a callback rather than done once here:
			// `isOpen` lives in `AuditEntryTable`'s own `useState`, which a fresh
			// `page.goto` resets, so `visibleText` has to redo this after each of
			// its own two navigations, not just after this one. A structural
			// locator, not `getByRole('button', { name: /^Details/u })`: that
			// button's visible text is `t('audit.details')`, which is "Details" in
			// English and "Einzelheiten" in German, so a fixed English pattern
			// would only ever find it on one of the two passes.
			afterGoto = async (crawlPage) => {
				const toggle = crawlPage.locator('table tbody tr').first().getByRole('button');
				await waitForHydration(toggle);
				await toggle.click();
				await expect(toggle).toHaveAttribute('aria-expanded', 'true');
			};
		}

		// Every authenticated page renders `AuthedShell` -> `TeamSwitcher`, which
		// prints `membership.name` — this run's `teamName` fixture value — as
		// plain link text. That is user data, not UI copy: a real Verein's own
		// name would sit in that exact spot and would be exactly as identical
		// across languages, because nobody translates an association's name
		// (any more than they would translate "Bürgerinitiative Lindenstraße
		// e.V." into English for the English UI — it already is what it is,
		// regardless of language). The same is true of a link's destination and
		// short URL (`linkStrings`, above, populated when `links` or `stats`
		// created one): a real Verein's own link would render its own
		// destination and short URL in that exact spot, identically in both
		// languages, for the same reason — nobody translates a URL either.
		// `domainStrings` (above,
		// populated only when `domains` claimed one) is the same story again: a
		// hostname, its TXT challenge name, and the raw values of the two DNS
		// records a Verein is told to create are all data a claiming team
		// supplied or that this instance generated, never copy. `auditStrings`
		// (populated only when `audit-log` seeded and read one) is the same
		// story again: the fixture's own team member email, and the `slug`/
		// `hostname`/`destination_url` values that link's own `link.created` row
		// disclosed. Allowing the *literal* strings this run's own fixture, link
		// creation, domain claim, and team membership produced — reusing the
		// module's own exclusion Set rather than a second mechanism — has no
		// blind spot: a pattern-based exclusion (a UUID shape, an `e2e ` prefix,
		// "anything that looks like a URL, hostname or email") would just as
		// happily swallow a real hardcoded string that happened to sit next to
		// one of these, which is exactly the false negative this spec exists to
		// prevent.
		const identicalByDesign = new Set([
			...IDENTICAL_BY_DESIGN,
			teamName,
			...linkStrings,
			...domainStrings,
			...auditStrings,
		]);

		const english = new Set(
			await visibleText({ afterGoto, baseURL, identicalByDesign, language: 'en', page, path }),
		);
		const german = await visibleText({
			afterGoto,
			baseURL,
			identicalByDesign,
			language: 'de',
			page,
			path,
		});

		const untranslated = german.filter((value) => english.has(value));

		expect(
			untranslated,
			`these strings did not change with the language: ${untranslated.join(', ')}`,
		).toEqual([]);
	});
}
