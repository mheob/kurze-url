/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `Link`/`PageLink`/`Folder` types (`Link.tags`'s
   nested array included), whose properties are not marked readonly; that is generated codegen
   output, never edited by hand. */

import type { Folder, Link as ApiLink, PageLink, Tag } from '@kurze-url/api-client';
import {
	createRootRoute,
	createRoute,
	createRouter,
	createMemoryHistory,
	RouterProvider,
} from '@tanstack/react-router';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, useState } from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkList } from './link-list';

function linkWith(overrides: Partial<ApiLink> = {}): ApiLink {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00Z',
		created_by: 'user-1',
		destination_url: 'https://example.org/',
		domain_id: 'domain-1',
		expires_at: null,
		has_password: false,
		hostname: 'short.invalid',
		id: 'link-1',
		redirect_type: 302,
		short_url: 'https://short.invalid/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'a',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides,
	};
}

/**
 * Unlike `link-list.stories.tsx`'s own `pageOf` (and this file's own former
 * one), this takes the items directly rather than a `Partial<PageLink>`:
 * every test below either wants a specific set of links with `total_count`
 * following from it, or overrides `total_count`/`per_page` by spreading over
 * the result — `{ ...pageOf([...]), per_page: 1, total_count: 2 }` — which
 * reads the override at the call site instead of hiding it behind another
 * layer of partial-object merging.
 *
 * @param items - The links on this page.
 * @returns The page, with `page: 1`, `per_page: 20`, and `total_count` following from `items.length`.
 */
function pageOf(items: readonly ApiLink[] = []): PageLink {
	return { items: [...items], page: 1, per_page: 20, total_count: items.length };
}

/** The props `renderList`/`listElement` accept; every field has a default so a test only names what it cares about. */
interface ListElementOptions {
	/** Defaults to true: most tests are about the list, not about who may edit. */
	readonly canEdit?: boolean;
	readonly data?: PageLink;
	readonly folder?: string;
	readonly folders?: readonly Folder[];
	readonly onFilterChange?: (next: { folder?: string; tag?: string }) => void;
	readonly page?: number;
	readonly tag?: string;
	readonly tags?: readonly Tag[];
	readonly teamSlug?: string;
}

/**
 * Identity function returning the props it was given — `renderList`'s
 * `rerender` (below) takes the same shape directly, so this exists only so a
 * test reads `rerender(listElement({ ... }))` rather than a bare object
 * literal, mirroring how the task brief names it.
 *
 * @param options - The next props to render `LinkList` with.
 * @returns The same options, unchanged.
 */
function listElement(options: ListElementOptions = {}): ListElementOptions {
	return options;
}

/**
 * `folders` needs a three-way default, not the usual `?? []`: an *omitted*
 * `folders` key means "this test doesn't care, default to loaded-and-empty"
 * (`links.folderNone` for existing tests, unchanged since before this task,
 * predates the folder column entirely) — but an *explicitly* passed
 * `folders: undefined` (as "falls back to the ordinary empty state..." below
 * does) means "folders unavailable" and must reach `LinkList` unchanged, not
 * be folded into `[]`. `?? []` cannot tell those two apart — `state.folders`
 * reads as `undefined` either way — so this checks the key's presence with
 * `in` instead.
 *
 * @param state - The current render's options.
 * @returns `state.folders` untouched when the key is present (however it is set), `[]` when it is absent.
 */
function foldersOf(state: ListElementOptions): readonly Folder[] | undefined {
	return 'folders' in state ? state.folders : [];
}

/**
 * `foldersOf`'s counterpart for the tags, for the same reason: an omitted
 * `tags` key means loaded-and-empty, an explicit `tags: undefined` means the
 * tags query has not resolved, and `?? []` could not tell them apart.
 *
 * @param state - The current render's options.
 * @returns `state.tags` untouched when the key is present (however it is set), `[]` when it is absent.
 */
function tagsOf(state: ListElementOptions): readonly Tag[] | undefined {
	return 'tags' in state ? state.tags : [];
}

/**
 * `LinkList` renders TanStack Router `<Link>` elements for pagination, the
 * folder filter's row links and the "New link"/"Show all links" links, which
 * all need a router in context — the same reasoning `team-switcher.test.tsx`
 * gives for its own minimal, test-only route tree. Also registers
 * `/teams/$teamSlug/links/new` and `/teams/$teamSlug/links/$linkId`, the
 * create and edit entry points `<Link>` targets from this component.
 *
 * Unlike the file's former `renderWith`, the router and its route tree are
 * built once per `renderList` call and never recreated: the root route's own
 * component holds the current props in `useState`, and the `rerender`
 * returned here (deliberately not Testing Library's own `rerender`, which
 * would require rebuilding — and remounting — the whole router to hand the
 * leaf component new props) just updates that state. That keeps a test like
 * "keeps the filter and heading visible over each empty state" — which walks
 * through three different `folder` values in a row — driven by ordinary
 * React re-renders instead of tearing down and reattaching a `RouterProvider`
 * three times, which is not a transition TanStack Router's own initial-match
 * resolution is written to support mid-test.
 *
 * @param initial - The props to render `LinkList` with first.
 * @returns Testing Library's render result, with `rerender` replaced by one that accepts the next props directly (not a `ReactElement`).
 */
function renderList(initial: ListElementOptions = {}): Omit<
	ReturnType<typeof render>,
	'rerender'
> & {
	rerender: (next: ListElementOptions) => void;
} {
	let applyState: ((next: ListElementOptions) => void) | undefined;

	function Root(): React.JSX.Element {
		const [state, setState] = useState(initial);
		// Registered in an effect, not assigned directly during render: oxlint's
		// `react/globals` flags reassigning a variable declared outside the
		// component while rendering, exactly this line's own help text
		// ("update it in an effect" instead). Testing Library's `render` flushes
		// effects synchronously (it wraps the initial render in `act`), so
		// `applyState` is already set by the time `renderList` returns below.
		// `setState` is stable across renders (React guarantees it), so an empty
		// dependency list is accurate, not a lie told to silence the linter.
		useEffect(() => {
			applyState = setState;
		}, []);
		return (
			<LinkList
				canEdit={state.canEdit ?? true}
				data={state.data ?? pageOf()}
				folder={state.folder}
				folders={foldersOf(state)}
				onFilterChange={
					state.onFilterChange ?? vi.fn<(next: { folder?: string; tag?: string }) => void>()
				}
				page={state.page ?? 1}
				tag={state.tag}
				tags={tagsOf(state)}
				teamSlug={state.teamSlug ?? 'verein-a'}
			/>
		);
	}

	const rootRoute = createRootRoute({ component: Root });
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const newLinkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/new',
	});
	const editLinkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/$linkId',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute, newLinkRoute, editLinkRoute]),
	});

	const result = render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);

	return {
		...result,
		rerender: (next: ListElementOptions) => {
			act(() => {
				applyState?.(next);
			});
		},
	};
}

/**
 * The search parameters of a rendered link, read from its `href`. Order-free
 * on purpose: which parameters a link carries is the contract, the order
 * TanStack Router happens to serialise them in is not. Values are strings, as
 * the URL has no other kind.
 *
 * @param link - A rendered `<a>`.
 * @returns Its query parameters.
 */
function searchOf(link: HTMLElement): Record<string, string> {
	const href = link.getAttribute('href') ?? '';
	return Object.fromEntries(new URL(href, 'http://localhost').searchParams);
}

describe(LinkList, () => {
	/**
	 * The task brief warns this exact state is easy to get silently wrong: a
	 * list that renders nothing looks identical to a team with no links,
	 * unless the empty state actually says so. See the falsification note in
	 * the task report for the mutation this test exists to catch.
	 */
	it('shows the empty-state message when the team has no links', async () => {
		renderList();
		// `findBy*`, not `getBy*`, for the first assertion in every test here:
		// `RouterProvider`'s initial match resolves asynchronously (its own
		// microtask, separate from React's synchronous render), the same
		// reason `team-switcher.test.tsx` awaits its first query too.
		await expect(screen.findByText('No links yet.')).resolves.toBeInTheDocument();
		expect(screen.queryByRole('table')).not.toBeInTheDocument();
	});

	it('offers a link to create the first link when the team has none', async () => {
		// Finding 2: the empty state read as an actionable prompt ("Create your
		// first one") with nothing to click. Now it is an actual link — and,
		// since Task 7, the one link always rendered above the filter, not a
		// second copy duplicated inside the empty state.
		renderList();
		await expect(screen.findByRole('link', { name: 'Create link' })).resolves.toHaveAttribute(
			'href',
			'/teams/verein-a/links/new',
		);
	});

	it('offers a link to create another link when the team already has links', async () => {
		// Finding 2, other half: a team that already has links must still be
		// able to reach the create page, not only a team with none.
		renderList({ data: pageOf([linkWith()]) });
		await expect(screen.findByRole('link', { name: 'Create link' })).resolves.toHaveAttribute(
			'href',
			'/teams/verein-a/links/new',
		);
	});

	it('offers an edit link per row, addressed at both the team and the link', async () => {
		// Finding 2: rows had no edit link at all, so Task 11's entire edit and
		// delete surface was reachable only by hand-typing a URL containing a
		// UUID. The edit link interpolates *two* params — a wrong `teamSlug` or a
		// row's link confused with another's would both compile and pass a
		// role/name-only assertion, so this pins the actual `href` for each row.
		// The fixture's `teamSlug` ('verein-a') and its `team_id` ('a') are
		// deliberately different values: pinning the `href` only proves the
		// component used the right one because the two can't be mistaken for
		// each other here.
		renderList({
			data: pageOf([
				linkWith(),
				linkWith({ id: 'link-2', short_url: 'https://short.invalid/def456' }),
			]),
		});

		const editLinks = await screen.findAllByRole('link', { name: 'Edit' });
		expect(editLinks).toHaveLength(2);
		expect(editLinks[0]).toHaveAttribute('href', '/teams/verein-a/links/link-1');
		expect(editLinks[1]).toHaveAttribute('href', '/teams/verein-a/links/link-2');
	});

	// Below editor the API refuses create, update and delete with a 403, so
	// the list stops offering the way in. The row link stays, because the
	// link page is how a viewer reads a link's settings and gets its QR code
	// and statistics; only its wording changes, so it does not promise an edit.
	describe('for a member below editor', () => {
		it('offers no way to create a link, with links or on an empty team', async () => {
			renderList({ canEdit: false, data: pageOf([linkWith()]) });
			await expect(
				screen.findByRole('link', { name: 'https://short.invalid/abc123' }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('link', { name: 'Create link' })).not.toBeInTheDocument();

			renderList({ canEdit: false });
			await expect(screen.findByText('No links yet.')).resolves.toBeInTheDocument();
			expect(screen.queryByRole('link', { name: 'Create link' })).not.toBeInTheDocument();
		});

		it('words each row link as "Details", still addressed at the link page', async () => {
			renderList({
				canEdit: false,
				data: pageOf([
					linkWith(),
					linkWith({ id: 'link-2', short_url: 'https://short.invalid/def456' }),
				]),
			});

			const detailLinks = await screen.findAllByRole('link', { name: 'Details' });
			expect(detailLinks).toHaveLength(2);
			expect(detailLinks[0]).toHaveAttribute('href', '/teams/verein-a/links/link-1');
			expect(detailLinks[1]).toHaveAttribute('href', '/teams/verein-a/links/link-2');
			expect(screen.queryByRole('link', { name: 'Edit' })).not.toBeInTheDocument();
		});

		it('keeps the filters, the chips and the pagination', async () => {
			renderList({
				canEdit: false,
				data: {
					...pageOf([linkWith({ tags: [{ id: 'tag-1', name: 'Jugend', team_id: 'a' }] })]),
					per_page: 1,
					total_count: 2,
				},
				folders: [],
				tags: [{ id: 'tag-1', name: 'Jugend', team_id: 'a' }],
			});

			await expect(screen.findByRole('link', { name: 'Jugend' })).resolves.toBeInTheDocument();
			expect(screen.getByRole('link', { name: 'Next page' })).toBeInTheDocument();
			expect(screen.getByRole('heading', { level: 1, name: 'Your links' })).toBeInTheDocument();
		});
	});

	it('lists every link on the page with a copy button and its destination', async () => {
		renderList({
			data: pageOf([
				linkWith(),
				linkWith({ id: 'link-2', short_url: 'https://short.invalid/def456' }),
			]),
		});

		await expect(
			screen.findByRole('link', { name: 'https://short.invalid/abc123' }),
		).resolves.toBeInTheDocument();
		// One header row plus one per link.
		expect(screen.getAllByRole('row')).toHaveLength(3);
		expect(screen.getAllByRole('button', { name: 'Copy' })).toHaveLength(2);
	});

	it('marks a password-protected link', async () => {
		renderList({ data: pageOf([linkWith({ has_password: true })]) });

		// The badge carries text, not only an icon and a colour: colour alone may
		// never be the sole carrier of meaning (WCAG 1.4.1), and the icon is
		// aria-hidden.
		await expect(screen.findByText('Password protected')).resolves.toBeInTheDocument();
	});

	it('shows the short-domain notice when the links live on an .invalid hostname', async () => {
		renderList({ data: pageOf([linkWith({ hostname: 'short.invalid' })]) });
		await expect(screen.findByRole('note')).resolves.toBeInTheDocument();
	});

	it('hides the short-domain notice once a real domain is configured', async () => {
		renderList({ data: pageOf([linkWith({ hostname: 'kurze.url' })]) });
		await expect(screen.findByRole('heading', { name: 'Your links' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('still shows the notice when a verified custom domain sorts first', async () => {
		// The domain picker made this reachable: a team may now mix links on a
		// custom domain it verified with links still on the shared instance
		// hostname. Reading only `items[0]` would miss the invalid one entirely
		// whenever it is not the first row — this pins the fix against that
		// exact ordering.
		renderList({
			data: pageOf([
				linkWith({ hostname: 'kurze.url', id: 'link-1', short_url: 'https://kurze.url/abc123' }),
				linkWith({
					hostname: 'short.invalid',
					id: 'link-2',
					short_url: 'https://short.invalid/def456',
				}),
			]),
		});
		await expect(screen.findByRole('note')).resolves.toBeInTheDocument();
	});

	it('disables the previous-page control on the first page', async () => {
		renderList({
			data: { ...pageOf([linkWith()]), per_page: 1, total_count: 2 },
			page: 1,
		});
		await expect(screen.findByRole('link', { name: 'Next page' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Previous page' })).not.toBeInTheDocument();
	});

	it('disables the next-page control on the last page', async () => {
		renderList({
			data: { ...pageOf([linkWith()]), page: 2, per_page: 1, total_count: 2 },
			page: 2,
		});
		await expect(screen.findByRole('link', { name: 'Previous page' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Next page' })).not.toBeInTheDocument();
	});

	const folders: readonly Folder[] = [
		{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
	];

	const tags: readonly Tag[] = [
		{ id: 't1', name: 'Jugend', team_id: 'team-a' },
		{ id: 't2', name: 'Presse', team_id: 'team-a' },
	];

	const unknownId = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';

	it('shows the folder column: a link for a filed link, "–" with hidden text for an unfiled one', async () => {
		renderList({
			data: pageOf([linkWith({ folder_id: 'f1', id: 'l1' }), linkWith({ id: 'l2' })]),
			folders,
		});
		await expect(screen.findByRole('columnheader', { name: 'Folder' })).resolves.toBeVisible();
		expect(screen.getByRole('link', { name: 'Sommerfest' })).toHaveAttribute(
			'href',
			expect.stringContaining('folder=f1'),
		);
		expect(screen.getByText('No folder', { selector: '.sr-only' })).toBeInTheDocument();
	});

	it("shows the tags column: a filed link's tags as links that keep the folder and start at page 1", async () => {
		renderList({
			data: pageOf([
				linkWith({
					folder_id: 'f1',
					id: 'l1',
					tags: [
						{ id: 't1', name: 'Jugend', team_id: 'team-a' },
						{ id: 't2', name: 'Presse', team_id: 'team-a' },
					],
				}),
			]),
			folder: 'f1',
			folders,
			page: 3,
			tags,
		});
		await expect(screen.findByRole('columnheader', { name: 'Tags' })).resolves.toBeVisible();
		// The chips are named after the tags the link itself carries, and the
		// page jumps back to 1 even though the reader is on page 3: a new
		// filter's first page is the only one that is certain to exist.
		expect(searchOf(screen.getByRole('link', { name: 'Presse' }))).toStrictEqual({
			folder: 'f1',
			page: '1',
			tag: 't2',
		});
		expect(searchOf(screen.getByRole('link', { name: 'Jugend' }))).toStrictEqual({
			folder: 'f1',
			page: '1',
			tag: 't1',
		});
	});

	it('shows "–" with hidden text for a link without tags', async () => {
		// Filed, so the "–" in the folder column is not also on the page.
		renderList({ data: pageOf([linkWith({ folder_id: 'f1', tags: [] })]), folders });
		await expect(
			screen.findByText('No tags', { selector: '.sr-only' }),
		).resolves.toBeInTheDocument();
		expect(screen.getAllByText('–')).toHaveLength(1);
	});

	it('names a link whose tags the API sent as null the same as one without tags', async () => {
		// `Link.tags` is nullable on the wire, like `PageLink.items`.
		renderList({ data: pageOf([linkWith({ folder_id: 'f1', tags: null })]), folders });
		await expect(
			screen.findByText('No tags', { selector: '.sr-only' }),
		).resolves.toBeInTheDocument();
	});

	it("keeps the active tag in a row's folder link", async () => {
		renderList({
			data: pageOf([linkWith({ folder_id: 'f1', id: 'l1' })]),
			folders,
			tag: 't2',
			tags,
		});
		// The two filters are additive: choosing a facet on a row narrows what
		// is already filtered instead of replacing it, the way a tag chip keeps
		// the folder.
		await expect(screen.findByRole('link', { name: 'Sommerfest' })).resolves.toBeInTheDocument();
		expect(searchOf(screen.getByRole('link', { name: 'Sommerfest' }))).toStrictEqual({
			folder: 'f1',
			page: '1',
			tag: 't2',
		});
	});

	it('reports a filter change through onFilterChange, keeping the other filter', async () => {
		const onFilterChange = vi.fn<(next: { folder?: string; tag?: string }) => void>();
		renderList({ folder: 'f1', folders, onFilterChange, tags });
		await userEvent.selectOptions(await screen.findByRole('combobox', { name: 'Tag' }), 't1');
		expect(onFilterChange).toHaveBeenCalledWith({ folder: 'f1', tag: 't1' });
		await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Folder' }), 'none');
		expect(onFilterChange).toHaveBeenLastCalledWith({ folder: 'none', tag: undefined });
	});

	it('names the active tag under the heading', async () => {
		renderList({ data: pageOf([linkWith()]), folder: 'f1', folders, tag: 't2', tags });
		await expect(screen.findByText('Tag: Presse')).resolves.toBeVisible();
		expect(screen.getByText('Folder: Sommerfest')).toBeVisible();
	});

	it('keeps the filter and heading visible over each empty state', async () => {
		const { rerender } = renderList({ data: pageOf(), folder: 'f1', folders });
		await expect(screen.findByText('No links in this folder.')).resolves.toBeVisible();
		expect(screen.getByRole('combobox', { name: 'Folder' })).toBeVisible();

		rerender(listElement({ data: pageOf(), folder: 'none', folders }));
		expect(screen.getByText('Every link is in a folder.')).toBeVisible();

		rerender(listElement({ data: pageOf(), folder: unknownId, folders }));
		expect(screen.getByText('This folder does not exist (any more).')).toBeVisible();
		expect(screen.getByRole('link', { name: 'Show all links' })).toBeVisible();
	});

	it('words the empty state for a tag alone, a folder with a tag, and no folder with a tag', async () => {
		const { rerender } = renderList({ data: pageOf(), folders, tag: 't2', tags });
		await expect(screen.findByText('No links with this tag.')).resolves.toBeVisible();

		rerender(listElement({ data: pageOf(), folder: 'f1', folders, tag: 't2', tags }));
		expect(screen.getByText('No links in this folder with this tag.')).toBeVisible();

		// "No folder" is not a folder the reader is "in", so it has its own wording.
		rerender(listElement({ data: pageOf(), folder: 'none', folders, tag: 't2', tags }));
		expect(screen.getByText('No unfiled links with this tag.')).toBeVisible();
	});

	it('says the tag does not exist once the tags are known, with a way back', async () => {
		renderList({ data: pageOf(), folders, tag: unknownId, tags });
		await expect(screen.findByText('This tag does not exist (any more).')).resolves.toBeVisible();
		expect(screen.getByRole('link', { name: 'Show all links' })).toBeVisible();
		// The tag has no name to show, so no "Tag: …" line pretends it has.
		expect(screen.queryByText(/^Tag: /u)).not.toBeInTheDocument();
	});

	it('shows the folder message when both the folder and the tag are unknown', async () => {
		renderList({
			data: pageOf(),
			folder: '5d1c2b3a-9e8f-4a7b-8c6d-1f2e3a4b5c6d',
			folders,
			tag: unknownId,
			tags,
		});
		await expect(
			screen.findByText('This folder does not exist (any more).'),
		).resolves.toBeVisible();
		expect(screen.queryByText('This tag does not exist (any more).')).not.toBeInTheDocument();
	});

	it('keeps both filters in the pagination links', async () => {
		renderList({
			data: { ...pageOf([linkWith({ id: 'l1' })]), page: 2, total_count: 45 },
			folder: 'f1',
			folders,
			page: 2,
			tag: 't2',
			tags,
		});
		await expect(screen.findByRole('link', { name: 'Next page' })).resolves.toBeInTheDocument();
		expect(searchOf(screen.getByRole('link', { name: 'Next page' }))).toStrictEqual({
			folder: 'f1',
			page: '3',
			tag: 't2',
		});
		expect(searchOf(screen.getByRole('link', { name: 'Previous page' }))).toStrictEqual({
			folder: 'f1',
			page: '1',
			tag: 't2',
		});
	});

	it('carries both filters into "New link"', async () => {
		renderList({ data: pageOf([linkWith()]), folder: 'f1', folders, tag: 't2', tags });
		const create = await screen.findByRole('link', { name: 'Create link' });
		expect(searchOf(create)).toStrictEqual({ folder: 'f1', tag: 't2' });
	});

	it('does not carry a tag it cannot name into "New link"', async () => {
		// An unknown tag id would be dropped by the create form anyway; not
		// forwarding it keeps the form's URL free of a filter that matched nothing.
		renderList({ data: pageOf([linkWith()]), folders, tag: unknownId, tags });
		await expect(screen.findByRole('link', { name: 'Create link' })).resolves.toHaveAttribute(
			'href',
			'/teams/verein-a/links/new',
		);
	});

	/**
	 * Controller ruling (Task 7 review, on top of the brief): the
	 * "does not exist (any more)" message must only ever appear once the
	 * folders are actually known — passing `folders={undefined}` is how the
	 * route reports "the folders query hasn't resolved (or failed) yet", and
	 * that must fall back to the ordinary in-folder empty state rather than
	 * accusing a possibly-real folder of not existing. The filter still
	 * offers "All folders" and "No folder", since neither depends on the
	 * team's actual folder list; `link-filter-bar.test.tsx` pins that half.
	 */
	it('falls back to the ordinary empty state instead of "missing" while folders have not loaded', async () => {
		renderList({
			data: pageOf(),
			folder: unknownId,
			folders: undefined,
		});
		await expect(screen.findByText('No links in this folder.')).resolves.toBeVisible();
		expect(screen.queryByText('This folder does not exist (any more).')).not.toBeInTheDocument();
	});

	/** The same ruling for tags: an unloaded tag list must not accuse a real tag of not existing. */
	it('falls back to the ordinary empty state instead of "missing" while tags have not loaded', async () => {
		renderList({ data: pageOf(), folders, tag: unknownId, tags: undefined });
		await expect(screen.findByText('No links with this tag.')).resolves.toBeVisible();
		expect(screen.queryByText('This tag does not exist (any more).')).not.toBeInTheDocument();
	});
});
