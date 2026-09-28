import type { PageFolder } from '@kurze-url/api-client';
import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { FolderForm } from '../../components/folder-form';
import { FolderList } from '../../components/folder-list';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { FOLDERS_PER_TEAM, folderFailureOf, type FolderFailure } from '../../lib/folders';
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
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const { data } = useSuspenseQuery(foldersQueryOptions(teamId));
	const folders = data.items ?? [];
	const editor = canEdit(role);
	const [createError, setCreateError] = useState<string | undefined>(undefined);
	const [createKey, setCreateKey] = useState(0);
	const [rowError, setRowError] = useState<{ folderId: string; message: string } | null>(null);

	const refresh = async (): Promise<void> => {
		await queryClient.invalidateQueries({ queryKey: ['folders', teamId] });
		await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	};

	const messageFor = (failure: FolderFailure): string | undefined => {
		if (failure === 'unauthenticated') {
			void router.navigate({ to: '/login' });
			return undefined;
		}
		if (failure === 'notFound') return t('folders.notFound');
		if (failure === 'rateLimited' || failure === 'unknown') return t(`errors.${failure}`);
		return t(`folders.${failure}`);
	};

	const create = useMutation({
		mutationFn: async (name: string) => createFolderFn({ data: { name, teamId } }),
		onError: (error: unknown) => {
			setCreateError(messageFor(folderFailureOf(error, folders.length >= FOLDERS_PER_TEAM)));
		},
		onSuccess: async () => {
			setCreateError(undefined);
			setCreateKey((key) => key + 1); // remounts the form, clearing the field
			await refresh();
		},
	});

	const onRename = async (folderId: string, name: string): Promise<boolean> => {
		try {
			await renameFolderFn({ data: { folderId, name } });
			setRowError(null);
			await refresh();
			return true;
		} catch (error) {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { folderId, message });
			return false;
		}
	};

	const remove = useMutation({
		mutationFn: async (folderId: string) => deleteFolderFn({ data: { folderId } }),
		onError: (error: unknown, folderId: string) => {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { folderId, message });
		},
		onSuccess: async () => {
			setRowError(null);
			await refresh();
		},
	});

	return (
		<>
			<h1>{t('folders.heading')}</h1>
			<p>{t('folders.intro')}</p>
			{editor ? (
				<FolderForm
					error={createError}
					key={createKey}
					label={t('folders.name')}
					onSubmit={(name) => {
						create.mutate(name);
					}}
					submitLabel={t('folders.create')}
				/>
			) : null}
			<FolderList
				canEdit={editor}
				folders={folders}
				onDelete={(folderId) => {
					remove.mutate(folderId);
				}}
				onRename={onRename}
				rowError={rowError}
				teamSlug={teamSlug}
			/>
		</>
	);
}
