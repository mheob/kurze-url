import {
	createFolder,
	deleteFolder,
	listFolders,
	updateFolder,
	type Folder,
	type PageFolder,
} from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { FOLDERS_PER_TEAM } from '../lib/folders';
import { authedApiClient, flushSessionCookies, requireSession } from './session';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is the same `request: Request` parameter each `...For` function takes: `Request` nests a
 * mutable `Headers` through its own `.headers` getter, and `Readonly<>` is shallow — it does not
 * reach that nested property, unlike a bare `Headers` parameter, which the check does accept once
 * wrapped.
 */

/**
 * The one method `prefetchFolders` below reaches through — same reasoning as
 * `loadVerifiedDomains`'s own `DomainsDataSource` (`teams.$teamSlug.links.new.tsx`):
 * a real `QueryClient` satisfies this structurally, so callers need no cast,
 * and — unlike calling `context.queryClient.ensureQueryData` directly — a
 * fresh, separately declared interface method carries none of the real
 * `QueryClient.ensureQueryData`'s own `@deprecated` doc comment, so
 * `typescript/no-deprecated` has nothing to fire on. Declared here, ahead of
 * every export in this file (including `foldersQueryOptions`, whose return
 * type it names): `import/exports-last` requires every export to be
 * contiguous at the end of the file, and a type-only interface has no
 * runtime evaluation order to respect, so there is no cost to moving it
 * ahead of the value it types.
 */
interface FoldersDataSource {
	readonly ensureQueryData: (
		options: ReturnType<typeof foldersQueryOptions>,
	) => Promise<PageFolder>;
}

/**
 * Same `...For`/`...Fn` split as `server/links.ts` and `server/domains.ts`,
 * for the same reason: `listFoldersFn`'s `createServerFn` can't be called
 * directly under Vitest ("No Start context found"), so the testable half
 * takes `request: Request` as a plain parameter instead of reaching for
 * `getRequest()` itself. `folders.test.ts` exercises this function directly.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are required for
 * the same reason documented on `listLinksFor`: reading the session via
 * `requireSession` is what refreshes an expiring one, and skipping the flush
 * would silently drop that refresh's cookies on every folder list fetch.
 *
 * One page always holds every folder: `FOLDERS_PER_TEAM` (100) equals the
 * API's own maximum page size, so a team at its cap still fits in one
 * request and this never needs a `page` parameter of its own.
 */
export const listFoldersFor = createServerOnlyFn(
	async (request: Request, teamId: string): Promise<PageFolder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await listFolders({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// One page always holds every folder: the cap equals the maximum page size.
			query: { per_page: FOLDERS_PER_TEAM },
			throwOnError: true,
		});
		return data;
	},
);

/** `getRequest()` inline, not inside `listFoldersFor`, for the same reason as `listLinksFn`. */
export const listFoldersFn = createServerFn({ method: 'GET' })
	.validator((data: { readonly teamId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly teamId: string } }) =>
		listFoldersFor(getRequest(), data.teamId),
	);

/**
 * One definition of the key and the fetcher, used by both a route's loader
 * (`ensureQueryData`) and its component (`useSuspenseQuery`), the same reason
 * `domainsQueryOptions` exists. Two definitions drift, and the symptom is a
 * folder list that updates on navigation but not after a create, rename or
 * delete.
 *
 * @param teamId - The team whose folders to list.
 * @returns Query options for `useSuspenseQuery`/`ensureQueryData`, keyed on `['folders', teamId]`.
 */
// oxlint's typescript(explicit-function-return-type) is error-level, but
// `queryOptions`'s own return type can't be written out by hand without
// losing the specific `['folders', teamId]` tuple type `useSuspenseQuery`
// needs downstream — same reasoning as `domainsQueryOptions`. Same reason
// covers `explicit-module-boundary-types` below: it's the same missing
// annotation this exported function can't be given either.
// oxlint-disable-next-line typescript/explicit-function-return-type, typescript/explicit-module-boundary-types
export const foldersQueryOptions = (teamId: string) =>
	queryOptions({
		queryFn: async () => listFoldersFn({ data: { teamId } }),
		queryKey: ['folders', teamId] as const,
	});

/**
 * Warms `['folders', teamId]` ahead of the link create/edit routes' own
 * component-level `useQuery` read, without making either route's *loader* —
 * and therefore the whole page — depend on the folders fetch succeeding: a
 * link form with no folder options is still a usable link form, unlike one
 * with no domains or no link at all. Every failure is swallowed and logged,
 * the same fallback `loadVerifiedDomains` uses and for the same reason:
 * Vercel Pro retains runtime logs for a day (CLAUDE.md), so this is not this
 * failure's durable record — and it never reaches Sentry either, since a
 * bare `console.error` is not routed through `lib/observability`'s own
 * `reportUnexpected` — but a day is still enough for "the folder select
 * quietly offers only 'No folder'" to turn from a mystery someone notices
 * downstream into something a `runtime-logs` search on this route can
 * actually surface.
 *
 * @param queryClient - The query client to prefetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns Nothing — callers read the result back out of the query cache, via `useQuery`.
 */
export async function prefetchFolders(
	queryClient: FoldersDataSource,
	teamId: string,
): Promise<void> {
	try {
		await queryClient.ensureQueryData(foldersQueryOptions(teamId));
	} catch (error) {
		// See `loadVerifiedDomains`'s identical comment: silent to the visitor,
		// not to every possible observer.
		console.error('prefetchFolders: leaving the folder select unfilled', error);
	}
}

/**
 * Same `...For`/`...Fn` split, same reason as `listFoldersFor` above.
 */
export const createFolderFor = createServerOnlyFn(
	async (request: Request, teamId: string, name: string): Promise<Folder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createFolder({
			body: { name },
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// throwOnError is required: the generated client's default (false)
			// never rejects, so a taken name or an over-cap team would resolve to
			// `{ data: undefined, error }` instead of throwing — silently
			// reporting a folder that was never created.
			throwOnError: true,
		});
		return data;
	},
);

export const createFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly name: string; readonly teamId: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly name: string; readonly teamId: string } }) =>
			createFolderFor(getRequest(), data.teamId, data.name),
	);

/**
 * Same `...For`/`...Fn` split, same reason.
 */
export const renameFolderFor = createServerOnlyFn(
	async (request: Request, folderId: string, name: string): Promise<Folder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await updateFolder({
			body: { name },
			client: authedApiClient(accessToken),
			path: { folder_id: folderId },
			throwOnError: true,
		});
		return data;
	},
);

export const renameFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly folderId: string; readonly name: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly folderId: string; readonly name: string } }) =>
			renameFolderFor(getRequest(), data.folderId, data.name),
	);

/**
 * Same `...For`/`...Fn` split. Returns `void`, not the folder: nothing
 * downstream reads a return value — the folders page's mutation only cares
 * whether the promise resolved or rejected — same reasoning as `deleteLinkFor`.
 */
export const deleteFolderFor = createServerOnlyFn(
	async (request: Request, folderId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await deleteFolder({
			client: authedApiClient(accessToken),
			path: { folder_id: folderId },
			throwOnError: true,
		});
	},
);

export const deleteFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly folderId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly folderId: string } }) =>
		deleteFolderFor(getRequest(), data.folderId),
	);
