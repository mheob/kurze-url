import { useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, type SearchSchemaInput } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { LinkList } from '../../components/link-list';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { linksQueryOptions } from '../../server/links';
import { assertMembership } from '../_authed';

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
	loader: ({ context, deps, params }) =>
		context.queryClient.ensureQueryData(linksQueryOptions(params.teamId, deps.page)),
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
 */
function LinksError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);
	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamId } = Route.useParams();
	const { page } = Route.useSearch();
	const { data } = useSuspenseQuery(linksQueryOptions(teamId, page));

	return <LinkList data={data} page={page} teamId={teamId} />;
}
