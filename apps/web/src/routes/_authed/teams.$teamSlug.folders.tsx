import type { PageFolder } from '@kurze-url/api-client';
import { useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { NameManagementBody } from '../../components/name-management-body';
import { useNameMutations } from '../../hooks/use-name-mutations';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { FOLDERS_PER_TEAM } from '../../lib/folders';
import { reportUnexpected } from '../../lib/observability';
import { canEdit } from '../../lib/team-roles';
import {
	createFolderFn,
	deleteFolderFn,
	foldersQueryOptions,
	renameFolderFn,
} from '../../server/folders';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is typed by
   TanStack Router/Query's own option shapes — `beforeLoad`/`loader`'s parameter, and
   `queryOptions()`'s own return type via `ReturnType<typeof foldersQueryOptions>` — none of which
   is a declaration this file can edit. Same deviation `teams.$teamSlug.domains.tsx` documents. */

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `DomainsDataSource`/`LinksDataSource`: a real `QueryClient`
 * satisfies this structurally, so the loader needs no cast.
 */
interface FoldersDataSource {
	readonly ensureQueryData: (
		options: ReturnType<typeof foldersQueryOptions>,
	) => Promise<PageFolder>;
}

/**
 * Same shape and reasoning as `loadDomains`/`loadLinks`: a 401 that survives
 * to this loader must not fall through to `errorComponent` as dead-end
 * inline text — it sends the visitor back to `/login` instead. Every other
 * error kind is rethrown unchanged.
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns The team's folders.
 */
export async function loadFolders(
	queryClient: FoldersDataSource,
	teamId: string,
): Promise<PageFolder> {
	try {
		return await queryClient.ensureQueryData(foldersQueryOptions(teamId));
	} catch (error) {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamSlug/folders')({
	beforeLoad: ({ context, params }) => ({
		role: context.me.memberships.find((membership) => membership.slug === params.teamSlug)?.role,
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	errorComponent: FoldersError,
	loader: async ({ context }) => loadFolders(context.queryClient, context.teamId),
});

/**
 * Same reasoning as `LinksError`/`DomainsError`: a list that silently
 * rendered empty on a failed request would be indistinguishable from a team
 * with no folders, so this fails loudly instead. `kind: 'unauthenticated'`
 * can still reach here on a background refetch, a path `loadFolders`'s own
 * try/catch never sees — hence the `<Navigate>`.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function FoldersError({ error }: { readonly error: unknown }): React.JSX.Element {
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
	const { data } = useSuspenseQuery(foldersQueryOptions(teamId));
	const items = data.items ?? [];
	const mutations = useNameMutations({
		cap: FOLDERS_PER_TEAM,
		create: async (name) => createFolderFn({ data: { name, teamId } }),
		items,
		namespace: 'folders',
		remove: async (folderId) => deleteFolderFn({ data: { folderId } }),
		rename: async (folderId, name) => renameFolderFn({ data: { folderId, name } }),
		teamId,
	});

	return (
		<NameManagementBody
			canEdit={canEdit(role)}
			items={items}
			namespace="folders"
			teamSlug={teamSlug}
			{...mutations}
		/>
	);
}
