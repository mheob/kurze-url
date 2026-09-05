import type { PageLink } from '@kurze-url/api-client';
import { useSuspenseQuery } from '@tanstack/react-query';
import {
	createFileRoute,
	Navigate,
	redirect,
	type SearchSchemaInput,
} from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { LinkList } from '../../components/link-list';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { linksQueryOptions } from '../../server/links';
import { assertMembership } from '../_authed';

/**
 * The one method this loader reaches through on `context.queryClient` — a
 * real `QueryClient` satisfies this structurally, so the loader below needs
 * no cast, and `links.test.ts`'s fake-object style (narrow interface, not a
 * mocked class) works here without `no-unsafe-type-assertion` needing to be
 * silenced.
 */
interface LinksDataSource {
	ensureQueryData: (options: ReturnType<typeof linksQueryOptions>) => Promise<PageLink>;
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
 * `teams.$teamId.links.index.test.ts`.
 */
export async function loadLinks(
	queryClient: LinksDataSource,
	teamId: string,
	page: number,
): Promise<PageLink> {
	try {
		return await queryClient.ensureQueryData(linksQueryOptions(teamId, page));
	} catch (error) {
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamId/links/')({
	// Pagination lives in the URL — the same reasoning that put the team id in
	// the path — so the back button works and a page can be sent to a
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
	validateSearch: (search: { page?: number | string } & SearchSchemaInput): { page: number } => {
		const page = Number(search.page ?? 1);
		return { page: Number.isFinite(page) && page > 0 ? page : 1 };
	},
	loaderDeps: ({ search }) => ({ page: search.page }),
	beforeLoad: ({ context, params }) => {
		assertMembership(context.me.memberships, params.teamId);
	},
	loader: ({ context, deps, params }) => loadLinks(context.queryClient, params.teamId, deps.page),
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
 */
export function LinksError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamId } = Route.useParams();
	const { page } = Route.useSearch();
	const { data } = useSuspenseQuery(linksQueryOptions(teamId, page));

	return <LinkList data={data} page={page} teamId={teamId} />;
}
