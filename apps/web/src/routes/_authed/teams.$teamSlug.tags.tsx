import type { Tag } from '@kurze-url/api-client';
import { useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { NameManagementBody } from '../../components/name-management-body';
import { useNameMutations } from '../../hooks/use-name-mutations';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { reportUnexpected } from '../../lib/observability';
import { TAGS_PER_TEAM } from '../../lib/tags';
import { canEdit } from '../../lib/team-roles';
import { createTagFn, deleteTagFn, renameTagFn, tagsQueryOptions } from '../../server/tags';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is typed by
   TanStack Router/Query's own option shapes — `beforeLoad`/`loader`'s parameter, and
   `queryOptions()`'s own return type via `ReturnType<typeof tagsQueryOptions>` — none of which
   is a declaration this file can edit. Same deviation `teams.$teamSlug.folders.tsx` documents. */

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `FoldersDataSource`: a real `QueryClient` satisfies this
 * structurally, so the loader needs no cast.
 */
interface TagsDataSource {
	readonly ensureQueryData: (options: ReturnType<typeof tagsQueryOptions>) => Promise<Tag[]>;
}

/**
 * Same shape and reasoning as `loadFolders`: a 401 that survives to this
 * loader must not fall through to `errorComponent` as dead-end inline text —
 * it sends the visitor back to `/login` instead. Every other error kind is
 * rethrown unchanged. Strict, unlike the link pages' `prefetchTags`: this
 * page exists to show the tags, so a list that silently rendered empty would
 * be indistinguishable from a team with none.
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns The team's tags.
 */
export async function loadTags(queryClient: TagsDataSource, teamId: string): Promise<Tag[]> {
	try {
		return await queryClient.ensureQueryData(tagsQueryOptions(teamId));
	} catch (error) {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamSlug/tags')({
	beforeLoad: ({ context, params }) => ({
		role: context.me.memberships.find((membership) => membership.slug === params.teamSlug)?.role,
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	errorComponent: TagsError,
	loader: async ({ context }) => loadTags(context.queryClient, context.teamId),
});

/**
 * Same reasoning as `FoldersError`: a list that silently rendered empty on a
 * failed request would be indistinguishable from a team with no tags, so this
 * fails loudly instead. `kind: 'unauthenticated'` can still reach here on a
 * background refetch, a path `loadTags`'s own try/catch never sees — hence the
 * `<Navigate>`.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function TagsError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { role, teamId } = Route.useRouteContext();
	const { data: items } = useSuspenseQuery(tagsQueryOptions(teamId));
	const mutations = useNameMutations({
		cap: TAGS_PER_TEAM,
		create: async (name) => createTagFn({ data: { name, teamId } }),
		items,
		namespace: 'tags',
		remove: async (tagId) => deleteTagFn({ data: { tagId } }),
		rename: async (tagId, name) => renameTagFn({ data: { name, tagId } }),
		teamId,
	});

	return (
		<NameManagementBody
			canEdit={canEdit(role)}
			items={items}
			namespace="tags"
			teamSlug={teamSlug}
			{...mutations}
		/>
	);
}
