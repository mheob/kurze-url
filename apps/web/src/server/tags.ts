import { createTag, deleteTag, listTags, updateTag, type Tag } from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { authedApiClient, flushSessionCookies, requireSession } from './session';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is the same `request: Request` parameter each `...For` function takes: `Request` nests a
 * mutable `Headers` through its own `.headers` getter, and `Readonly<>` is shallow — it does not
 * reach that nested property, unlike a bare `Headers` parameter, which the check does accept once
 * wrapped.
 */

/**
 * The one method `prefetchTags` below reaches through — same reasoning as
 * `FoldersDataSource` in `server/folders.ts`: a real `QueryClient` satisfies
 * this structurally, so callers need no cast, and a fresh, separately
 * declared interface method carries none of the real
 * `QueryClient.ensureQueryData`'s own `@deprecated` doc comment, so
 * `typescript/no-deprecated` has nothing to fire on. Declared ahead of every
 * export in this file (including `tagsQueryOptions`, whose return type it
 * names): `import/exports-last` requires every export to be contiguous at the
 * end of the file, and a type-only interface has no runtime evaluation order
 * to respect.
 */
interface TagsDataSource {
	readonly ensureQueryData: (options: ReturnType<typeof tagsQueryOptions>) => Promise<Tag[]>;
}

/** The API's page-size ceiling; with TAGS_PER_TEAM at 200, two pages always suffice. */
const TAG_PAGE_SIZE = 100;

/**
 * Same `...For`/`...Fn` split as `server/folders.ts`, for the same reason:
 * `listTagsFn`'s `createServerFn` can't be called directly under Vitest ("No
 * Start context found"), so the testable half takes `request: Request` as a
 * plain parameter instead of reaching for `getRequest()` itself.
 * `tags.test.ts` exercises this function directly.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are required for
 * the same reason documented on `listLinksFor`: reading the session via
 * `requireSession` is what refreshes an expiring one, and skipping the flush
 * would silently drop that refresh's cookies on every tag list fetch.
 *
 * Unlike `listFoldersFor`, one request is not always enough: a team may hold
 * `TAGS_PER_TEAM` (200) tags and the API pages at 100, so a second page is
 * fetched whenever the first one did not carry everything. Two pages always
 * suffice, which is why this is a fixed second request and not a loop — a
 * loop would also keep going if the API ever misreported `total_count`.
 */
export const listTagsFor = createServerOnlyFn(
	async (request: Request, teamId: string): Promise<Tag[]> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const client = authedApiClient(accessToken);
		const first = await listTags({
			client,
			path: { team_id: teamId },
			query: { page: 1, per_page: TAG_PAGE_SIZE },
			throwOnError: true,
		});
		const items = [...(first.data.items ?? [])];
		if (first.data.total_count > items.length) {
			const second = await listTags({
				client,
				path: { team_id: teamId },
				query: { page: 2, per_page: TAG_PAGE_SIZE },
				throwOnError: true,
			});
			items.push(...(second.data.items ?? []));
		}
		return items;
	},
);

/** `getRequest()` inline, not inside `listTagsFor`, for the same reason as `listLinksFn`. */
export const listTagsFn = createServerFn({ method: 'GET' })
	.validator((data: { readonly teamId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly teamId: string } }) =>
		listTagsFor(getRequest(), data.teamId),
	);

/**
 * One definition of the key and the fetcher, used by every route's loader
 * (`ensureQueryData`) and component (`useQuery`), the same reason
 * `foldersQueryOptions` exists. Two definitions drift, and the symptom is a
 * tag list that updates on navigation but not after a create, rename or
 * delete.
 *
 * @param teamId - The team whose tags to list.
 * @returns Query options for `useQuery`/`ensureQueryData`, keyed on `['tags', teamId]`.
 */
// oxlint's typescript(explicit-function-return-type) is error-level, but
// `queryOptions`'s own return type can't be written out by hand without
// losing the specific `['tags', teamId]` tuple type downstream consumers
// need — same reasoning as `foldersQueryOptions`. Same reason covers
// `explicit-module-boundary-types` below: it's the same missing annotation
// this exported function can't be given either.
// oxlint-disable-next-line typescript/explicit-function-return-type, typescript/explicit-module-boundary-types
export const tagsQueryOptions = (teamId: string) =>
	queryOptions({
		queryFn: async () => listTagsFn({ data: { teamId } }),
		queryKey: ['tags', teamId] as const,
	});

/**
 * Warms `['tags', teamId]` ahead of the link routes' own component-level
 * `useQuery` read, without making any route's *loader* — and therefore the
 * whole page — depend on the tags fetch succeeding: a link form with no tag
 * options is still a usable link form, unlike one with no domains or no link
 * at all. Every failure is swallowed and logged, the same fallback
 * `prefetchFolders` uses and for the same reason: Vercel Pro retains runtime
 * logs for a day (CLAUDE.md), so this is not the failure's durable record —
 * and it never reaches Sentry either, since a bare `console.error` is not
 * routed through `lib/observability`'s own `reportUnexpected` — but a day is
 * still enough for "the tag picker quietly offers nothing" to turn from a
 * mystery someone notices downstream into something a `runtime-logs` search
 * on this route can actually surface.
 *
 * @param queryClient - The query client to prefetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns Nothing — callers read the result back out of the query cache, via `useQuery`.
 */
export async function prefetchTags(queryClient: TagsDataSource, teamId: string): Promise<void> {
	try {
		await queryClient.ensureQueryData(tagsQueryOptions(teamId));
	} catch (error) {
		// See `prefetchFolders`'s identical comment: silent to the visitor,
		// not to every possible observer.
		console.error('prefetchTags: leaving the tag picker unfilled', error);
	}
}

/**
 * Same `...For`/`...Fn` split, same reason as `listTagsFor` above.
 */
export const createTagFor = createServerOnlyFn(
	async (request: Request, teamId: string, name: string): Promise<Tag> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createTag({
			body: { name },
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// throwOnError is required: the generated client's default (false)
			// never rejects, so a taken name or an over-cap team would resolve to
			// `{ data: undefined, error }` instead of throwing — silently
			// reporting a tag that was never created.
			throwOnError: true,
		});
		return data;
	},
);

export const createTagFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly name: string; readonly teamId: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly name: string; readonly teamId: string } }) =>
			createTagFor(getRequest(), data.teamId, data.name),
	);

/**
 * Same `...For`/`...Fn` split, same reason.
 */
export const renameTagFor = createServerOnlyFn(
	async (request: Request, tagId: string, name: string): Promise<Tag> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await updateTag({
			body: { name },
			client: authedApiClient(accessToken),
			path: { tag_id: tagId },
			throwOnError: true,
		});
		return data;
	},
);

export const renameTagFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly name: string; readonly tagId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly name: string; readonly tagId: string } }) =>
		renameTagFor(getRequest(), data.tagId, data.name),
	);

/**
 * Same `...For`/`...Fn` split. Returns `void`, not the tag: nothing
 * downstream reads a return value — the tags page's mutation only cares
 * whether the promise resolved or rejected — same reasoning as
 * `deleteFolderFor`.
 */
export const deleteTagFor = createServerOnlyFn(
	async (request: Request, tagId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await deleteTag({
			client: authedApiClient(accessToken),
			path: { tag_id: tagId },
			throwOnError: true,
		});
	},
);

export const deleteTagFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly tagId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly tagId: string } }) =>
		deleteTagFor(getRequest(), data.tagId),
	);
