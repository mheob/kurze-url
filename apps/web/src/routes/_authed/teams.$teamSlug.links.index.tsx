import type { PageLink } from '@kurze-url/api-client';
import { useQuery, useSuspenseQuery } from '@tanstack/react-query';
import {
	createFileRoute,
	Navigate,
	redirect,
	type SearchSchemaInput,
} from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { LinkList } from '../../components/link-list';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { folderFilterOf, parseFolderSearch, type FolderFilter } from '../../lib/folders';
import { parseUuidSearch } from '../../lib/names';
import { reportUnexpected } from '../../lib/observability';
import { foldersQueryOptions, prefetchFolders } from '../../server/folders';
import { linksQueryOptions } from '../../server/links';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is typed by
   TanStack Router/Query's own option shapes — `linksQueryOptions`'s `ReturnType`, `validateSearch`,
   `beforeLoad`, `loaderDeps`, `loader` — none of which is a declaration this file can edit. */

/**
 * The one method this loader reaches through on `context.queryClient` — a
 * real `QueryClient` satisfies this structurally, so the loader below needs
 * no cast, and `links.test.ts`'s fake-object style (narrow interface, not a
 * mocked class) works here without `no-unsafe-type-assertion` needing to be
 * silenced.
 */
interface LinksDataSource {
	readonly ensureQueryData: (options: ReturnType<typeof linksQueryOptions>) => Promise<PageLink>;
}

/**
 * Finding (Fix round 1): a 401 reaching this loader used to fall through to
 * `errorComponent`, which rendered `errors.unauthenticated` as dead-end
 * inline text on a page the visitor can no longer use. `_authed.tsx`'s
 * `beforeLoad` already redirects to `/login` for the *no session at all*
 * case (`isUnauthenticatedError`, checked one layer up); this loader's own
 * gap was the narrower window where that check passed but the session dies
 * — or the API rejects the token for some other reason — by the time this
 * route's own fetch runs. That surfaces as the API answering the actual
 * `listLinks` call with a 401, which `classifyApiError` (not
 * `isUnauthenticatedError`, which only matches the *no session at all*
 * shape `requireSession` throws) turns into `{ kind: 'unauthenticated' }`.
 *
 * Every other error kind is rethrown unchanged, so it still reaches
 * `errorComponent` and fails loudly — an empty list is indistinguishable
 * from a team with no links, which is the worse failure this list is built
 * to avoid.
 *
 * Extracted from the route's `loader` option so it can be unit-tested with a
 * fake `LinksDataSource` instead of a real router loader context; see
 * `teams.$teamSlug.links.index.test.ts`.
 *
 * `page` and `filter` are bundled into `query`, for the same
 * `eslint(max-params)` reason `addMemberFor` gives in `server/members.ts`:
 * `teamId` stays positional, and `query` groups the two things that shape
 * the page of links being asked for — the same split `listLinksFor` already
 * uses in `server/links.ts` for its own `query` parameter.
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @param query - The 1-indexed page number to fetch, and which folder to scope the list to.
 * @returns The requested page of links.
 */
export async function loadLinks(
	queryClient: LinksDataSource,
	teamId: string,
	query: { readonly filter: FolderFilter; readonly page: number },
): Promise<PageLink> {
	try {
		return await queryClient.ensureQueryData(linksQueryOptions(teamId, query.page, query.filter));
	} catch (error) {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

/**
 * A new folder filter always starts at page 1, as the audit log's filters do
 * (`nextFilters` in `audit-filter-bar.tsx`) — a filtered view still showing a
 * page number from the previous, unfiltered result would leave the reader
 * looking at a page that may not exist under the new filter.
 *
 * @param folder - The newly chosen `folder` search value, or undefined for all folders.
 * @returns The search parameters to navigate to.
 */
export function folderChangeSearch(folder: string | undefined): {
	readonly folder?: string;
	readonly page: number;
} {
	return { folder, page: 1 };
}

/**
 * The route's own `validateSearch`, extracted to a named, exported function
 * so it can be unit-tested directly with a plain object — this codebase's
 * other routes reach for the same split (`parseAuditFilters` for the audit
 * log) rather than poking at `Route.options`, which TanStack Router does not
 * build for that purpose. Deliberately typed *without* the `& SearchSchemaInput`
 * intersection the route's own `validateSearch` carries below: that marker
 * only matters for TanStack Router's own inference of this route's `<Link>`
 * write-side schema, and a plain object literal in a test can never satisfy
 * an intersection with it (it has no way to supply the marker's phantom
 * property) — the same reason `parseAuditFilters` itself doesn't carry it
 * either, only the arrow function wrapping it does.
 *
 * `page?: number | string` (not just `number`) is what the raw URL actually
 * hands this function — parsed search params are strings — and it's also
 * what keeps `Number(search.page ?? 1)` below a real conversion rather than a
 * no-op oxlint's `no-unnecessary-type-conversion` would flag; `Number(...)`
 * folds an absent, malformed, or non-numeric `page` to a `NaN`-free `1`
 * rather than propagating garbage into the API call. `folder` goes through
 * `parseFolderSearch`, which drops anything but `none` or a well-formed UUID
 * the same way.
 *
 * `tag` is a stub for now: the tags page links each tag to `?tag=<id>`, so
 * the route has to declare the parameter for those links to typecheck, but
 * nothing reads it yet — `loaderDeps` and the component leave it alone until
 * the tag filter lands. It goes through `parseUuidSearch`, so a tag that
 * isn't a UUID is dropped the way a malformed `folder` is.
 *
 * @param search - The raw search record TanStack Router hands `validateSearch`.
 * @returns The parsed `folder`/`page`/`tag` search, `page` always a positive integer.
 */
export function parseLinksSearch(search: {
	folder?: unknown;
	page?: number | string;
	tag?: unknown;
}): {
	folder?: string;
	page: number;
	tag?: string;
} {
	const page = Number(search.page ?? 1);
	return {
		folder: parseFolderSearch(search.folder),
		page: Number.isFinite(page) && page > 0 ? page : 1,
		tag: parseUuidSearch(search.tag),
	};
}

// oxlint-disable-next-line sort-keys
export const Route = createFileRoute('/_authed/teams/$teamSlug/links/')({
	// Pagination lives in the URL — the same reasoning that put the team slug
	// in the path — so the back button works and a page can be sent to a
	// colleague. The parameter type intersects `SearchSchemaInput` (TanStack
	// Router's marker for "this validator's write side differs from its read
	// side") so that linking to this route, from `TeamSwitcher` or `/`'s
	// post-login redirect, can omit `page` entirely and still get page 1 —
	// without it, the route's *output* type (`page: number`, always present)
	// would also become the required *input* type for every `<Link>`
	// targeting this route, forcing unrelated call sites to know about
	// pagination. `page?: number | string` (not just `number`) is what the
	// raw URL actually hands this function — parsed search params are
	// strings — and it's also what keeps `Number(search.page ?? 1)` below a
	// real conversion rather than a no-op oxlint's
	// `no-unnecessary-type-conversion` would flag; `Number(...)` folds an
	// absent, malformed, or non-numeric `page` to a `NaN`-free `1` rather
	// than propagating garbage into the API call.
	//
	// Declared before `loaderDeps`/`loader` in this object, not merely for
	// readability: `loaderDeps`'s own `search` parameter is typed *from*
	// `validateSearch`'s return type, and moving this later made that
	// inference fall back to `{}`, failing `deps.page` below with "Property
	// 'page' does not exist" — confirmed by moving it back and forth.
	// `parseLinksSearch` is the exported, unit-tested function; this arrow
	// function only adds the `& SearchSchemaInput` intersection back for
	// TanStack Router's own benefit (see `parseLinksSearch`'s docstring for
	// why that marker can't live on the tested function itself).
	validateSearch: (
		search: { folder?: unknown; page?: number | string; tag?: unknown } & SearchSchemaInput,
	): { folder?: string; page: number; tag?: string } => parseLinksSearch(search),
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	loaderDeps: ({ search }) => ({ folder: search.folder, page: search.page }),
	// Both requests run together: the folders fetch only warms
	// `['folders', teamId]` for the component's own `useQuery` read below (see
	// `prefetchFolders`'s docstring) and must never make the whole page depend
	// on it succeeding, so it is not awaited through `loadLinks`'s
	// unauthenticated-redirect/rethrow path — a link list is still useful with
	// an unfilled folder filter, unlike with no links at all.
	loader: async ({ context, deps }) => {
		await Promise.all([
			loadLinks(context.queryClient, context.teamId, {
				filter: folderFilterOf(deps.folder),
				page: deps.page,
			}),
			prefetchFolders(context.queryClient, context.teamId),
		]);
	},
	component: RouteComponent,
	errorComponent: LinksError,
});

/**
 * Unlike `server/health.ts`'s `fetchHealth` — which degrades to
 * `'unreachable'` so a down API can't break the footer — this list fails
 * loudly. A list that silently rendered empty on a failed request would look
 * exactly like a team with no links, and there would be nothing on screen to
 * tell those two states apart.
 *
 * `classifyApiError` (Task 8) is what turns whatever `listLinksFn` threw
 * (via `throwOnError: true`) into one of a small set of kinds; `fields` is
 * meaningless for a list fetch (it only ever arises from a form's 400/422),
 * so it falls back to the same generic message as an unrecognised failure.
 *
 * `kind: 'unauthenticated'` *can* still reach here (Fix round 2, reviewing
 * Fix round 1's claim that it couldn't): `loadLinks`'s try/catch only guards
 * its own `ensureQueryData` call, which is the *loader's* fetch. React
 * Query's defaults (`router.tsx` sets no `defaultOptions`, so
 * `refetchOnWindowFocus: true` applies) mean `useSuspenseQuery` in
 * `RouteComponent` below can also throw to this boundary on a *background*
 * refetch — e.g. the tab was left open, the session expired, and the window
 * regained focus — a path `loadLinks` never sees because it isn't a loader
 * run at all. So this component redirects to `/login` itself for that kind,
 * via `<Navigate>` (the component-side equivalent of the `throw redirect(...)`
 * a loader would use — a render can't throw a redirect the way a
 * loader/`beforeLoad` can, since nothing upstream is watching for one).
 * Every other kind still renders inline, unchanged, so the list keeps failing
 * loudly for a genuinely down API. There is still no `errors.unauthenticated`
 * catalogue key: both paths that can classify a failure this way redirect
 * before any text would render, so the key would stay dead.
 *
 * Reporting happens here and not only in `RootErrorPage` (Fix round 3):
 * TanStack Router renders the *nearest* `errorComponent`, and this route has
 * its own — so a 500 from listing links, the likeliest real failure in the
 * authenticated app, never reaches the root boundary and was never reported.
 * `reportUnexpected` refuses everything `classifyApiError` names, so the
 * kinds rendered as ordinary UI above still cost no event. Called during
 * render rather than from an effect, for the same reason `RootErrorPage`
 * does: this component also renders on the server, where effects never run.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function LinksError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { teamId } = Route.useRouteContext();
	const { folder, page } = Route.useSearch();
	const navigate = Route.useNavigate();
	const { data } = useSuspenseQuery(linksQueryOptions(teamId, page, folderFilterOf(folder)));
	// Non-suspense, deliberately: the loader's own `prefetchFolders` call never
	// rejects and never throws — a link list with an unfilled folder filter is
	// still a usable link list, unlike one with no links at all — so this read
	// must tolerate `data` staying `undefined` instead of suspending the whole
	// page on it. `data === undefined` (still loading, or the query errored)
	// is passed through as `folders={undefined}` rather than folded into `[]`
	// here: `LinkList` uses that distinction to tell "no folders exist" apart
	// from "folders haven't loaded yet", which is what keeps `folderMissing`
	// from flashing for a legitimate folder while this query is still in
	// flight.
	const { data: folderPage } = useQuery(foldersQueryOptions(teamId));
	const folders = folderPage === undefined ? undefined : (folderPage.items ?? []);

	return (
		<LinkList
			data={data}
			folder={folder}
			folders={folders}
			onFolderChange={(next) => {
				void navigate({ search: folderChangeSearch(next) });
			}}
			page={page}
			teamSlug={teamSlug}
		/>
	);
}
